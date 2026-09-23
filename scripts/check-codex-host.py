#!/usr/bin/env python3
"""Opt-in real Codex queue/ack/reply check with a designated test task.

Tell that task to use OUTPUT_DIR/brote for the forthcoming read-only question.
The wrapper selects this fixture's private storage and supplied packaged core.
"""
import argparse,json,os,subprocess,time
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--core',required=True);p.add_argument('--thread',required=True);p.add_argument('--output-dir',required=True);a=p.parse_args()
w=Path(a.output_dir).resolve();w.mkdir(parents=True,exist_ok=True);core=str(Path(a.core).resolve())
env={k:v for k,v in os.environ.items() if not k.startswith('OTEL_EXPORTER_OTLP')};private=dict(DEBUG_HANDOVER_HOME=str(w/'sessions'),AGENTDEBUGGER_DATA_DIR=str(w/'data'));env.update(private)
wrapper=w/'brote';wrapper.write_text('#!/usr/bin/env python3\nimport os,sys\nos.environ.update('+repr(private)+')\nos.execv('+repr(core)+','+repr([core])+'+sys.argv[1:])\n');wrapper.chmod(0o700)
source=w/'main.go';source.write_text('package main\nimport "time"\nfunc main(){for{time.Sleep(time.Second)}}\n');target=w/'target';subprocess.run(['go','build','-gcflags=all=-N -l','-o',str(target),str(source)],check=True)
def cli(*args):
 r=subprocess.run([core,*args],env=env,text=True,capture_output=True,timeout=45)
 if r.returncode:raise RuntimeError(r.stderr)
 return json.loads(r.stdout)
s=cli('start','--service','--binary',str(target),'--project',str(w),'--thread',a.thread);sid=s['id']
try:
 body=w/'question.txt';body.write_text('Read-only integration check: explain that the target is paused using the saved evidence. Include BROTE_CODEX_REPLY_OK in the persisted answer. Do not execute the target.')
 q=cli('comment','create',sid,'--file',str(source),'--line','3','--body-file',str(body))['thread'];(w/'fixture.json').write_text(json.dumps(dict(session=sid,thread=q['id'],question=q['delivery']['question'],wrapper=str(wrapper))))
 deadline=time.monotonic()+600;observed=[]
 while time.monotonic()<deadline:
  document=cli('comment','list',sid);thread=document['threads'][0];status=thread['delivery']['status']
  if not observed or observed[-1]!=status:observed.append(status);print(status,flush=True)
  if status=='answered':break
  time.sleep(1)
 assert status=='answered',observed
 answers=[m for m in thread['messages'] if m.get('question')==q['delivery']['question']];assert len(answers)==1 and 'BROTE_CODEX_REPLY_OK' in answers[0]['body'],answers
 state=cli('state',sid,'--brief');assert state['status']=='paused' and not state.get('task'),state
 cli('end-session',sid,'--confirmed');ended=True
 saved=cli('comment','list',sid);assert saved['threads'][0]['messages']==thread['messages']
 (w/'result.json').write_text(json.dumps(dict(codexVersion=subprocess.check_output(['codex','--version'],text=True).strip(),session=sid,observed=observed,oneAnswer=True,taskNotCreated=True,archivedReply=True,delivery=thread['delivery']),indent=2));print('PASS real queue, exact acknowledgement/reply, and archive persistence',flush=True)
finally:
 if not locals().get('ended'):cli('end-session',sid,'--confirmed')

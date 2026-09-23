#!/usr/bin/env python3
"""Opt-in installed Pi + packaged Brote validation using the configured model."""
import argparse, json, os, subprocess, tempfile, uuid, time, urllib.request, urllib.error
from pathlib import Path

def main():
 p=argparse.ArgumentParser();p.add_argument('--extension',required=True);p.add_argument('--core',required=True);p.add_argument('--output',required=True);p.add_argument('--otlp-endpoint',default='http://127.0.0.1:1');p.add_argument('--tempo-url');a=p.parse_args()
 core=str(Path(a.core).resolve());extension=str(Path(a.extension).resolve());output=Path(a.output).resolve();output.parent.mkdir(parents=True,exist_ok=True)
 with tempfile.TemporaryDirectory(prefix='brote-pi-host-') as tmp:
  work=Path(tmp);source=work/'main.go';target=work/'target'
  source.write_text('package main\nimport "fmt"\nfunc work(v int) int {\n total:=v+1\n return total\n}\nfunc main(){for _,v:=range []int{7,8,9}{fmt.Println(work(v))}}\n')
  subprocess.run(['go','build','-gcflags=all=-N -l','-o',str(target),str(source)],check=True)
  env={k:v for k,v in os.environ.items() if not k.startswith('OTEL_EXPORTER_OTLP')};conversation=str(uuid.uuid4())
  (work/'brote').symlink_to(core)
  env['PATH']=str(work)+os.pathsep+env.get('PATH','')
  env.update(DEBUG_HANDOVER_HOME=str(work/'debug'),AGENTDEBUGGER_DATA_DIR=str(work/'data'),DELVE_LLM_ADAPTER_BIN=core,OTEL_EXPORTER_OTLP_ENDPOINT=a.otlp_endpoint,PI_OFFLINE='1',PI_TELEMETRY='0')
  def cli(*args):
   r=subprocess.run([core,*args],env=env,text=True,capture_output=True,timeout=45)
   if r.returncode:raise RuntimeError(r.stderr)
   return json.loads(r.stdout)
  session=cli('start','--service','--binary',str(target),'--project',str(work),'--thread','','--binding','pi:'+conversation,'--name','Pi host validation');sid=session['id']
  question=work/'question.txt';question.write_text('Read-only integration check: explain the saved pause and include BROTE_PI_REPLY_OK. Do not resume or change execution for this question.')
  saved=cli('comment','create',sid,'--file',str(source),'--line','5','--body-file',str(question))['thread']
  prompt=f'''This is an isolated Brote integration test, and I authorize the bounded debugging investigation below. Use the debugger tools for execution and tracepoints. A canonical read-only question will arrive through the managed stream; use bash only for its Brote comment list/ack/reply commands and a temporary answer body file under {work}. The brote command on PATH selects this same packaged core and private storage. After the debugger sequence, finish your turn so the queued canonical follow-up can be processed; do not invent acknowledgement syntax before that message arrives. Persist a reply containing BROTE_PI_REPLY_OK to that delivered question; keep its exact question, recipient, revision and attempt. Replying does not authorize execution. Session {sid}, source {source}, tracepoint line 5. First create a tracepoint with values {{"total":"total"}}, captureLimit 1, owner matching this Pi conversation. Save the returned ID/revision. Start a debugging task with instruction "Run to the bounded tracepoint capture, inspect capture/export results, then leave the target paused". Call debug_execute continue under that task once. Read debug_captures; report the actual capture and OTLP export status without assuming success. Update the tracepoint to enabled false using its current revision. Attempt one update with its old revision and confirm it is rejected. Delete using the new revision. Complete the task. Do not resume again or modify files. End with BROTE_PI_HOST_DONE and concise observed outcomes.'''
  args=['pi','--no-extensions','--no-skills','--no-prompt-templates','--no-context-files','--session-id',conversation,'--session-dir',str(work/'pi'),'--mode','json','--tools','bash,debug_tracepoints,debug_captures,debug_task,debug_execute','-e',extension,'-p',prompt]
  try:
   result=subprocess.run(args,cwd=work,env=env,text=True,capture_output=True,input="",timeout=600)
   events=[]
   for line in result.stdout.splitlines():
    try:event=json.loads(line)
    except ValueError:continue
    if event.get('type') in ('tool_execution_start','tool_execution_end','agent_end','agent_settled'):
     if event.get('type')=='agent_end':event={'type':'agent_end'}
     events.append(event)
   state=cli('state',sid,'--brief');captures=cli('captures',sid);discussion=cli('comment','list',sid)
   evidence={'piVersion':subprocess.check_output(['pi','--version'],text=True).strip(),'session':sid,'exitCode':result.returncode,'events':events,'state':state,'captures':captures,'discussion':discussion,'stderr':result.stderr[-4096:]}
   output.write_text(json.dumps(evidence,indent=2))
   names=[event.get('toolName') for event in events if event.get('type')=='tool_execution_start']
   assert result.returncode==0,(result.stderr,names)
   assert all(name in names for name in ('debug_tracepoints','debug_task','debug_execute','debug_captures')),names
   assert state['status']=='paused',state
   assert state['task']['status']=='completed',state.get('task')
   assert captures['captures'],captures
   captured=next(c for c in captures['captures'] if c['status']=='captured');assert captured['values']['total']['value']=='8',captured
   assert any(c.get('error',{}).get('code')=='capture_limit' for c in captures['captures']),captures
   assert any(e.get('toolName')=='debug_tracepoints' and e.get('isError') and 'stale_revision' in json.dumps(e) for e in events),events
   if a.tempo_url:
    assert captured['exportStatus'] in ('queued','sent'),captured
    trace_id=captured['programTraceId'];deadline=time.monotonic()+30;found=False
    while time.monotonic()<deadline:
     try:
      request=urllib.request.Request(a.tempo_url.rstrip('/')+'/api/traces/'+trace_id,headers={'Accept':'application/json'})
      with urllib.request.urlopen(request,timeout=5) as response:payload=json.load(response)
      spans=[span for batch in payload.get('batches',payload.get('resourceSpans',[])) for scope in batch.get('scopeSpans',batch.get('instrumentationLibrarySpans',[])) for span in scope.get('spans',[])]
      found=any(any(attr['key']=='program.value.total' and attr['value'].get('stringValue',attr['value'].get('intValue'))=='8' for attr in span.get('attributes',[])) for span in spans)
      if found:break
     except urllib.error.HTTPError as error:
      if error.code!=404:raise
     time.sleep(.25)
    assert found,trace_id
    evidence['tempoRetrieved']=trace_id;output.write_text(json.dumps(evidence,indent=2))
   assert not state['definitions']['items'],state['definitions']
   thread=next(t for t in discussion['threads'] if t['id']==saved['id']);answers=[m for m in thread['messages'] if m.get('question')==saved['delivery']['question']]
   assert thread['delivery']['status']=='answered' and len(answers)==1 and 'BROTE_PI_REPLY_OK' in answers[0]['body'],thread
   print(json.dumps({'piVersion':evidence['piVersion'],'realModelToolCalls':len(names),'paused':True,'taskCompleted':True,'managedReplyOnce':True,'captures':len(captures['captures']),'evidence':str(output)}))
  finally:cli('end-session',sid,'--confirmed')
if __name__=='__main__':main()

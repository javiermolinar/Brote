import json, os, signal, subprocess, tempfile, time, urllib.request, urllib.error
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix='brote-review-recovery-') as td:
 w=Path(td); env={k:v for k,v in os.environ.items() if not k.startswith('OTEL_EXPORTER_OTLP')}
 env.update(DEBUG_HANDOVER_HOME=str(w/'sessions'),AGENTDEBUGGER_DATA_DIR=str(w/'data'))
 core=w/'brote'; target=w/'target'; source=w/'main.go'
 source.write_text('package main\nimport "time"\nfunc main(){for{time.Sleep(time.Second)}}\n')
 subprocess.run(['go','build','-o',str(core),'./cmd/brote'],cwd=ROOT,check=True)
 subprocess.run(['go','build','-gcflags=all=-N -l','-o',str(target),str(source)],check=True)
 def cli(*args):
  r=subprocess.run([str(core),*args],env=env,text=True,capture_output=True,timeout=30)
  if r.returncode: raise RuntimeError(r.stderr)
  return json.loads(r.stdout)
 s=cli('start','--service','--binary',str(target),'--project',str(w),'--thread','','--binding','pi:test','--name','Pi');sid=s['id']
 def descriptor():return json.loads((w/'sessions'/sid/'session.json').read_text())
 def action(a):
  d=descriptor();a=dict(a,run=d['run'])
  req=urllib.request.Request(d['http']+'/api/action',data=json.dumps(a).encode(),headers={'Authorization':'Bearer '+d['token'],'Content-Type':'application/json'})
  try: return json.load(urllib.request.urlopen(req,timeout=10))
  except urllib.error.HTTPError as e: raise RuntimeError(e.read().decode()) from e
 try:
  body=w/'question.txt';body.write_text('Why is this paused?')
  q=cli('comment','create',sid,'--file',str(source),'--line','3','--body-file',str(body))['thread']
  r=dict(kind='agent',**descriptor()['binding'])
  c=action(dict(action='consumer-open',consumer='original',recipient=r))['consumer']
  claimed=action(dict(action='consumer-next',consumer=c['id'],instance=c['instance']))['delivery']
  assert claimed['kind']=='question'
  os.kill(descriptor()['brokerPid'],signal.SIGKILL);time.sleep(.25)
  cli('recover',sid)
  state=cli('comment','list',sid)['threads'][0]['delivery']['status']
  other=action(dict(action='consumer-open',consumer='replacement-reader',recipient=r))['consumer']
  fresh=action(dict(action='consumer-next',consumer=other['id'],instance=other['instance']))
  retry=subprocess.run([str(core),'comment','retry',sid,q['id']],env=env,text=True,capture_output=True,timeout=10)
  print(json.dumps(dict(statusAfterRecovery=state,newReaderDelivery=fresh.get('delivery'),explicitRetryExit=retry.returncode,explicitRetryError=retry.stderr.strip())))
  assert fresh.get('delivery') is None and retry.returncode == 0
  assert state=='unknown', 'interrupted question remained sending on broker recovery'
  provider=cli('comment','create',sid,'--file',str(source),'--line','3','--body-file',str(body),'--recipient-kind','provider','--recipient-id','fixture-model')['thread']
  assert descriptor()['binding']['id']=='pi:test' and not descriptor().get('task')
  # Retry through the live CLI, browser-shaped HTTP and offline CLI must retain the provider.
  def fail_provider():
   claimed=cli('comment','claim',sid,provider['id'],'--question',provider['delivery']['question'],'--recipient-kind','provider','--recipient-id','fixture-model')['delivery']
   cli('comment','answer-failed',sid,provider['id'],'--question',provider['delivery']['question'],'--recipient-kind','provider','--recipient-id','fixture-model','--attempt',claimed['attempt'],'--error','Fixture failure')
  for transport in ('cli','http','offline'):
   fail_provider()
   if transport=='http':
    d=descriptor();req=urllib.request.Request(d['http']+'/api/comments',data=json.dumps(dict(action='retry',thread=provider['id'])).encode(),headers={'Authorization':'Bearer '+d['token'],'Content-Type':'application/json'})
    retried=json.load(urllib.request.urlopen(req,timeout=10))
   else:retried=cli('comment','retry',sid,provider['id'],*(['--offline'] if transport=='offline' else []))
   assert retried['thread']['delivery']['recipient']['id']=='fixture-model',retried
  print(json.dumps(dict(providerRetryParity=['cli','http','offline'])))
  claim=cli('comment','claim',sid,provider['id'],'--question',provider['delivery']['question'],'--recipient-kind','provider','--recipient-id','fixture-model')['delivery']
  cli('end-session',sid,'--confirmed')
  body.write_text('Historical answer after the debugger ended.')
  reply=cli('comment','reply',sid,provider['id'],'--offline','--question',provider['delivery']['question'],'--recipient-kind','provider','--recipient-id','fixture-model','--attempt',claim['attempt'],'--message-id','fixture-answer','--body-file',str(body))
  assert reply['thread']['delivery']['status']=='answered'
  print(json.dumps(dict(providerDidNotRebind=True,offlineReplyAfterExit=True)))
 finally:
  if not descriptor().get('stopped'): cli('end-session',sid,'--confirmed')

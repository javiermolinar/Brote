const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const os=require('node:os');
const {execFile,spawn}=require('node:child_process');
const {promisify}=require('node:util');
const exec=promisify(execFile),sleep=ms=>new Promise(r=>setTimeout(r,ms));
const binary=path.resolve('bin/brote');
test('shared Go core captures native and broker traces, survives restart and isolates remote failure',{skip:!process.env.BROTE_CORE_INTEGRATION,timeout:150000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-core-'));
 const env={...process.env,AGENTDEBUGGER_DATA_DIR:path.join(dir,'data'),DEBUG_HANDOVER_HOME:path.join(dir,'sessions'),OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:'http://127.0.0.1:1/v1/traces',OTEL_EXPORTER_OTLP_TRACES_HEADERS:'Authorization=Basic%20secret'};
 const cli=async(...args)=>JSON.parse((await exec(binary,args,{env,timeout:40000,maxBuffer:16<<20})).stdout);
 const query=async id=>{for(let i=0;;i++){try{return await cli('trace',id);}catch(error){if(i>=60)throw error;await sleep(500);}}};
 let endpoint,broker;
 async function stopService(){let pid;try{pid=Number(await fs.readFile(path.join(env.AGENTDEBUGGER_DATA_DIR,'tracing/service.pid'),'utf8'));process.kill(pid,'SIGTERM');}catch{return;}for(let i=0;i<200;i++){try{process.kill(pid,0);}catch{return;}await sleep(100);}throw Error('core did not stop');}
 try{
  const starts=await Promise.all([cli('trace-service'),cli('trace-service'),cli('trace-service')]);endpoint=starts[0].endpoint;assert.ok(starts.every(s=>s.endpoint===endpoint),'one shared runtime');
  const event=async body=>{const r=await fetch(endpoint+'/api/trace-events',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({session:'native-test',...body})});assert.equal(r.status,200,await r.clone().text());return r.json();};
  const record=await event({kind:'start',name:'native-program',adapter:'go'});
  await event({kind:'request',seq:1,command:'next',thread:7});await event({kind:'stopped',thread:7});await event({kind:'response',seq:1,success:true});
  await event({kind:'snapshot',observation:{thread:7,frame:{name:'main.work'},stack:[{name:'main.work'}],scopes:[{name:'Locals',variables:[{name:'total',value:'42',type:'int'}]}],capturedAt:new Date().toISOString()},selections:{total:'total'}});
  const closed=await event({kind:'close'});assert.equal(closed.local[record.program],'Export accepted; query to verify');assert.equal(closed.remote[record.program],'Export failed or incomplete');
  const trace=await query(record.program);assert.ok(JSON.stringify(trace).includes('42'));assert.equal(trace.batches.flatMap(b=>b.scopeSpans.flatMap(s=>s.spans)).length,3);
  await fs.writeFile(path.join(dir,'main.go'),'package main\nimport "fmt"\nfunc main(){\n value:=42\n fmt.Println(value)\n}\n');
  await exec('go',['build','-gcflags=all=-N -l','-o',path.join(dir,'demo'),path.join(dir,'main.go')]);
  broker=await cli('start','--binary',path.join(dir,'demo'),'--project',dir,'--no-ui','--thread','','--name','Pi');
  const id=broker.id || broker.session?.id;assert.ok(id,JSON.stringify(broker));
  await cli('break',id,'--file',path.join(dir,'main.go'),'--line','5');await cli('continue',id,'--human','--wait','10s');await cli('state',id);
  let records;for(let i=0;i<100;i++){records=await cli('traces');if(records.some(r=>r.session===id&&Object.values(r.local).includes('Export accepted; query to verify')))break;await sleep(100);}
  const brokerRecord=records.find(r=>r.session===id);assert.ok(brokerRecord,'broker automatically captured without an editor');
  await cli('end-session',id,'--confirmed');broker=undefined;
  for(let i=0;i<100;i++){records=await cli('traces');if(records.find(r=>r.session===id)?.closed)break;await sleep(100);}
  const brokerTrace=await query(brokerRecord.program);assert.ok(JSON.stringify(brokerTrace).includes('main.main'));assert.ok(JSON.stringify(brokerTrace).includes('42'),'broker captures inspected local values');
  const active=await event({kind:'start',session:'active-restart',name:'active producer'});
  await event({kind:'record',session:'active-restart',command:'before restart'});
  await event({kind:'flush',session:'active-restart'});
  await stopService();const next=await cli('trace-service');endpoint=next.endpoint;
  const resumed=await event({kind:'record',session:'active-restart',command:'after restart'});
  assert.equal(resumed.program,active.program);assert.equal(resumed.closed,false);assert.equal(resumed.incomplete,true);assert.notEqual(resumed.run,active.run);
  await event({kind:'close',session:'active-restart'});
  const resumedTrace=JSON.stringify(await query(active.debugger));assert.ok(resumedTrace.includes('before restart'));assert.ok(resumedTrace.includes('after restart'));
  const saved=await cli('traces');assert.equal(saved.find(r=>r.session==='native-test').program,record.program);assert.ok(JSON.stringify(await query(record.program)).includes('42'));assert.ok(JSON.stringify(await query(brokerRecord.program)).includes('main.main'));
  const paths=await fs.readdir(path.join(env.AGENTDEBUGGER_DATA_DIR,'tracing/sessions'));for(const name of paths)assert.ok(!(await fs.readFile(path.join(env.AGENTDEBUGGER_DATA_DIR,'tracing/sessions',name),'utf8')).includes('Basic secret'));
  await fs.mkdir('dist/embedded-tempo',{recursive:true});await fs.writeFile('dist/embedded-tempo/core-integration.json',JSON.stringify({native:record.program,broker:brokerRecord.program,dataDir:env.AGENTDEBUGGER_DATA_DIR,restarted:true,remoteFailureIsolated:true},null,2));
 }finally{if(broker){const id=broker.id||broker.session?.id;try{await cli('end-session',id,'--confirmed');}catch{}}await stopService();}
});

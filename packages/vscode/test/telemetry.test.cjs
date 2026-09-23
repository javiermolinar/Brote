const {test}=require('node:test');
const assert=require('node:assert/strict');
const {buildSync}=require('esbuild');
const Module=require('node:module');
const path=require('node:path');
const {createServer}=require('node:http');
const output=buildSync({entryPoints:['packages/vscode/test/fixtures/telemetry.ts'],bundle:true,platform:'node',format:'cjs',packages:'external',write:false}).outputFiles[0].text;
const mod=new Module(path.join(process.cwd(),'telemetry-test.cjs'));mod.paths=Module._nodeModulePaths(process.cwd());mod._compile(output,path.join(process.cwd(),'telemetry-test.cjs'));
const {exportConfig,SessionTrace,createSessionTrace}=mod.exports;
function harness(){const spans=[];const trace=new SessionTrace('session','test-program','go',()=>({export(batch,done){spans.push(...batch);done({code:0});},shutdown:async()=>{}}));return {trace,spans};}
const observation=()=>({thread:7,capturedAt:new Date().toISOString(),frame:{name:'main.process',line:17},stack:[{name:'main.process'},{name:'main.worker'}],scopes:[{name:'Locals',variables:[{name:'total',value:'9007199254740993',type:'int64',variablesReference:0}]}]});
test('OTLP is opt-in and configuration rejects unsafe or unsupported input',()=>{
 assert.deepEqual(exportConfig({OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:'http://localhost:4318/v1/traces'}),{url:'http://localhost:4318/v1/traces',headers:{}});
 assert.deepEqual(exportConfig({OTEL_EXPORTER_OTLP_ENDPOINT:'http://localhost:4318/base/',OTEL_EXPORTER_OTLP_HEADERS:'Authorization=Bearer%20abc'}),{url:'http://localhost:4318/base/v1/traces',headers:{Authorization:'Bearer abc'}});
 assert.equal(exportConfig({OTEL_EXPORTER_OTLP_ENDPOINT:'http://localhost:4318',OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:'https://example.test/traces'}).url,'https://example.test/traces');
 for(const env of [{OTEL_EXPORTER_OTLP_PROTOCOL:'grpc'},{OTEL_EXPORTER_OTLP_ENDPOINT:'http://user:password@localhost'},{OTEL_EXPORTER_OTLP_HEADERS:'bad=%0aX-test:yes'}])assert.throws(()=>exportConfig({OTEL_EXPORTER_OTLP_ENDPOINT:'http://localhost:4318',...env}));
});
test('execution waits for both response and stop, including event before response',async()=>{
 const {trace,spans}=harness();trace.request(1,'continue',7);trace.response(1,true);await trace.flush();assert.equal(spans.length,0);
 trace.stopped();trace.request(2,'next',7);trace.stopped();await trace.flush();assert.equal(spans.length,1);
 trace.response(2,true);trace.request(3,'stepIn',7);trace.response(3,false);await trace.close();
 assert.deepEqual(spans.filter(s=>s.attributes['debugger.command']).map(s=>[s.name,s.attributes['debugger.outcome']]),[['continue','stopped'],['next','stopped'],['stepIn','error']]);
 const root=spans.find(s=>s.name==='debugger.session');assert.ok(root);
 for(const span of spans.filter(s=>s.attributes['debugger.command']))assert.equal(span.parentSpanContext.spanId,root.spanContext().spanId);
});
test('single-thread stop does not complete another thread execution',async()=>{
 const {trace,spans}=harness();trace.request(1,'next',7);trace.response(1,true);trace.stopped('stopped',8,false);await trace.flush();assert.equal(spans.length,0);
 trace.stopped('stopped',7,false);await trace.close();assert.equal(spans.find(s=>s.name==='next').attributes['debugger.outcome'],'stopped');
});
test('snapshots have stable names, shared program trace, exact named values and bounded JSON',async()=>{
 const {trace,spans}=harness();trace.snapshot(observation(),'worker.result',{total:'total'});trace.snapshot({...observation(),thread:99},'worker.result',{total:'total'});
 const huge=observation();huge.scopes=[{variables:[{name:'total',value:'x'.repeat(40000)}]}];trace.snapshot(huge,'large',{total:'total'});
 await trace.close();const captures=spans.filter(s=>s.attributes['program.span.type']==='snapshot');assert.equal(captures.length,3);
 assert.equal(captures[0].attributes['program.value.total'],'9007199254740993');assert.equal(captures[0].name,captures[1].name);
 assert.equal(captures[2].attributes['program.value_status.total'],'not_captured');assert.equal(captures[2].attributes['program.capture.status'],'partial');
 for(const span of captures){assert.equal(span.spanContext().traceId,trace.programTraceID);assert.ok(Buffer.byteLength(span.attributes['program.snapshot.json'])<=32768);JSON.parse(span.attributes['program.snapshot.json']);}
 const threads=spans.filter(s=>s.attributes['program.span.type']==='thread');assert.deepEqual(threads.map(s=>s.name),['thread observed at main.worker','thread observed at main.worker']);assert.notEqual(trace.programTraceID,trace.debuggerTraceID);
});
test('shutdown is idempotent and ignores late captures',async()=>{
 const {trace,spans}=harness();trace.request(1,'continue');await trace.close();trace.snapshot(observation());await trace.close();assert.equal(spans.length,3);assert.equal(spans.find(s=>s.name==='continue').attributes['debugger.outcome'],'interrupted');
});
test('SDK sends authenticated protobuf to the configured HTTP endpoint',async()=>{
 const requests=[];const server=createServer((req,res)=>{const chunks=[];req.on('data',chunk=>chunks.push(chunk));req.on('end',()=>{requests.push({url:req.url,headers:req.headers,body:Buffer.concat(chunks)});res.writeHead(200,{'content-type':'application/x-protobuf'});res.end();});});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try{const trace=createSessionTrace('http-test','test-program','go',{url:`http://127.0.0.1:${server.address().port}/v1/traces`,headers:{Authorization:'Bearer test'}});trace.snapshot(observation());await trace.close();assert.equal(requests.length,2);for(const req of requests){assert.equal(req.url,'/v1/traces');assert.equal(req.headers.authorization,'Bearer test');assert.equal(req.headers['content-type'],'application/x-protobuf');assert.ok(req.body.length>0);}}
 finally{await new Promise(resolve=>server.close(resolve));}
});
test('exports a retrievable program hierarchy to real Tempo',{skip:!process.env.BROTE_TEMPO_INTEGRATION},async()=>{
 const trace=createSessionTrace(`native-${Date.now()}`,'brote-native-test','go',exportConfig({OTEL_EXPORTER_OTLP_ENDPOINT:process.env.OTEL_EXPORTER_OTLP_ENDPOINT || 'http://127.0.0.1:4318'}));
 trace.request(1,'continue',7);trace.response(1,true);trace.stopped();trace.snapshot(observation(),'worker.result',{total:'total'});trace.snapshot({...observation(),thread:8},'worker.result',{total:'total'});await trace.close();
 let spans=[];for(let i=0;i<30;i++){const response=await fetch(`${process.env.BROTE_TEMPO_QUERY_URL || 'http://localhost:3200'}/api/traces/${trace.programTraceID}`,{headers:{Accept:'application/json'}});if(response.ok){const data=await response.json();spans=(data.batches||data.resourceSpans||[]).flatMap(b=>(b.scopeSpans||b.instrumentationLibrarySpans||[]).flatMap(s=>s.spans||[]));if(spans.length===5)break;}await new Promise(resolve=>setTimeout(resolve,500));}
 assert.equal(spans.length,5);const snapshots=spans.filter(s=>s.name==='worker.result');assert.equal(snapshots.length,2);
 const root=spans.find(s=>s.name==='run brote-native-test');const threads=spans.filter(s=>s.name.startsWith('thread observed'));for(const thread of threads)assert.equal(thread.parentSpanId,root.spanId);for(const snapshot of snapshots){assert.ok(threads.some(t=>t.spanId===snapshot.parentSpanId));assert.equal(snapshot.attributes.find(a=>a.key==='program.value.total').value.stringValue,'9007199254740993');}
 console.log(`Tempo program trace: ${trace.programTraceID}; debugger trace: ${trace.debuggerTraceID}`);
});

test('oversized UTF-8 names are bounded in spans and resources',async()=>{
 const spans=[],large='🪴'.repeat(256*1024);
 const trace=new SessionTrace('bounded-names',large,large,()=>({export(batch,done){spans.push(...batch);done({code:0});},shutdown:async()=>{}}));
 const capture={...observation(),frame:{name:large},stack:[{name:large}]};
 trace.snapshot(capture);trace.snapshot({...capture,thread:8},large);
 await trace.close();
 assert.equal(spans.length,6);
 for(const span of spans){
  assert.ok(Buffer.byteLength(span.name)<=512);
  assert.equal(Buffer.from(span.name).toString('utf8'),span.name,'no split surrogate pairs');
  assert.ok(Buffer.byteLength(span.resource.attributes['service.name'])<=512);
  assert.ok(Buffer.byteLength(span.resource.attributes['debugger.adapter.type'])<=128);
 }
 assert.ok(spans.some(s=>s.name.startsWith('run ')));
 assert.ok(spans.some(s=>s.name.startsWith('thread observed at ')));
});

test('trace-specific headers take precedence and decode Cloud Basic auth',()=>{
 const config=exportConfig({OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:'https://example.test/otlp/v1/traces',OTEL_EXPORTER_OTLP_HEADERS:'Authorization=wrong',OTEL_EXPORTER_OTLP_TRACES_HEADERS:'Authorization=Basic%20aWQ6dG9rZW4='});
 assert.deepEqual(config,{url:'https://example.test/otlp/v1/traces',headers:{Authorization:'Basic aWQ6dG9rZW4='}});
 assert.equal(exportConfig({OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:'  ',OTEL_EXPORTER_OTLP_ENDPOINT:'https://example.test/otlp'}).url,'https://example.test/otlp/v1/traces');
});
test('dual export keeps identities and credentials separate when remote auth fails',async()=>{
 const local=[],remote=[];
 async function receiver(calls,status){const server=createServer((req,res)=>{const chunks=[];req.on('data',c=>chunks.push(c));req.on('end',()=>{calls.push({auth:req.headers.authorization,body:Buffer.concat(chunks)});res.writeHead(status,{'content-type':'application/x-protobuf'});res.end();});});await new Promise(r=>server.listen(0,'127.0.0.1',r));return server;}
 const a=await receiver(local,200),b=await receiver(remote,401);const outcomes=[];
 try{
  const trace=createSessionTrace('dual','program','go',{url:`http://127.0.0.1:${a.address().port}/v1/traces`,headers:{Authorization:'Bearer local'}},{url:`http://127.0.0.1:${b.address().port}/v1/traces`,headers:{Authorization:'Basic cloud'}},(destination,success)=>outcomes.push({destination,success}));
  trace.snapshot(observation());await trace.close();
  assert.equal(local.length,2);assert.equal(remote.length,2);
  assert.ok(local.every(r=>r.auth==='Bearer local'));assert.ok(remote.every(r=>r.auth==='Basic cloud'));
  // The same protobuf payload goes to each destination, preserving trace/span IDs.
  for(const request of local)assert.ok(remote.some(r=>r.body.equals(request.body)));
  assert.ok(outcomes.some(r=>r.destination==='local'&&r.success));assert.ok(outcomes.some(r=>r.destination==='remote'&&!r.success));
 }finally{await Promise.all([new Promise(r=>a.close(r)),new Promise(r=>b.close(r))]);}
});

test('local exporter waits for readiness without delaying tracker creation',async()=>{
 const calls=[];const server=createServer((req,res)=>{req.resume();req.on('end',()=>{calls.push(req.url);res.writeHead(200,{'Content-Type':'application/x-protobuf'});res.end();});});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try{
  let ready;const config=new Promise(resolve=>{ready=resolve;});const trace=createSessionTrace('pending','program','go',config);trace.snapshot(observation());
  const closed=trace.close();assert.match(trace.programTraceID,/^[a-f0-9]{32}$/);assert.equal(calls.length,0);
  ready({url:`http://127.0.0.1:${server.address().port}/v1/traces`,headers:{}});await closed;assert.equal(calls.length,2);
 }finally{await new Promise(resolve=>server.close(resolve));}
});

test('ambient remote headers and compression cannot contaminate local export',async()=>{
 const keys=['OTEL_EXPORTER_OTLP_HEADERS','OTEL_EXPORTER_OTLP_TRACES_HEADERS','OTEL_EXPORTER_OTLP_COMPRESSION'];const before=Object.fromEntries(keys.map(k=>[k,process.env[k]]));
 const local=[],remote=[];
 async function listen(calls){const server=createServer((req,res)=>{req.resume();req.on('end',()=>{calls.push(req.headers);res.writeHead(200,{'Content-Type':'application/x-protobuf'});res.end();});});await new Promise(r=>server.listen(0,'127.0.0.1',r));return server;}
 const a=await listen(local),b=await listen(remote);
 try{
  process.env.OTEL_EXPORTER_OTLP_HEADERS='Authorization=Basic%20generic-secret,x-remote-key=generic-secret';
  process.env.OTEL_EXPORTER_OTLP_TRACES_HEADERS='x-trace-key=trace-secret';process.env.OTEL_EXPORTER_OTLP_COMPRESSION='gzip';
  const config=exportConfig({...process.env,OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:`http://127.0.0.1:${b.address().port}/v1/traces`});
  const trace=createSessionTrace('ambient','program','go',{url:`http://127.0.0.1:${a.address().port}/v1/traces`,headers:{Authorization:'Bearer local-only'}},config);trace.snapshot(observation());await trace.close();
  assert.equal(local.length,2);assert.equal(remote.length,2);
  for(const h of local){assert.equal(h.authorization,'Bearer local-only');assert.equal(h['x-remote-key'],undefined);assert.equal(h['x-trace-key'],undefined);assert.equal(h['content-encoding'],undefined);}
  for(const h of remote){assert.equal(h.authorization,undefined);assert.equal(h['x-remote-key'],undefined);assert.equal(h['x-trace-key'],'trace-secret');assert.equal(h['content-encoding'],'gzip');}
  local.length=0;remote.length=0;process.env.OTEL_EXPORTER_OTLP_TRACES_HEADERS='';
  const blank=exportConfig({...process.env,OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:`http://127.0.0.1:${b.address().port}/v1/traces`});
  await createSessionTrace('empty-headers','program','go',{url:`http://127.0.0.1:${a.address().port}/v1/traces`,headers:{Authorization:'Bearer local-only'}},blank).close();
  assert.equal(remote.length,2);for(const h of remote){assert.equal(h.authorization,undefined);assert.equal(h['x-remote-key'],undefined);assert.equal(h['x-trace-key'],undefined);}
 }finally{for(const k of keys){if(before[k]===undefined)delete process.env[k];else process.env[k]=before[k];}await Promise.all([new Promise(r=>a.close(r)),new Promise(r=>b.close(r))]);}
});

test('local shutdown waits past the export timeout for runtime readiness',{timeout:7000},async()=>{
 const calls=[],outcomes=[];const server=createServer((req,res)=>{req.resume();req.on('end',()=>{calls.push(req.url);res.writeHead(200,{'Content-Type':'application/x-protobuf'});res.end();});});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try{
  let ready;const config=new Promise(resolve=>{ready=resolve;});
  const trace=createSessionTrace('slow-start','program','go',config,undefined,(...result)=>outcomes.push(result));trace.snapshot(observation());
  let closed=false;const closing=trace.close().then(()=>{closed=true;});
  await new Promise(resolve=>setTimeout(resolve,2200));
  assert.equal(closed,false,'shutdown must not overtake the deferred local exports');assert.equal(calls.length,0);
  ready({url:`http://127.0.0.1:${server.address().port}/v1/traces`,headers:{}});
  await closing;assert.equal(calls.length,2);assert.ok(outcomes.every(([,success])=>success));
 }finally{await new Promise(resolve=>server.close(resolve));}
});

test('failed runtime readiness drains retained batches with failure status',async()=>{
 let fail;const ready=new Promise((_,reject)=>{fail=reject;});const outcomes=[];
 const trace=createSessionTrace('failed-start','program','go',ready,undefined,(...result)=>outcomes.push(result));trace.snapshot(observation());
 const closing=trace.close();fail(new Error('startup failed'));
 await assert.rejects(closing,/Local trace export failed/);
 assert.equal(outcomes.length,2);assert.ok(outcomes.every(([,success])=>!success));
});

test('private in-process push and remote OTLP preserve payloads independently',async()=>{
 const local=[],remote=[],outcomes=[];
 const server=createServer((req,res)=>{const chunks=[];req.on('data',c=>chunks.push(c));req.on('end',()=>{remote.push({headers:req.headers,body:Buffer.concat(chunks)});res.writeHead(200,{'Content-Type':'application/x-protobuf'});res.end();});});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try{
  const trace=createSessionTrace('private-dual','program','go',{url:'',headers:{},push:async bytes=>{local.push(Buffer.from(bytes));}},
   {url:`http://127.0.0.1:${server.address().port}/v1/traces`,headers:{Authorization:'Basic cloud'}},(...result)=>outcomes.push(result));
  trace.snapshot(observation());await trace.close();
  assert.equal(local.length,2);assert.equal(remote.length,2);assert.ok(outcomes.every(([,ok])=>ok));
  for(const body of local)assert.ok(remote.some(request=>request.body.equals(body)));
  assert.ok(remote.every(request=>request.headers.authorization==='Basic cloud'));
 }finally{await new Promise(resolve=>server.close(resolve));}
});

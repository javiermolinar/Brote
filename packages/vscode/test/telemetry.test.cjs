const {test}=require('node:test');
const assert=require('node:assert/strict');
const {buildSync}=require('esbuild');
const Module=require('node:module');
const path=require('node:path');
const {createServer}=require('node:http');
const output=buildSync({entryPoints:['packages/vscode/src/telemetry.ts'],bundle:true,platform:'node',format:'cjs',packages:'external',write:false}).outputFiles[0].text;
const mod=new Module(path.join(process.cwd(),'telemetry-test.cjs'));mod.paths=Module._nodeModulePaths(process.cwd());mod._compile(output,path.join(process.cwd(),'telemetry-test.cjs'));
const {exportConfig,SessionTrace,createSessionTrace}=mod.exports;
function harness(){const spans=[];const trace=new SessionTrace('session','test-program','go',()=>({export(batch,done){spans.push(...batch);done({code:0});},shutdown:async()=>{}}));return {trace,spans};}
const observation=()=>({thread:7,capturedAt:new Date().toISOString(),frame:{name:'main.process',line:17},stack:[{name:'main.process'},{name:'main.worker'}],scopes:[{name:'Locals',variables:[{name:'total',value:'9007199254740993',type:'int64',variablesReference:0}]}]});
test('OTLP is opt-in and configuration rejects unsafe or unsupported input',()=>{
 assert.equal(exportConfig({OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:'http://localhost:4318/v1/traces'}),undefined);
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

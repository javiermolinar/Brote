const {test}=require('node:test');
const assert=require('node:assert/strict');
const {buildSync}=require('esbuild');
const Module=require('node:module');
const path=require('node:path');
const code=buildSync({entryPoints:['packages/vscode/src/coreTrace.ts'],bundle:true,platform:'node',format:'cjs',write:false}).outputFiles[0].text;
const m=new Module(path.resolve('core-trace-test.cjs'));m.paths=Module._nodeModulePaths(process.cwd());m._compile(code,path.resolve('core-trace-test.cjs'));
const {CoreSessionTrace}=m.exports;
test('native transport buffers in order until core startup resolves and closes once',async()=>{
 let release;const gate=new Promise(r=>release=r),events=[];
 const core={async request(route,event){assert.equal(route,'trace-events');if(event.kind==='start')await gate;events.push(event);return {session:event.session,program:'a'.repeat(32),debugger:'b'.repeat(32)};}};
 const trace=new CoreSessionTrace(core,'native','demo','go',()=>{},()=>{});
 trace.request(1,'next',7);trace.stopped('stopped',7,false);trace.response(1,true);const closing=trace.close();assert.equal(events.length,0);release();await closing;await trace.close();assert.deepEqual(events.map(e=>e.kind),['start','request','stopped','response','close']);
});
test('startup failure does not retry every buffered event or leave close pending',async()=>{
 let calls=0,reports=0;
 const trace=new CoreSessionTrace({async request(){calls++;throw Error('unavailable');}},'failure','demo','go',()=>{},()=>reports++);
 for(let i=0;i<100;i++)trace.request(i,'next');await trace.close();assert.equal(calls,1);assert.equal(reports,1);
});
test('producer backlog is bounded and marks subsequent records incomplete',async()=>{
 let release;const gate=new Promise(r=>release=r),records=[],events=[];let calls=0;
 const trace=new CoreSessionTrace({async request(route,event){calls++;await gate;events.push(event);return {program:'a'.repeat(32),debugger:'b'.repeat(32)};}},'bounded','demo','go',r=>records.push(r),()=>{});
 for(let i=0;i<1000;i++)trace.request(i,'next');const closing=trace.close();release();await closing;assert.ok(calls<=257);assert.ok(records.every(r=>r.local[r.program]==='Export failed or incomplete'));
 assert.equal(events.at(-1).kind,'close');assert.equal(events.at(-1).incomplete,true,'loss must reach the shared core, not just the local record');
});

test('established session reports a transport gap when close reconnects',async()=>{
 const events=[];let failedOnce=false;
 const trace=new CoreSessionTrace({async request(route,event){
  if(event.kind==='request'&&!failedOnce){failedOnce=true;throw Error('transport lost');}
  events.push(event);return {program:'a'.repeat(32),debugger:'b'.repeat(32)};
 }},'recover','demo','go',()=>{},()=>{});
 await trace.flush();trace.request(1,'next');await trace.close();
 assert.equal(events.at(-1).kind,'close');assert.equal(events.at(-1).incomplete,true);
});

test('capture selections are copied out of configuration objects before JSON transport',async()=>{
 const sent=[];const selections={total:'total'};Object.defineProperty(selections,'toJSON',{value:()=>undefined});
 const trace=new CoreSessionTrace({async request(route,event){sent.push(JSON.parse(JSON.stringify(event)));return {program:'a'.repeat(32),debugger:'b'.repeat(32)};}},'selection','demo','go',()=>{},()=>{});
 trace.snapshot({thread:1,frame:{},stack:[],scopes:[],capturedAt:new Date().toISOString()},'work',selections);await trace.close();assert.deepEqual(sent.find(e=>e.kind==='snapshot').selections,{total:'total'});
});

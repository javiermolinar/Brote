const {test}=require('node:test');
const assert=require('node:assert/strict');
const {buildSync}=require('esbuild');
const Module=require('node:module');
const path=require('node:path');
const fs=require('node:fs/promises');
const os=require('node:os');
const {spawn,spawnSync}=require('node:child_process');
function load(file){const output=buildSync({entryPoints:[file],bundle:true,platform:'node',format:'cjs',packages:'external',write:false}).outputFiles[0].text;const m=new Module(path.join(process.cwd(),'integration.cjs'));m.paths=Module._nodeModulePaths(process.cwd());m._compile(output,path.join(process.cwd(),'integration.cjs'));return m.exports;}
const {TraceRuntime}=load('packages/vscode/test/fixtures/traceRuntime.ts');
const {createSessionTrace}=load('packages/vscode/test/fixtures/telemetry.ts');
const sleep=ms=>new Promise(r=>setTimeout(r,ms));

test('bundled runtime persists complete snapshots through maintenance and restart',{skip:!process.env.BROTE_EMBEDDED_INTEGRATION,timeout:240000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-durable-'));
 const binary=path.resolve('bin/brote');let runtime=new TraceRuntime(binary,dir,console.log);
 const ids=[];const expected=[];
 try{
  let config=await runtime.start();
  const second=spawn(binary,['embedded-tempo',dir],{stdio:['pipe','ignore','ignore']});
  assert.notEqual(await new Promise(r=>second.once('exit',r)),0,'second writer must fail');
  for(let i=0;i<3;i++){
   const trace=createSessionTrace('durability-'+i,'durable-program','go',config,{url:'http://127.0.0.1:1/v1/traces',headers:{Authorization:'Basic deliberately-invalid'}});
   trace.snapshot({thread:7,capturedAt:new Date().toISOString(),frame:{name:'main.work',line:10},stack:[{name:'main.work'}],scopes:[{name:'Locals',variables:[{name:'result',value:'evidence-'+i}]}]});
   await trace.close();ids.push(trace.programTraceID);expected.push('evidence-'+i);
   await sleep(12000);
  }
  // Let the production scheduler discover and compact the completed blocks.
  await sleep(65000);
  async function verify(){for(let i=0;i<ids.length;i++){
   let text;for(let attempt=0;attempt<20;attempt++){try{text=await runtime.traceJSON(ids[i]);if(text.includes(expected[i]))break;}catch{}await sleep(500);}
   assert.ok(text?.includes(expected[i]),'snapshot content retained');
   const spans=JSON.parse(text).batches.flatMap(b=>b.scopeSpans.flatMap(s=>s.spans));
   assert.equal(spans.length,3,'run, thread and snapshot preserved');
  }}
  await verify();await runtime.stop();
  const files=await fs.readdir(path.join(dir,'blocks','single-tenant'),{recursive:true});
  const metas=await Promise.all(files.filter(f=>f.endsWith('meta.json')).map(async f=>JSON.parse(await fs.readFile(path.join(dir,'blocks','single-tenant',f),'utf8'))));
  assert.ok(metas.some(meta=>meta.compactionLevel>0),'worker actually compacted blocks');
  runtime=new TraceRuntime(binary,dir,console.log);await runtime.start();await verify();
  await fs.mkdir('dist/embedded-tempo',{recursive:true});await fs.writeFile('dist/embedded-tempo/durability.json',JSON.stringify({ids,dataDir:dir,compacted:true,verifiedSnapshots:ids.length,restarted:true},null,2));
 }finally{await runtime.stop();}
});

test('flushed trace survives runtime interruption',{skip:!process.env.BROTE_EMBEDDED_INTEGRATION,timeout:45000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-recovery-'));let runtime=new TraceRuntime(path.resolve('bin/brote'),dir,()=>{});
 try{
  const config=await runtime.start();const trace=createSessionTrace('recovery','crash-program','go',config);
  trace.snapshot({thread:7,capturedAt:new Date().toISOString(),frame:{name:'captured.before.crash'},stack:[],scopes:[]});await trace.close();
  // The current Tempo API acknowledges memory ingestion, not durable WAL writes.
  // Wait through idle cutting and periodic flush before testing WAL recovery.
  await sleep(12000);
  const oldPID=runtime.child.pid;runtime.child.kill('SIGKILL');await runtime.exited;
  await Promise.all([runtime.start(),runtime.start()]);assert.notEqual(runtime.child.pid,oldPID,'same runtime object relaunches once');
  let found=false;for(let i=0;i<20;i++){try{found=(await runtime.traceJSON(trace.programTraceID)).includes('captured.before.crash');if(found)break;}catch{}await sleep(500);}
  assert.ok(found,'flushed snapshot recovered from WAL');
 }finally{await runtime.stop();}
});

test('runtime launch failure is bounded and can be stopped',{timeout:5000},async()=>{
 const runtime=new TraceRuntime('/does-not-exist/brote',os.tmpdir(),()=>{});
 await assert.rejects(runtime.start(),/could not start/);await runtime.stop();
});

test('immediate graceful shutdown saves accepted traces',{skip:!process.env.BROTE_EMBEDDED_INTEGRATION,timeout:45000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-graceful-'));let runtime=new TraceRuntime(path.resolve('bin/brote'),dir,()=>{});
 try{
  const config=await runtime.start();const trace=createSessionTrace('graceful','graceful-program','go',config);
  trace.snapshot({thread:7,capturedAt:new Date().toISOString(),frame:{name:'saved.on.close'},stack:[],scopes:[]});await trace.close();await runtime.stop();
  runtime=new TraceRuntime(path.resolve('bin/brote'),dir,()=>{});await runtime.start();
  let found=false;for(let i=0;i<20;i++){try{found=(await runtime.traceJSON(trace.programTraceID)).includes('saved.on.close');if(found)break;}catch{}await sleep(500);}
  assert.ok(found,'shutdown cuts remaining live traces to WAL');
 }finally{await runtime.stop();}
});

test('a transient storage lock failure can be retried on the same runtime',{skip:!process.env.BROTE_EMBEDDED_INTEGRATION,timeout:45000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-lock-retry-')),binary=path.resolve('bin/brote');
 const owner=new TraceRuntime(binary,dir,()=>{}),retry=new TraceRuntime(binary,dir,()=>{});
 try{
  await owner.start();await assert.rejects(retry.start(),/could not start/);await owner.stop();
  const [a,b]=await Promise.all([retry.start(),retry.start()]);assert.equal(a.url,'');assert.equal(b.url,'');assert.equal(typeof a.push,'function');
  const trace=createSessionTrace('retry','retry','go',a);await trace.close();
  assert.ok((await retry.traceJSON(trace.programTraceID)).includes('run retry'));
 }finally{await owner.stop();await retry.stop();await fs.rm(dir,{recursive:true,force:true});}
});

test('slow helper startup drains traces before normal shutdown',{skip:!process.env.BROTE_EMBEDDED_INTEGRATION,timeout:45000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-slow-start-')),data=path.join(dir,'data'),wrapper=path.join(dir,'slow-brote');
 const binary=path.resolve('bin/brote');
 await fs.writeFile(wrapper,'#!/bin/sh\nsleep 3\nexec '+"'"+binary.replaceAll("'","'\\''")+"'"+' "$@"\n',{mode:0o700});
 let runtime=new TraceRuntime(wrapper,data,()=>{});
 try{
  const outcomes=[],trace=createSessionTrace('slow','slow','go',runtime.start(),undefined,(...result)=>outcomes.push(result));
  trace.snapshot({thread:7,capturedAt:new Date().toISOString(),frame:{name:'saved.after.startup'},stack:[],scopes:[]});
  await trace.close();await runtime.stop();assert.ok(outcomes.every(([,ok])=>ok));
  runtime=new TraceRuntime(binary,data,()=>{});
  assert.ok((await runtime.traceJSON(trace.programTraceID)).includes('saved.after.startup'));
 }finally{await runtime.stop();await fs.rm(dir,{recursive:true,force:true});}
});

test('large valid traces remain complete through compaction and restart',{skip:!process.env.BROTE_EMBEDDED_INTEGRATION,timeout:180000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-large-query-')),binary=path.resolve('bin/brote');let runtime=new TraceRuntime(binary,dir,()=>{});
 try{
  const outcomes=[],trace=createSessionTrace('large','large','go',await runtime.start(),undefined,(...result)=>outcomes.push(result));
  for(let i=0;i<700;i++){
   trace.snapshot({thread:7,capturedAt:new Date().toISOString(),frame:{name:'large.snapshot'},stack:[],scopes:[{name:'Locals',variables:Array.from({length:14},(_,j)=>({name:'v'+j,value:'x'.repeat(1990),variablesReference:0}))}]});
   if(i%32===31)await trace.flush();
   if(i===349){await trace.flush();await sleep(12000);}
  }
  await trace.close();assert.ok(outcomes.every(([,ok])=>ok));
  async function verify(waitForFlush=false){
   let json,spans;
   for(let attempt=0;;attempt++){
    json=await runtime.traceJSON(trace.programTraceID);spans=JSON.parse(json).batches.flatMap(b=>b.scopeSpans.flatMap(s=>s.spans));
    if(spans.length===702||!waitForFlush||attempt>=50)break;await sleep(500);
   }
   assert.ok(Buffer.byteLength(json)>16*1024*1024);assert.equal(spans.length,702);
   const snapshots=spans.filter(s=>s.name==='large.snapshot');assert.equal(snapshots.length,700);
   for(const span of snapshots){const snapshot=JSON.parse(span.attributes.find(a=>a.key==='program.snapshot.json').value.stringValue);assert.equal(snapshot.scopes[0].variables[13].value,'x'.repeat(1990));}
  }
  // Backend-only queries intentionally wait for the last live fragment to flush.
  await verify(true);await sleep(85000);await verify();
  const files=await fs.readdir(path.join(dir,'blocks','single-tenant'),{recursive:true});
  const metas=await Promise.all(files.filter(f=>f.endsWith('meta.json')).map(async f=>JSON.parse(await fs.readFile(path.join(dir,'blocks','single-tenant',f),'utf8'))));
  assert.ok(metas.some(meta=>meta.compactionLevel>0),'worker merged blocks containing fragments of this large trace');
  await runtime.stop();
  runtime=new TraceRuntime(binary,dir,()=>{});await verify();
 }finally{await runtime.stop();if(!process.env.BROTE_KEEP_DATA)await fs.rm(dir,{recursive:true,force:true});}
});


test('embedded runtime binds only loopback listeners and Brote uses inherited pipes',{skip:!process.env.BROTE_EMBEDDED_INTEGRATION,timeout:30000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-private-')),runtime=new TraceRuntime(path.resolve('bin/brote'),dir,()=>{});
 try{
  const config=await runtime.start();assert.equal(config.url,'');assert.deepEqual(config.headers,{});assert.equal(typeof config.push,'function');
  if(process.platform==='linux'){
   const fdDir=`/proc/${runtime.child.pid}/fd`,owned=new Set();
   for(const file of await fs.readdir(fdDir)){
    let target;try{target=await fs.readlink(path.join(fdDir,file));}catch(error){if(error.code==='ENOENT')continue;throw error;}
    const inode=target.match(/^socket:\[(\d+)\]$/)?.[1];if(inode)owned.add(inode);
   }
   // Node can implement inherited stdio with unnamed AF_UNIX socket pairs.
   // Permit only loopback TCP and unnamed inherited Unix socket pairs.
   let listeners=0;
   for(const protocol of ['tcp','tcp6','udp','udp6']){
    const table=await fs.readFile(`/proc/${runtime.child.pid}/net/${protocol}`,'utf8');
    for(const row of table.trim().split('\n').slice(1)){
     const fields=row.trim().split(/\s+/);if(!owned.has(fields[9]))continue;
     assert.equal(protocol,'tcp','unexpected socket protocol');assert.ok(fields[1].startsWith('0100007F:'),'non-loopback socket');
     if(fields[3]==='0A')listeners++;
    }
   }
   assert.equal(listeners,2,'internal gRPC and required OTLP receiver only');
   const unix=await fs.readFile(`/proc/${runtime.child.pid}/net/unix`,'utf8');
   for(const row of unix.trim().split('\n').slice(1)){
    const fields=row.trim().split(/\s+/);if(owned.has(fields[6]))assert.ok(fields.length===7 && fields[3]!=='00010000','runtime exposed a Unix socket');
   }
  }else{
   const sockets=spawnSync('lsof',['-nP','-a','-p',String(runtime.child.pid),'-i'],{encoding:'utf8'});
   assert.ifError(sockets.error);assert.equal(sockets.status,0,sockets.stdout || sockets.stderr);
   const lines=sockets.stdout.trim().split('\n').slice(1);assert.equal(lines.filter(l=>l.includes('(LISTEN)')).length,2);
   for(const line of lines){assert.ok(line.includes(' TCP '),line);const addresses=line.split(' TCP ')[1].split(' ')[0].split('->');for(const address of addresses)assert.match(address,/^127\.0\.0\.1:\d+$/,line);}
  }
  const trace=createSessionTrace('private','private','go',config);trace.snapshot({thread:7,capturedAt:new Date().toISOString(),frame:{name:'in.process.push'},stack:[],scopes:[]});await trace.close();
  assert.ok((await runtime.traceJSON(trace.programTraceID)).includes('in.process.push'));
 }finally{await runtime.stop();await fs.rm(dir,{recursive:true,force:true});}
});

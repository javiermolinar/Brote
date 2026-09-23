// Reproducible local workload; runs only the bundled helper, with no remote export.
const {buildSync}=require('esbuild'),Module=require('node:module'),path=require('node:path');
const fs=require('node:fs/promises'),os=require('node:os'),{execFileSync}=require('node:child_process'),{randomBytes}=require('node:crypto');
function load(file){const code=buildSync({entryPoints:[file],bundle:true,platform:'node',format:'cjs',packages:'external',write:false}).outputFiles[0].text;const name=path.join(process.cwd(),'measure.cjs');const m=new Module(name);m.paths=Module._nodeModulePaths(process.cwd());m._compile(code,name);return m.exports;}
const {TraceRuntime}=load('packages/vscode/test/fixtures/traceRuntime.ts'),{createSessionTrace}=load('packages/vscode/test/fixtures/telemetry.ts');
(async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-workload-'));const runtime=new TraceRuntime(path.resolve('bin/brote'),dir,console.log);let timer;
 try{
  const config=await runtime.start();const rss=()=>Number(execFileSync('ps',['-o','rss=','-p',String(runtime.child.pid)],{encoding:'utf8'}).trim())*1024;
  const idle=rss();let peak=idle;timer=setInterval(()=>{try{peak=Math.max(peak,rss());}catch{}},200);
  const start=Date.now(),ids=[];
  for(let i=0;i<100;i++){
   const trace=createSessionTrace('measure-'+i,'snapshot-workload','go',config);
   for(let j=0;j<100;j++)trace.snapshot({thread:7,capturedAt:new Date().toISOString(),frame:{name:'main.capture'},stack:[],scopes:[{name:'Locals',variables:[{name:'payload',value:randomBytes(400).toString('hex')}]}]});
   await trace.close();ids.push(trace.programTraceID);
  }
  const ingestMs=Date.now()-start;await new Promise(r=>setTimeout(r,70000));const postRSS=rss();
  let verified=0;for(const id of [ids[0],ids.at(-1)]){const trace=JSON.parse(await runtime.traceJSON(id));const spans=trace.batches.flatMap(b=>b.scopeSpans.flatMap(s=>s.spans));if(spans.length!==102)throw Error('Incomplete workload trace: '+spans.length);verified++;}
  const stop=Date.now();await runtime.stop();const shutdownMs=Date.now()-stop;clearInterval(timer);
  let disk=0;for(const file of await fs.readdir(dir,{recursive:true})){const stat=await fs.stat(path.join(dir,file));if(stat.isFile())disk+=stat.size;}
  const result={traces:100,programSpans:10200,idleRSSBytes:idle,peakRSSBytes:peak,postMaintenanceRSSBytes:postRSS,diskBytes:disk,ingestMs,shutdownMs,verifiedTraces:verified,dataDir:dir};await fs.mkdir('dist/embedded-tempo',{recursive:true});await fs.writeFile('dist/embedded-tempo/measurements.json',JSON.stringify(result,null,2));console.log(JSON.stringify(result));
 }finally{clearInterval(timer);await runtime.stop();}
})().catch(error=>{console.error(error);process.exitCode=1});

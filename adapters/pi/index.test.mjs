import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';import {join} from 'node:path';import {pathToFileURL} from 'node:url';import {build} from 'esbuild';
const delay=ms=>new Promise(r=>setTimeout(r,ms));
async function harness(t,options={}){
 const dir=await mkdtemp(join(tmpdir(),'brote-pi-managed-')),cli=join(dir,'cli.cjs'),log=join(dir,'frames.jsonl'),mod=join(dir,'extension.mjs');
 await writeFile(cli,`#!${process.execPath}
const fs=require('node:fs'),readline=require('node:readline');const args=process.argv.slice(2),binding={id:'pi:conversation',name:'Pi',revision:1},file=${JSON.stringify(log)};
const save=x=>fs.appendFileSync(file,JSON.stringify(x)+'\\n'),out=x=>console.log(JSON.stringify(x));save({args});
if(args[0]==='sessions')out([{id:'0123456789',status:'paused',project:'/demo',panel:'http://127.0.0.1:1234/'}]);
else if(args[0]==='state')out({binding,capabilities:{coordination:${options.legacy?'0':'1'},taskStart:true}});
else if(args[0]==='events'){
 if(!args.includes('--managed'))throw Error('legacy event stream');
 out({type:'ready',consumer:{instance:'instance'}});out({type:'liveness',challenge:'challenge',instance:'instance'});
 ${options.delivery===false?'':`out({type:'delivery',delivery:{kind:'question',attempt:'attempt',subject:'q',message:'CANONICAL GO MESSAGE'}});`}
 readline.createInterface({input:process.stdin}).on('line',line=>{const input=JSON.parse(line);save(input);if(input.type==='liveness')out({type:'liveness',challenge:'challenge',instance:'instance'});if(input.type==='host'&&${!options.dropHostAck})out({type:'host-state',sequence:input.fact.sequence});}).on('close',()=>process.exit());
}else if(args[0]==='tracepoint')out({definitions:{items:[{id:'point',revision:2,owner:'pi:conversation'}]}});else if(args[0]==='captures')out({captures:[{status:'failed',exportStatus:'failed',programTraceId:'program',debuggerTraceId:'debugger',error:{message:'receiver unavailable'}}]});else if(args[0]==='capabilities')out({traceIds:{programTraceId:'program',debuggerTraceId:'debugger'}});else if(args[0]==='task-start')out({task:{id:'task'}});else if(args[0].startsWith('task-')||args[0]==='end-session')out({status:'ok'});else if(args[0]==='ui')out({panel:'http://127.0.0.1:1234/'});else throw Error(args[0]);
`,{mode:0o755});
 await build({entryPoints:['adapters/pi/index.ts'],bundle:true,platform:'node',format:'esm',outfile:mod,logLevel:'silent'});
 const handlers=new Map(),tools=new Map(),commands=new Map(),messages=[],notices=[];let idle=true;
 (await import(pathToFileURL(mod).href)).default({on:(n,f)=>handlers.set(n,f),registerTool:t=>tools.set(t.name,t),registerCommand:(n,c)=>commands.set(n,c),sendMessage:(m,o)=>messages.push({m,o})},()=>Promise.resolve(cli));
 const ctx={isIdle:()=>idle,sessionManager:{getSessionId:()=>options.fork?'fork':'conversation'},ui:{notify:m=>notices.push(m)}};
 t.after(async()=>{await handlers.get('session_shutdown')();await rm(dir,{recursive:true,force:true});});
 await handlers.get('session_start')({},ctx);
 const frames=async()=>{try{return(await readFile(log,'utf8')).trim().split('\n').map(JSON.parse);}catch{return[];}};
 const wait=async predicate=>{for(let i=0;i<100;i++){if(await predicate())return;await delay(20);}assert.fail('timeout waiting for managed transport');};
 return{handlers,tools,commands,messages,notices,frames,wait,setIdle:v=>{idle=v;}};
}
test('Pi relays canonical messages and send receipts without local discussion policy',async t=>{
 const h=await harness(t);await h.wait(async()=> (await h.frames()).some(f=>f.type==='receipt'));
 assert.equal(h.messages[0].m.content,'CANONICAL GO MESSAGE');assert.equal(h.messages[0].m.display,false);assert.equal(h.messages[0].o.deliverAs,'followUp');
 const frames=await h.frames();assert.ok(frames.some(f=>f.type==='receipt'&&f.status==='queued'));assert.ok(!frames.some(f=>f.args?.[0]==='comment'||f.args?.[0]==='event-status'));
});
test('Pi active-turn claim waits for proof and sends only lifecycle facts on settle',async t=>{
 const h=await harness(t,{delivery:false});h.setIdle(false);await h.handlers.get('agent_start')();
 await h.tools.get('debug_task').execute('claim',{session:'0123456789',task:'task',operation:'claim'});
 await h.tools.get('debug_execute').execute('next',{session:'0123456789',task:'task',operation:'next'});
 h.setIdle(true);await h.handlers.get('agent_settled')();
 await h.wait(async()=> (await h.frames()).some(f=>f.type==='host'&&f.fact.state==='idle'&&f.fact.turn));
 const frames=await h.frames(),claim=frames.find(f=>f.args?.[0]==='task-heartbeat');assert.ok(claim.args.includes('--consumer'));assert.ok(claim.args.includes('--instance'));assert.ok(claim.args.includes('--turn'));
 assert.ok(!frames.some(f=>f.args?.[0]==='task-complete'),'Go interprets settled state');await assert.rejects(h.tools.get('debug_task').execute('claim',{session:'0123456789',task:'task',operation:'claim'}),/active Pi/);
});
test('Pi fork does not inherit another conversation and older services fail explicitly',async t=>{
 const fork=await harness(t,{fork:true});assert.equal(fork.messages.length,0);assert.ok(!(await fork.frames()).some(f=>f.args?.[0]==='events'));
 const old=await harness(t,{legacy:true});assert.ok(old.notices.some(n=>n.includes('update and recover')));assert.equal(old.messages.length,0);
});
test('Pi lists sessions and stops only the named target through CLI',async t=>{
 const h=await harness(t,{delivery:false});const result=await h.tools.get('debug_sessions').execute('list',{});assert.equal(result.details.sessions.length,1);
 await assert.rejects(h.commands.get('debug-stop').handler(''),/Expected debugger session ID/);await h.commands.get('debug-stop').handler('0123456789');assert.deepEqual((await h.frames()).filter(f=>f.args?.[0]==='end-session').map(f=>f.args),[['end-session','0123456789','--confirmed']]);
});

test('Pi tracepoint tools preserve revisions/scope and capture export failures',async t=>{
 const h=await harness(t,{delivery:false});
 const result=await h.tools.get('debug_tracepoints').execute('create',{session:'0123456789',operation:'create',file:'/main.go',line:3,scope:'run',values:{total:'total'},captureLimit:2,enabled:false});assert.equal(result.details.definitions.items[0].revision,2);
 await h.tools.get('debug_tracepoints').execute('update',{session:'0123456789',operation:'update',id:'point',revision:2,name:'Updated'});
 await h.tools.get('debug_tracepoints').execute('delete',{session:'0123456789',operation:'delete',id:'point',revision:3});
 const captured=await h.tools.get('debug_captures').execute('read',{session:'0123456789'});assert.equal(captured.details.captures[0].exportStatus,'failed');assert.equal(captured.details.traceIds.programTraceId,'program');
 const frames=await h.frames(),defs=frames.filter(f=>f.args?.[0]==='tracepoint');assert.ok(defs[0].args.includes('--enabled=false'));assert.ok(defs[0].args.includes('run'));assert.ok(defs[1].args.includes('--revision'));assert.equal(defs[2].args[1],'remove');assert.ok(!frames.some(f=>f.args?.[0]==='task-execute'));
});

test('settle and shutdown reject unanswered liveness without recursive callbacks',async t=>{const h=await harness(t,{delivery:false,dropHostAck:true});h.setIdle(false);await h.handlers.get('agent_start')();const claim=h.tools.get('debug_task').execute('claim',{session:'0123456789',task:'task',operation:'claim'});const rejected=assert.rejects(claim,/Agent turn ended/);await h.wait(async()=> (await h.frames()).some(f=>f.type==='host'&&f.fact.state==='active'));h.setIdle(true);await h.handlers.get('agent_settled')();await rejected;await h.handlers.get('session_shutdown')();});

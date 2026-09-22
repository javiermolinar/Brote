import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,readFile,rm,realpath} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {build} from 'esbuild';

const delay = ms => new Promise(resolve=>setTimeout(resolve,ms));
test('Pi handback uses the matching conversation, deduplicates, and does not inherit on fork',async t=>{
 const dir=await mkdtemp(join(tmpdir(),'handover-pi-'));t.after(()=>rm(dir,{recursive:true,force:true}));
 const cli=join(dir,'cli.cjs'),store=join(dir,'state.json'),module=join(dir,'extension.mjs');
 const original=process.env.DELVE_LLM_ADAPTER_BIN;process.env.DELVE_LLM_ADAPTER_BIN=cli;
 t.after(()=>{if(original===undefined)delete process.env.DELVE_LLM_ADAPTER_BIN;else process.env.DELVE_LLM_ADAPTER_BIN=original;});
 const binding={id:'pi:conversation',revision:1,name:'Pi'};
 await writeFile(store,JSON.stringify({id:'0123456789',owner:'agent',binding,cursor:1,notification:{id:'1',status:'pending'}}));
 await writeFile(cli,`#!${process.execPath}
const fs=require('node:fs');const file=${JSON.stringify(store)};const [command,...args]=process.argv.slice(2);let state=JSON.parse(fs.readFileSync(file));
if(command==='version')console.log(JSON.stringify({protocol:2,capabilities:['executionTasks','taskDelivery','taskExecute','embeddedWebUI']}));
else if(command==='sessions') console.log(JSON.stringify([{id:state.id,status:'paused'}]));
else if(command==='state')console.log(JSON.stringify(state));
else if(command==='event-status'){state.notification.status=args[args.indexOf('--status')+1];fs.writeFileSync(file,JSON.stringify(state));console.log('{}');}
else if(command==='events'){const event={id:1,kind:'control_returned',binding:state.binding};console.log(JSON.stringify(event));console.log(JSON.stringify(event));setInterval(()=>{},1000);}
else throw new Error(command);
`,{mode:0o755});
 await build({entryPoints:['adapters/pi/index.ts'],bundle:true,platform:'node',format:'esm',outfile:module,logLevel:'silent'});
 const extension=(await import(pathToFileURL(module).href)).default;
 const handlers=new Map(),messages=[],tools=new Map();
 extension({on:(name,fn)=>handlers.set(name,fn),registerCommand:()=>{},registerTool:tool=>tools.set(tool.name,tool),sendMessage:(message,options)=>messages.push({message,options})});
 t.after(()=>handlers.get('session_shutdown')());
 const context=id=>({sessionManager:{getSessionId:()=>id},ui:{notify:()=>{}}});
 await handlers.get('session_start')({reason:'startup'},context('conversation'));
 for(let i=0;i<100&&JSON.parse(await readFile(store)).notification.status!=='queued';i++)await delay(20);
 assert.equal(messages.length,1);
 assert.equal(messages[0].message.display,false,'transport details stay out of the transcript');
 assert.equal(messages[0].options.triggerTurn,true);
 assert.equal(messages[0].options.deliverAs,'followUp');
 assert.match(messages[0].message.content,/Handback alone permits inspection; execution requires a current task for a user-requested debugging investigation/);
 assert.equal(JSON.parse(await readFile(store)).notification.status,'queued');
 assert.ok(tools.has('debug_connect'));
 await handlers.get('session_shutdown')();
 const state=JSON.parse(await readFile(store));state.notification.status='pending';await writeFile(store,JSON.stringify(state));
 await handlers.get('session_start')({reason:'fork'},context('forked-conversation'));
 await delay(100);assert.equal(messages.length,1,'fork must not receive original session handback');
 await handlers.get('session_shutdown')();
 state.notification.status='sending';await writeFile(store,JSON.stringify(state));
 await handlers.get('session_start')({reason:'resume'},context('conversation'));
 for(let i=0;i<100&&JSON.parse(await readFile(store)).notification.status!=='unknown';i++)await delay(20);
 assert.equal(JSON.parse(await readFile(store)).notification.status,'unknown');
 assert.equal(messages.length,1,'ambiguous delivery must not resend');
});

test('Pi delivers persisted questions while browser owns execution and reconciles duplicates',async t=>{
 const dir=await mkdtemp(join(tmpdir(),'comment-pi-'));t.after(()=>rm(dir,{recursive:true,force:true}));
 const cli=join(dir,'cli.cjs'),store=join(dir,'state.json'),module=join(dir,'extension.mjs');
 const original=process.env.DELVE_LLM_ADAPTER_BIN;process.env.DELVE_LLM_ADAPTER_BIN=cli;
 t.after(()=>{if(original===undefined)delete process.env.DELVE_LLM_ADAPTER_BIN;else process.env.DELVE_LLM_ADAPTER_BIN=original;});
 const binding={id:'pi:conversation',revision:1,name:'Pi'};
 await writeFile(store,JSON.stringify({id:'0123456789',owner:'browser',binding,cursor:1,capabilities:{comments:true},threads:[{id:'thread',resolved:false,delivery:{binding,question:'question',status:'pending'},messages:[{body:'Why is total 21?'}]}]}));
 await writeFile(cli,`#!${process.execPath}
const fs=require('node:fs');const file=${JSON.stringify(store)};const [command,...args]=process.argv.slice(2);let state=JSON.parse(fs.readFileSync(file));
if(command==='version')console.log(JSON.stringify({protocol:2,capabilities:['executionTasks','taskDelivery','taskExecute','embeddedWebUI']}));
else if(command==='sessions') console.log(JSON.stringify([{id:state.id,status:'paused'}]));
else if(command==='state')console.log(JSON.stringify(state));
else if(command==='comment'&&args[0]==='list')console.log(JSON.stringify({threads:state.threads}));
else if(command==='comment'&&args[0]==='delivery'){state.threads[0].delivery.status=args[args.indexOf('--status')+1];fs.writeFileSync(file,JSON.stringify(state));console.log('{}');}
else if(command==='events'){const event={id:1,kind:'question.created',binding:state.binding};console.log(JSON.stringify(event));console.log(JSON.stringify(event));setInterval(()=>{},1000);}
else throw new Error(command);
`,{mode:0o755});
 await build({entryPoints:['adapters/pi/index.ts'],bundle:true,platform:'node',format:'esm',outfile:module,logLevel:'silent'});
 const extension=(await import(pathToFileURL(module).href)).default,handlers=new Map(),messages=[];
 extension({on:(name,fn)=>handlers.set(name,fn),registerCommand:()=>{},registerTool:()=>{},sendMessage:(message)=>messages.push(message)});
 t.after(()=>handlers.get('session_shutdown')());
 const context=id=>({sessionManager:{getSessionId:()=>id},ui:{notify:()=>{}}});
 await handlers.get('session_start')({},context('conversation'));await delay(100);
 assert.equal(messages.length,1);assert.match(messages[0].content,/comment reply/);
 assert.equal(JSON.parse(await readFile(store)).owner,'browser');
 await handlers.get('session_shutdown')();await handlers.get('session_start')({},context('conversation'));await delay(100);
 assert.equal(messages.length,1,'queued question is not delivered twice on restart');
 await handlers.get('session_shutdown')();
 const state=JSON.parse(await readFile(store));state.threads[0].delivery.status='sending';await writeFile(store,JSON.stringify(state));
 await handlers.get('session_start')({},context('conversation'));await delay(100);
 assert.equal(JSON.parse(await readFile(store)).threads[0].delivery.status,'unknown');assert.equal(messages.length,1);
});

test('Pi lists current sessions and stops only the explicitly named session',async t=>{
 const dir=await mkdtemp(join(tmpdir(),'sessions-pi-'));t.after(()=>rm(dir,{recursive:true,force:true}));
 const cli=join(dir,'cli.cjs'),log=join(dir,'calls.jsonl'),module=join(dir,'extension.mjs');
 const original=process.env.DELVE_LLM_ADAPTER_BIN;process.env.DELVE_LLM_ADAPTER_BIN=cli;
 t.after(()=>{if(original===undefined)delete process.env.DELVE_LLM_ADAPTER_BIN;else process.env.DELVE_LLM_ADAPTER_BIN=original;});
 await writeFile(cli,`#!${process.execPath}
const fs=require('node:fs');const args=process.argv.slice(2);fs.appendFileSync(${JSON.stringify(log)},JSON.stringify(args)+'\\n');
if(args[0]==='version')console.log(JSON.stringify({protocol:2,capabilities:['executionTasks','taskDelivery','taskExecute','embeddedWebUI']}));
else if(args[0]==='sessions') console.log(JSON.stringify([{id:'0123456789',status:'paused',project:'/demo',panel:'http://127.0.0.1:1234/'},{id:'aaaaaaaaaa',status:'ended'}]));
else if(args[0]==='state') console.log('{}');
else if(args[0]==='end-session') console.log('{"status":"ended"}');
else throw new Error(args[0]);
`,{mode:0o755});
 await build({entryPoints:['adapters/pi/index.ts'],bundle:true,platform:'node',format:'esm',outfile:module,logLevel:'silent'});
 const extension=(await import(pathToFileURL(module).href)).default,handlers=new Map(),commands=new Map(),tools=new Map(),notifications=[];
 extension({on:(name,fn)=>handlers.set(name,fn),registerCommand:(name,command)=>commands.set(name,command),registerTool:tool=>tools.set(tool.name,tool)});
 t.after(()=>handlers.get('session_shutdown')());
 await handlers.get('session_start')({}, {sessionManager:{getSessionId:()=> 'conversation'},ui:{notify:text=>notifications.push(text)}});
 await commands.get('debug-sessions').handler('');
 assert.match(notifications.at(-1),/0123456789.*paused/);
 assert.match(notifications.at(-1),/http:\/\/127.0.0.1:1234\//);
 assert.doesNotMatch(notifications.at(-1),/aaaaaaaaaa/);
 const listed=await tools.get('debug_sessions').execute('call',{});
 assert.equal(listed.details.sessions.length,1);
 assert.equal(listed.details.cli,await realpath(cli));
 assert.match(listed.content[0].text,/Brote CLI:/);
 await assert.rejects(commands.get('debug-stop').handler(''),/Expected debugger session ID/);
 await commands.get('debug-stop').handler('0123456789');
 const calls=(await readFile(log,'utf8')).trim().split('\n').map(JSON.parse);
 assert.deepEqual(calls.filter(args=>args[0]==='end-session'),[['end-session','0123456789','--confirmed']]);
 assert.ok(tools.has('debug_stop'));
});

test('Pi task delivery reconciles, claims, executes and settles only its bound grant',async t=>{
 const dir=await mkdtemp(join(tmpdir(),'tasks-pi-'));t.after(()=>rm(dir,{recursive:true,force:true}));
 const cli=join(dir,'cli.cjs'),store=join(dir,'state.json'),module=join(dir,'extension.mjs');
 const original=process.env.DELVE_LLM_ADAPTER_BIN;process.env.DELVE_LLM_ADAPTER_BIN=cli;
 t.after(()=>{if(original===undefined)delete process.env.DELVE_LLM_ADAPTER_BIN;else process.env.DELVE_LLM_ADAPTER_BIN=original;});
 const binding={id:'pi:conversation',revision:1,name:'Pi'};
 await writeFile(store,JSON.stringify({id:'0123456789',status:'paused',binding,cursor:1,capabilities:{taskStart:true},task:{id:'task',binding,status:'authorized',delivery:'pending',instruction:'Inspect retry'},calls:[]}));
 await writeFile(cli,`#!${process.execPath}
const fs=require('node:fs'),file=${JSON.stringify(store)};const [command,...args]=process.argv.slice(2);let state=JSON.parse(fs.readFileSync(file));
if(command==='version')console.log(JSON.stringify({protocol:2,capabilities:['executionTasks','taskDelivery','taskExecute','embeddedWebUI']}));
else if(command==='sessions')console.log(JSON.stringify([{id:state.id,status:'paused'}]));
else if(command==='state')console.log(JSON.stringify(state));
else if(command==='events'){console.log(JSON.stringify({id:2,kind:'task.authorized',binding:state.binding}));setInterval(()=>{},1000);}
else if(command.startsWith('task-')){state.calls.push({command,args});if(command==='task-start')state.task={id:'chat-task',binding:state.binding,status:'authorized',delivery:'acknowledged',instruction:args[args.indexOf('--instruction')+1]};if(command==='task-delivery')state.task.delivery=args[args.indexOf('--status')+1];if(command==='task-heartbeat')state.task.delivery='acknowledged';if(command==='task-complete')state.task.status='completed';if(command==='task-cancel')state.task.status='cancelled';fs.writeFileSync(file,JSON.stringify(state));console.log(JSON.stringify(state));}
else throw new Error(command);
`,{mode:0o755});
 await build({entryPoints:['adapters/pi/index.ts'],bundle:true,platform:'node',format:'esm',outfile:module,logLevel:'silent'});
 const handlers=new Map(),tools=new Map(),messages=[];
 (await import(pathToFileURL(module).href)).default({on:(n,f)=>handlers.set(n,f),registerCommand:()=>{},registerTool:t=>tools.set(t.name,t),sendMessage:m=>messages.push(m)});
 t.after(()=>handlers.get('session_shutdown')());
 let idle=false;const context={isIdle:()=>idle,sessionManager:{getSessionId:()=> 'conversation'},ui:{notify:()=>{}}};
 await handlers.get('session_start')({},context);await delay(150);
 assert.equal(messages.length,1);assert.match(messages[0].content,/debug_task/);
 assert.equal(JSON.parse(await readFile(store)).task.delivery,'queued');
 await tools.get('debug_task').execute('claim',{session:'0123456789',task:'task',operation:'claim'});
 await tools.get('debug_execute').execute('exec',{session:'0123456789',task:'task',operation:'next'});
 await handlers.get('agent_settled')();
 let state=JSON.parse(await readFile(store));assert.equal(state.task.status,'completed');
 assert.equal(state.calls.filter(c=>c.command==='task-execute').length,1);
 assert.ok(state.calls.every(c=>c.args.includes('pi:conversation')&&c.args.includes('task')));
 await handlers.get('session_shutdown')();await handlers.get('session_start')({},context);await delay(100);
 assert.equal(messages.length,1,'completed task never redelivered');
 state=JSON.parse(await readFile(store));state.task.status='authorized';state.task.delivery='sending';await writeFile(store,JSON.stringify(state));
 await handlers.get('session_shutdown')();await handlers.get('session_start')({},context);await delay(100);
 assert.equal(JSON.parse(await readFile(store)).task.delivery,'unknown');assert.equal(messages.length,1);
 await tools.get('debug_task').execute('claim',{session:'0123456789',task:'task',operation:'claim'});
 await handlers.get('session_shutdown')();assert.equal(JSON.parse(await readFile(store)).task.status,'cancelled','closing Pi relinquishes its grant');
 state=JSON.parse(await readFile(store));state.task.status='authorized';state.task.delivery='acknowledged';await writeFile(store,JSON.stringify(state));
 await handlers.get('session_start')({},context);
 let leaseTick;const originalInterval=globalThis.setInterval;
 try {
   globalThis.setInterval=(fn)=>{leaseTick=fn;return originalInterval(()=>{},60000);};
   await tools.get('debug_task').execute('claim',{session:'0123456789',task:'task',operation:'claim'});
 } finally {globalThis.setInterval=originalInterval;}
 idle=true;await leaseTick();
 assert.equal(JSON.parse(await readFile(store)).task.status,'completed','an idle harness cannot renew a grant indefinitely even without a settled notification');

 idle=false;
 await assert.rejects(tools.get('debug_task').execute('start',{session:'0123456789',operation:'start'}),/instruction/);
 await assert.rejects(tools.get('debug_task').execute('claim',{session:'0123456789',operation:'claim'}),/task ID/);
 const started=await tools.get('debug_task').execute('start',{session:'0123456789',operation:'start',instruction:'Debug the retry, inspect total, then leave it paused'});
 assert.equal(started.details.task.id,'chat-task');
 state=JSON.parse(await readFile(store));
 assert.equal(state.task.delivery,'acknowledged');
 const created=state.calls.find(c=>c.command==='task-start');
 assert.equal(created.args[created.args.indexOf('--revision')+1],'1');
 assert.ok(!created.args.includes('--human'));
 assert.equal(messages.length,1,'chat-origin request does not enqueue another turn');
 await tools.get('debug_execute').execute('exec',{session:'0123456789',task:'chat-task',operation:'next'});
 await tools.get('debug_task').execute('complete',{session:'0123456789',task:'chat-task',operation:'complete'});
 assert.equal(JSON.parse(await readFile(store)).task.status,'completed');
 await assert.rejects(tools.get('debug_execute').execute('exec',{session:'0123456789',task:'chat-task',operation:'next'}),/changed/);

});

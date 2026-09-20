import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,readFile,rm} from 'node:fs/promises';
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
if(command==='sessions') console.log(JSON.stringify([{id:state.id,status:'paused'}]));
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
 assert.match(messages[0].message.content,/Do not resume/);
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
if(command==='sessions') console.log(JSON.stringify([{id:state.id,status:'paused'}]));
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

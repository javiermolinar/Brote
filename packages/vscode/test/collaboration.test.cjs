const {test}=require('node:test');
const assert=require('node:assert/strict');
const {build}=require('esbuild');
const vm=require('node:vm');
const http=require('node:http');

async function harness(t, hooks={}){
 const calls=[],tools=new Map(),commands=new Map(),native=[],messages=[];
 let participant;
 const state={id:'0123456789',version:2,generation:1,status:'paused',capabilities:{taskStart:true},binding:{id:'vscode:test',name:'VS Code Chat',revision:1},frames:[{file:'/tmp/main.go',line:17,Locals:[{name:'total',value:'21',type:'int'}]}],goroutine:1,frame:0};
 const thread={id:'thread',file:'/tmp/main.go',line:17,context:{generation:1,frames:state.frames},messages:[{id:'question',author:'human',body:'Why 21?'}],delivery:{question:'question',status:'pending',binding:state.binding}};
 const server=http.createServer(async(req,res)=>{
  if(req.url.startsWith('/api/events')){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(': connected\n\n');return;}
  let body='';for await(const data of req)body+=data;
  const input=body?JSON.parse(body):undefined;calls.push({url:req.url,input});
  let result=state;
  if(req.url==='/api/comments'){
   if(!input)result={discussion:{threads:[thread]}};
   else if(input.action==='delivery'){thread.delivery.status=input.status;result={thread};}
   else if(input.action==='reply'){thread.messages.push({id:input.messageId,author:'VS Code Chat',body:input.body});thread.delivery.status='answered';result={thread};}
   else if(input.action==='resolve'){thread.resolved=true;result={thread};}
  }
  if(req.url==='/api/action'){
   if(input.action==='bind'){state.binding={id:input.binding,revision:2};}
   else if(input.action==='task-start'){state.task={id:'task',status:'authorized',delivery:'acknowledged',binding:state.binding,instruction:input.instruction};result={task:state.task};}
   else if(input.action==='next'){state.task.status='active';state.frames[0].line++;}
   else if(input.action==='task-complete'){state.task.status='completed';}
   else if(input.action==='task-cancel'){state.task.status='cancelled';}
   state.generation++;
  }
  await hooks.response?.(req.url,input,state);
  res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(result));
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const s={id:state.id,version:2,project:'/tmp',http:`http://127.0.0.1:${server.address().port}`};
 const subscriptions=[];
 t.after(()=>{for(const d of subscriptions)d.dispose();server.closeAllConnections();server.close();});
 const disposable=()=>({dispose(){}});
 class Text{constructor(value){this.value=value;}}
 const vscode={
  debug:{registerDebugAdapterTrackerFactory:()=>disposable()},
  comments:{createCommentController:()=>({...disposable(),createCommentThread:(uri,range,comments)=>{const thread={uri,range,comments,...disposable()};native.push(thread);return thread;}})},
  CommentMode:{Preview:0},CommentThreadCollapsibleState:{Collapsed:0},
  MarkdownString:Text,ThemeIcon:Text,Range:class{},Uri:{file:p=>p},
  LanguageModelTextPart:Text,LanguageModelToolResult:class{constructor(content){this.content=content;}},
  LanguageModelChatMessage:{User:content=>({content}),Assistant:content=>({content})},
  workspace:{isTrusted:true,getConfiguration:()=>({get:()=>undefined})},
  window:{showErrorMessage:m=>messages.push(m)},
  lm:{registerTool:(name,tool)=>{tools.set(name,tool);return disposable();}},
  chat:{createChatParticipant:(_id,handler)=>{participant=handler;return disposable();}},
  commands:{registerCommand:(name,fn)=>{commands.set(name,fn);return disposable();}},
 };
 const built=await build({entryPoints:['packages/vscode/src/collaboration.ts'],bundle:true,platform:'node',format:'cjs',external:['vscode'],write:false});
 const module={exports:{}};
 vm.runInNewContext(built.outputFiles[0].text,{module,exports:module.exports,require:name=>name==='vscode'?vscode:require(name),setInterval,clearInterval,setTimeout,URL,fetch,AbortSignal,AbortController,TextDecoder,Buffer});
 module.exports.registerCollaboration({extensionPath:'/extension',subscriptions,workspaceState:{get:(key,fallback)=>key==='collaborationID'?'test':fallback,update:async()=>{}}},{sessions:async()=>[s],selected:async()=>s,attach:async()=>{},scope:()=>({goroutine:7,frame:3}),log:{appendLine:m=>messages.push(m)}});
 for(let i=0;i<100&&!native.length;i++)await new Promise(r=>setTimeout(r,10));
 return {tools,participant,calls,state,thread,native,vscode,messages,commands};
}

test('native questions stream an answer and persist it without execution',async t=>{
 const h=await harness(t),chunks=[];
 assert.equal(h.native[0].comments[0].body.value,'Why 21?');
 await h.participant({command:'answer',prompt:'0123456789 thread',model:{sendRequest:async messages=>{assert.match(messages[0].content,/historical/);return {text:(async function*(){yield 'The captured total is 21.';})()};}}},{history:[]},{markdown:s=>chunks.push(s)},{isCancellationRequested:false});
 assert.equal(h.thread.delivery.status,'answered');
 assert.equal(h.thread.messages.at(-1).body,'The captured total is 21.');
 assert.equal(h.calls.filter(c=>c.url==='/api/action').length,0);
 assert.deepEqual(h.calls.filter(c=>c.input?.action==='delivery').map(c=>c.input.status),['sending','thinking']);
 assert.equal(h.native[0].comments.length,2);
});

test('obsolete binding refuses an inline answer',async t=>{
 const h=await harness(t);h.state.binding={id:'pi:other',revision:2};
 let called=false;const chunks=[];
 await h.participant({command:'answer',prompt:'0123456789 thread',model:{sendRequest:async()=>{called=true;}}},{history:[]},{markdown:s=>chunks.push(s)},{isCancellationRequested:false});
 assert.equal(called,false);assert.match(chunks.join(''),/another agent/);
 assert.equal(h.thread.messages.length,1);
});

test('requested investigation starts without confirmation and keeps one scope across steps',async t=>{
 const h=await harness(t);
 const tool=h.tools.get('agentdebugger_debug'),token={isCancellationRequested:false};
 const request={operation:'task-start',session:'0123456789',instruction:'Debug why total reaches 21, then leave it paused'};
 assert.equal((await tool.prepareInvocation({input:request})).confirmationMessages,undefined);
 await tool.invoke({input:request},token);
 for(let i=0;i<2;i++)await tool.invoke({input:{operation:'next',session:'0123456789',task:'task'}},token);
 assert.equal(h.state.task.status,'active','a pause does not finish an investigation');
 await tool.invoke({input:{operation:'task-complete',session:'0123456789',task:'task'}},token);
 const actions=h.calls.filter(c=>c.url==='/api/action').map(c=>c.input);
 assert.deepEqual(actions.map(a=>a.action),['task-start','task-heartbeat','next','task-heartbeat','next','task-complete']);
 assert.ok(actions.every(a=>a.actor==='agent'));assert.equal(actions[0].revision,1);
 assert.equal(actions[0].instruction,request.instruction);assert.equal(actions[2].task,'task');
 assert.equal(h.state.task.status,'completed');
});

test('attach and inspection never create execution scope, and execution requires a task',async t=>{
 const h=await harness(t),tool=h.tools.get('agentdebugger_debug'),token={isCancellationRequested:false};
 await tool.invoke({input:{operation:'attach'}},token);
 await tool.invoke({input:{operation:'inspect'}},token);
 await assert.rejects(tool.invoke({input:{operation:'next'}},token),/task-start/);
 await assert.rejects(tool.invoke({input:{operation:'task-start'}},token),/instruction/);
 assert.equal(h.calls.filter(c=>c.url==='/api/action').length,0);
});

function cancellation(){
 const listeners=new Set();
 return {isCancellationRequested:false,onCancellationRequested(fn){listeners.add(fn);return {dispose(){listeners.delete(fn);}};},cancel(){this.isCancellationRequested=true;for(const fn of listeners)fn();}};
}
test('cancellation while recording a request cancels its task without execution',async t=>{
 const token=cancellation();
 const h=await harness(t,{response:async(_url,input)=>{if(input?.action==='task-start')token.cancel();}});
 await assert.rejects(h.tools.get('agentdebugger_debug').invoke({input:{operation:'task-start',instruction:'Debug the retry'}},token),/cancel/i);
 assert.deepEqual(h.calls.filter(c=>c.url==='/api/action').map(c=>c.input.action),['task-start','task-cancel']);
 assert.equal(h.state.task.status,'cancelled');
});

test('execution rejects another conversation binding',async t=>{
 const h=await harness(t);h.state.binding={id:'pi:other',revision:2};
 await assert.rejects(h.tools.get('agentdebugger_debug').invoke({input:{operation:'task-start',instruction:'Debug the retry'}},{isCancellationRequested:false}),/another agent|another conversation/i);
 assert.equal(h.calls.filter(c=>c.url==='/api/action').length,0);
});
test('a requested investigation binds an unbound session to VS Code',async t=>{
 const h=await harness(t);delete h.state.binding;
 await h.tools.get('agentdebugger_debug').invoke({input:{operation:'task-start',instruction:'Debug the retry'}},{isCancellationRequested:false});
 const actions=h.calls.filter(c=>c.url==='/api/action').map(c=>c.input);
 assert.equal(actions[0].action,'bind');assert.ok(actions.every(a=>a.binding==='vscode:test'));
});
test('binding revision change during heartbeat prevents execution and foreign cleanup',async t=>{
 const h=await harness(t,{response:async(_url,input,state)=>{if(input?.action==='task-heartbeat')state.binding={id:'vscode:test',revision:3};}});
 h.state.task={id:'task',status:'authorized'};
 await assert.rejects(h.tools.get('agentdebugger_debug').invoke({input:{operation:'next',task:'task'}},{isCancellationRequested:false}),/binding|another/i);
 assert.deepEqual(h.calls.filter(c=>c.url==='/api/action').map(c=>c.input.action),['task-heartbeat']);
});

test('human pause ends the task without recreating it',async t=>{
 const h=await harness(t,{response:async(_url,input,state)=>{if(input?.action==='next')state.task.status='cancelled';}});
 h.state.task={id:'task',status:'active'};
 await assert.rejects(h.tools.get('agentdebugger_debug').invoke({input:{operation:'next',task:'task'}},{isCancellationRequested:false}),/interrupted/i);
 assert.deepEqual(h.calls.filter(c=>c.url==='/api/action').map(c=>c.input.action),['task-heartbeat','next']);
});

test('target exit completes the task and returns a successful stopped snapshot',async t=>{
 const h=await harness(t,{response:async(_url,input,state)=>{if(input?.action==='next'){state.task.status='completed';state.status='exited';}}});
 h.state.task={id:'task',status:'active'};
 const result=await h.tools.get('agentdebugger_debug').invoke({input:{operation:'next',task:'task'}},{isCancellationRequested:false});
 assert.equal(JSON.parse(result.content[0].value).status,'exited');
 assert.deepEqual(h.calls.filter(c=>c.url==='/api/action').map(c=>c.input.action),['task-heartbeat','next']);
});

test('evaluate uses selected frame with explicit scope overrides',async t=>{
 const h=await harness(t),tool=h.tools.get('agentdebugger_debug');
 await tool.invoke({input:{operation:'evaluate',expression:'total'}},{isCancellationRequested:false});
 await tool.invoke({input:{operation:'evaluate',expression:'total',frame:0}},{isCancellationRequested:false});
 const inputs=h.calls.filter(c=>c.input?.action==='eval').map(c=>c.input);
 assert.deepEqual(inputs.map(x=>[x.goroutine,x.frame]),[[7,3],[7,0]]);
 assert.ok(inputs.every(x=>typeof x.generation==='number'));
});

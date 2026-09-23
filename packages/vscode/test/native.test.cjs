const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm');
const {buildSync}=require('esbuild');
const code=buildSync({entryPoints:['packages/vscode/src/native.ts'],bundle:true,platform:'node',format:'cjs',external:['vscode'],write:false}).outputFiles[0].text;
async function harness(t){
 const calls=[],views=[],commands=new Map(),subscriptions=[],writes=[],prompts=[];let factory,document={threads:[]},serial=0;
 class Frame{constructor(session){this.session=session;this.threadId=7;this.frameId=42;}}
 const session={id:'editor',type:'brote',name:'Go',configuration:{sessionId:'0123456789'}};
 const model={id:'model',name:'Model',vendor:'test',sendRequest:async messages=>{prompts.push(messages[0].content);return{text:(async function*(){yield 'Answer';})()};}};
 const api={workspace:{isTrusted:true,workspaceFolders:[{uri:{toString:()=>'/workspace'}}]},debug:{activeDebugSession:session,activeStackItem:new Frame(session),registerDebugAdapterTrackerFactory:(_,f)=>{factory=f;return{dispose(){}};}},DebugStackFrame:Frame,lm:{selectChatModels:async()=>[model]},CancellationTokenSource:class{listeners=new Set();token={isCancellationRequested:false,onCancellationRequested:fn=>{this.listeners.add(fn);return{dispose:()=>this.listeners.delete(fn)};}};cancel(){this.token.isCancellationRequested=true;for(const fn of this.listeners)fn();}dispose(){this.listeners.clear();}},LanguageModelChatMessage:{User:content=>({content})},comments:{createCommentController:()=>({dispose(){},createCommentThread:()=>{const v={dispose(){this.disposed=true;}};views.push(v);return v;}})},Uri:{file:p=>p},Range:class{},MarkdownString:class{constructor(value){this.value=value;}},CommentMode:{Preview:0},CommentThreadCollapsibleState:{Expanded:1},window:{activeTextEditor:{document:{uri:{scheme:'file',fsPath:'/main.go'}},selection:{active:{line:2}}},showQuickPick:async items=>items.find(x=>x.model)||items[0],showErrorMessage:e=>calls.push({error:e})},commands:{registerCommand:(id,fn)=>{commands.set(id,fn);return{dispose(){}};},executeCommand:async()=>{}}};
 const client={async run(args){return operation(args);},async withBody(args,body){return operation(args,body);}};
 function operation(args,body){
  calls.push({args:[...args],body});const flag=k=>{const i=args.indexOf('--'+k);return i<0?undefined:args[i+1];};
  if(args[0]==='state')return {binding:{id:'pi',name:'Pi',revision:1}};
  const action=args[1];if(action==='index')return{discussions:document.threads.map(x=>({session:session.configuration.sessionId,thread:x.id}))};if(action==='list')return structuredClone(document);
  const r={kind:flag('recipient-kind'),id:flag('recipient-id'),revision:Number(flag('recipient-revision')),name:flag('recipient-name')};let record=document.threads.find(x=>x.id===args[3]);
  if(action==='create'){record={id:'t'+(++serial),file:flag('file'),line:Number(flag('line')),resolved:false,context:{run:'run',capturedAt:'saved',goroutine:7,frames:[{file:'/main.go',line:3}],frame:0},messages:[],delivery:{}};document.threads.push(record);}
  if(['create','ask'].includes(action)){record.messages.push({id:'q'+(++serial),author:'human',body,context:record.context});record.delivery={question:record.messages.at(-1).id,status:'pending',recipient:r};}
  if(action==='claim'){assert.equal(record.delivery.status,'pending');record.delivery.status='sending';record.delivery.attempt={id:'attempt'+(++serial)};}
  if(action==='delivery')record.delivery.status=flag('status');
  if(action==='reply'){assert.equal(flag('attempt'),record.delivery.attempt.id);record.messages.push({id:flag('message-id'),author:'Model',body,question:flag('question')});record.delivery.status='answered';}
  if(action==='answer-failed'){record.delivery.status='failed';record.delivery.error=flag('error');}
  if(action==='retry'){record.delivery.status='pending';record.delivery.recipient=r;delete record.delivery.attempt;}
  if(action==='resolve')record.resolved=true;
  return{thread:structuredClone(record)};
 }
 const module={exports:{}};vm.runInNewContext(code,{module,exports:module.exports,require:n=>n==='vscode'?api:require(n),Buffer,setInterval,clearInterval});
 const capture=async(s,thread,index)=>{calls.push({capture:{thread,index}});return{session:s.id,serviceSession:s.configuration.sessionId,name:s.name,type:'brote',run:'run',generation:3,frameIndex:index,capturedAt:'preview',thread,frame:{source:{path:'/main.go'},line:3},stack:[],scopes:[]};};
 const native=module.exports.nativeDiscussions({extensionPath:'/extension',subscriptions,workspaceState:{get:()=>undefined,update:async(k)=>writes.push(k)}},capture,client,Promise.resolve({discussions:[]}));
 t.after(()=>subscriptions.forEach(s=>s.dispose?.()));await native.ready;
 const tracker=factory.createDebugAdapterTracker(session);tracker.onDidSendMessage({type:'event',event:'stopped',body:{threadId:7}});tracker.onWillReceiveMessage({type:'request',command:'stackTrace',seq:1,arguments:{threadId:7}});tracker.onDidSendMessage({type:'response',command:'stackTrace',request_seq:1,success:true,body:{stackFrames:[{id:42}]}});
 const settle=async()=>{for(let i=0;i<100&&!['answered','failed'].includes(document.threads[0]?.delivery.status);i++)await new Promise(r=>setTimeout(r,5));};
 return{native,api,model,calls,views,commands,prompts,writes,tracker,settle,get document(){return document;}};
}
test('inline provider persists question before call and canonical answer after stream',async t=>{
 const h=await harness(t);await h.native.ask();const view=h.views[0];await h.commands.get('brote.nativeSend')({thread:view,text:'Why?'});await h.settle();
 assert.equal(h.document.threads[0].delivery.status,'answered');assert.equal(h.document.threads[0].messages[1].body,'Answer');assert.match(h.prompts[0],/saved/);assert.ok(!h.writes.includes('nativeDiscussions'));
 const actions=h.calls.filter(x=>x.args?.[0]==='comment').map(x=>x.args[1]);assert.ok(actions.indexOf('create')<actions.indexOf('claim'));assert.ok(actions.indexOf('claim')<actions.indexOf('reply'));assert.equal(view.canReply,true);
});
test('provider failure is persisted and retry uses a new attempt',async t=>{
 const h=await harness(t);h.model.sendRequest=async()=>{throw Error('unavailable');};await h.native.ask();await h.commands.get('brote.nativeSend')({thread:h.views[0],text:'Why?'});await h.settle();assert.equal(h.document.threads[0].delivery.status,'failed');assert.match(h.document.threads[0].delivery.error,/unavailable/);
});
test('attached-agent route saves without invoking a provider',async t=>{
 const h=await harness(t);h.api.window.showQuickPick=async items=>items.find(x=>x.recipient?.kind==='agent')||items[0];await h.native.ask();await h.commands.get('brote.nativeSend')({thread:h.views[0],text:'Explain'});assert.equal(h.document.threads[0].delivery.recipient.kind,'agent');assert.equal(h.prompts.length,0);
});
test('Chat saves ordinary turns by exact metadata ID and historical follow-ups',async t=>{
 const h=await harness(t);const stream={markdown(){}};const id=await h.native.chat('same text',h.model,stream,{isCancellationRequested:false});h.api.debug.activeDebugSession=undefined;await h.native.chat('same text',h.model,stream,{isCancellationRequested:false},id);assert.equal(h.document.threads.length,1);assert.equal(h.document.threads[0].messages.length,4);assert.equal(h.document.threads[0].messages[0].context.run,'run');
});
test('draft discard writes no question and stale frame cannot capture',async t=>{
 const h=await harness(t);await h.native.ask();await h.commands.get('brote.nativeDiscard')(h.views[0]);assert.equal(h.document.threads.length,0);h.tracker.onDidSendMessage({type:'event',event:'continued'});await assert.rejects(h.native.capture(),/Select the stack frame/);
});
test('paginated selected frame goes through service inspection',async t=>{
 const h=await harness(t);h.tracker.onWillReceiveMessage({type:'request',command:'stackTrace',seq:2,arguments:{threadId:7,startFrame:30}});h.tracker.onDidSendMessage({type:'response',command:'stackTrace',request_seq:2,success:true,body:{stackFrames:[{id:42}]}});await h.native.capture();assert.equal(h.calls.find(x=>x.capture).capture.index,30);
});

test('failed Chat response keeps the saved discussion metadata association',async t=>{
 const h=await harness(t);h.model.sendRequest=async()=>{throw Error('provider lost');};let message='';const id=await h.native.chat('Why?',h.model,{markdown:text=>{message+=text;}},{isCancellationRequested:false});assert.ok(id);assert.equal(h.document.threads[0].delivery.status,'failed');assert.match(message,/Answer failed/);assert.equal(h.native.discussion(id).question,'Why?');
});

test('inline history retains limitations and provenance of each saved question',async t=>{const h=await harness(t);const id=await h.native.chat('first',h.model,{markdown(){}},{isCancellationRequested:false});const first=h.document.threads[0].messages[0];first.context={...first.context,partial:true,truncated:true,inspectionError:'scope unavailable',run:'old-run',pauseEpoch:7,frames:[{Locals:[{name:'x',value:'1'}],localsTruncated:true}]};h.api.debug.activeDebugSession=undefined;await h.native.chat('second',h.model,{markdown(){}},{isCancellationRequested:false},id);const saved=h.native.discussion(id);assert.match(saved.turns[0].contextNote,/old-run/);assert.match(saved.turns[0].contextNote,/pause 7/);assert.match(saved.turns[0].contextNote,/localsTruncated/);assert.match(saved.turns[0].contextNote,/scope unavailable/);assert.equal(saved.turns[0].evidence.scopes[0].variables[0].value,'1');assert.match(h.views[0].comments[0].body.value,/scope unavailable/);});

test('shutdown persists interrupted outcome even when a provider ignores cancellation',async t=>{const h=await harness(t);let release;h.model.sendRequest=async()=>({text:(async function*(){yield 'partial';await new Promise(r=>release=r);yield 'late';})()});await h.native.ask();await h.commands.get('brote.nativeSend')({thread:h.views[0],text:'Why?'});for(let i=0;i<100&&!release;i++)await new Promise(r=>setTimeout(r,5));assert.ok(release);await h.native.shutdown();const thread=h.document.threads[0];assert.equal(thread.delivery.status,'failed');assert.match(thread.delivery.error,/editor is closing/);assert.equal(thread.messages.length,1);release();await new Promise(r=>setTimeout(r,5));assert.equal(thread.messages.length,1,'late provider output cannot become a final answer');assert.ok(!h.views[0].comments.some(c=>c.body.value==='partial'));});

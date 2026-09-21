const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const settled=async(h)=>{for(let i=0;i<100 && !['Answered'].includes(h.saved[0]?.status) && !h.saved[0]?.status?.startsWith('Failed:');i++)await new Promise(r=>setTimeout(r,5));};
const {build}=require('esbuild');
async function harness(){
 let factory,participantPrompt,saved=[];const requests=[],views=[],commands=new Map();
 class Frame{constructor(session){this.session=session;this.threadId=7;this.frameId=42;}}
 const session={id:'native-session',name:'F5 Go',type:'go',customRequest:async(command)=>{requests.push(command);if(command==='stackTrace')return {stackFrames:[{id:42,name:'process',line:17}]};if(command==='scopes')return {scopes:[{name:'Locals',variablesReference:8}]};if(command==='variables')return {variables:[{name:'total',value:'21',type:'int'}]};throw Error(command);}};
 const prompts=[];const model={id:'test-model',name:'Test model',vendor:'test',sendRequest:async(messages)=>{prompts.push(messages[0].content);return {text:(async function*(){yield 'Captured total is 21.';})()};}};
 const api={lm:{selectChatModels:async()=>[model]},CancellationTokenSource:class{token={isCancellationRequested:false};dispose(){}},LanguageModelChatMessage:{User:content=>({content})},DebugStackFrame:Frame,debug:{activeDebugSession:session,activeStackItem:new Frame(session),registerDebugAdapterTrackerFactory:(_,f)=>{factory=f;return {dispose(){}};}},comments:{createCommentController:()=>({dispose(){},createCommentThread:()=>{const view={dispose(){this.disposed=true;}};views.push(view);return view;}})},Uri:{file:p=>p},Range:class{},MarkdownString:class{constructor(value){this.value=value;}},CommentMode:{Preview:0},CommentThreadCollapsibleState:{Expanded:1,Collapsed:0},workspace:{isTrusted:true},window:{activeTextEditor:{document:{uri:{scheme:'file',fsPath:'/tmp/main.go'}},selection:{active:{line:16}}},showQuickPick:async(items)=>items[0],showErrorMessage:()=>{},showInputBox:async()=>{throw Error('Must not show a second question box');}},commands:{registerCommand:(name,fn)=>{commands.set(name,fn);return {dispose(){}};},executeCommand:async(_,{query,isPartialQuery})=>{assert.equal(isPartialQuery,false);participantPrompt=query;}}};
 const code=await build({entryPoints:['packages/vscode/src/native.ts'],bundle:true,platform:'node',format:'cjs',external:['vscode'],write:false});
 const module={exports:{}};vm.runInNewContext(code.outputFiles[0].text,{module,exports:module.exports,require:n=>n==='vscode'?api:require(n),Buffer});
 const native=module.exports.nativeDiscussions({subscriptions:[],workspaceState:{get:(key)=>key==='nativeDiscussions'?[]:undefined,update:async(key,records)=>{if(key==='nativeDiscussions')saved=JSON.parse(JSON.stringify(records));}}});
 const tracker=factory.createDebugAdapterTracker(session);tracker.onDidSendMessage({type:'event',event:'stopped',body:{threadId:7}});
 return {native,api,tracker,requests,views,commands,model,prompts,get saved(){return saved;},get prompt(){return participantPrompt;}};
}
test('inline question and follow-up persist both turns without opening chat',async()=>{
 const h=await harness();await h.native.ask();assert.equal(h.prompt,undefined);assert.equal(h.saved.length,0);
 await h.commands.get('debugHandover.nativeSend')({thread:h.views[0],text:'Why is total 21?'});await settled(h);
 assert.equal(h.saved[0].answer,'Captured total is 21.');assert.equal(h.views[0].canReply,true);assert.equal(h.views[0].collapsibleState,1);
 await h.commands.get('debugHandover.nativeSend')({thread:h.views[0],text:'And why did it change?'});await settled(h);
 assert.equal(h.saved[0].turns.length,1);assert.equal(h.views[0].comments.length,4);
 assert.match(h.prompts[1],/Why is total 21/);assert.match(h.prompts[1],/And why did it change/);
 assert.equal(h.prompt,undefined,'inline conversation must not open chat');
});
test('F5 capture rejects mixed evidence when the debugger resumes',async()=>{
 const h=await harness();const original=h.api.debug.activeDebugSession.customRequest;
 h.api.debug.activeDebugSession.customRequest=async command=>{const r=await original(command);if(command==='variables')h.tracker.onDidSendMessage({type:'event',event:'continued'});return r;};
 await assert.rejects(h.native.capture(),/moved/);assert.equal(h.saved.length,0);
});
test('model failure is persisted as failed rather than left thinking',async()=>{
 const h=await harness();await h.native.ask();h.model.sendRequest=async()=>{throw Error('unavailable');};
 await h.commands.get('debugHandover.nativeSend')({thread:h.views[0],text:'Why?'});await settled(h);
 assert.match(h.saved[0].status,/Failed:.*unavailable.*retry available/);
});
test('discarding an inline draft sends no chat request and saves no question',async()=>{
 const h=await harness();await h.native.ask();await h.commands.get('debugHandover.nativeDiscard')(h.views[0]);assert.equal(h.saved.length,0);assert.equal(h.prompt,undefined);assert.equal(h.views[0].disposed,true);
});

test('follow-up after debugger exit labels saved evidence as historical',async()=>{
 const h=await harness();await h.native.ask();await h.commands.get('debugHandover.nativeSend')({thread:h.views[0],text:'First question'});await settled(h);
 const before=h.requests.length;h.api.debug.activeDebugSession=undefined;
 await h.commands.get('debugHandover.nativeSend')({thread:h.views[0],text:'Explain the earlier result'});await settled(h);
 assert.equal(h.requests.length,before);assert.match(h.saved[0].contextNote,/historical.*debugger has ended/);
 assert.match(h.prompts[1],/First question/);
});

test('reply submission returns while model is pending and preserves its editor',async()=>{
 const h=await harness();let release;const pending=new Promise(resolve=>{release=resolve;});
 h.model.sendRequest=async()=>{await pending;return {text:(async function*(){yield 'Done';})()};};
 await h.native.ask();await h.commands.get('debugHandover.nativeSend')({thread:h.views[0],text:'Question'});
 assert.equal(h.views[0].canReply,true);assert.equal(h.views[0].contextValue,'agentdebugger.nativeBusy');
 release();await settled(h);assert.equal(h.saved[0].answer,'Done');assert.equal(h.views[0].contextValue,'agentdebugger.native');
});

test('capture fetches selected paginated frame even when frame IDs change',async()=>{
 const h=await harness();
 h.tracker.onWillReceiveMessage({type:'request',command:'stackTrace',seq:10,arguments:{threadId:7,startFrame:30}});
 h.tracker.onDidSendMessage({type:'response',command:'stackTrace',request_seq:10,success:true,body:{stackFrames:[{id:42}]}});
 const original=h.api.debug.activeDebugSession.customRequest;
 h.api.debug.activeDebugSession.customRequest=async(command,args)=>{
  if(command==='stackTrace'){assert.equal(args.startFrame,30);return {stackFrames:[{id:99,name:'deep',line:35}]};}
  if(command==='scopes')assert.equal(args.frameId,99);
  return original(command,args);
 };
 const evidence=await h.native.capture();assert.equal(evidence.frame.id,99);
});

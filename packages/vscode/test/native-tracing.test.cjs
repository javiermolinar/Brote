const {test}=require('node:test');
const assert=require('node:assert/strict');
const path=require('node:path');
const fs=require('node:fs');
const vm=require('node:vm');
const {createRequire,builtinModules}=require('node:module');
const root=process.cwd();
const req=createRequire(path.join(root,'package.json'));
const {buildSync}=req('esbuild');
const compile=file=>buildSync({entryPoints:[path.join(root,'packages/vscode/src',file)],bundle:false,platform:'node',format:'cjs',write:false}).outputFiles[0].text;
const source={native:compile('nativeTraceCapture.ts'),extension:compile('nativeTracing.ts')};
function load(code,loader,extra={}){const module={exports:{}};vm.runInNewContext(code,{module,exports:module.exports,require:loader,Buffer,setTimeout,clearTimeout,setInterval,clearInterval,URL,process,console,performance,TextEncoder,TextDecoder,...extra},{importModuleDynamically:vm.constants.USE_MAIN_CONTEXT_DEFAULT_LOADER});return module.exports;}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
async function harness({enabled=false,bundle,exportEnv,traceAction='Copy ID'}={}){
 const exportCallbacks=[];
 const factories=[],views=[],commands=new Map(),tools=new Map(),traces=[],spans=[],logs=[],requests=[],saved=new Map(),prompts=[];
 const model={id:'test',name:'Test',vendor:'test',sendRequest:async(messages)=>{prompts.push(messages);return {text:(async function*(){yield 'Evidence answer';})()};}};
 const session={id:'s1',name:'Test program',type:'go',customRequest:async(command,args)=>{requests.push({command,args});if(command==='stackTrace')return {stackFrames:[{id:42,name:'main.work',line:3}]};if(command==='scopes')return {scopes:[{name:'Locals',variablesReference:8}]};if(command==='variables')return {variables:[{name:'value',value:'21',variablesReference:0}]};throw Error(command);}};
 class Frame {constructor(s,threadId=7,frameId=42){this.session=s;this.threadId=threadId;this.frameId=frameId;}}
 class ResponseTurn {constructor(result,response=[]){this.result=result;this.response=response;}}
 class RequestTurn {constructor(prompt){this.prompt=prompt;}}
 class MarkdownPart {constructor(value){this.value={value};}}
 const disposable=()=>({dispose(){}});
 let participant;
 const api={DebugStackFrame:Frame,ChatResponseTurn:ResponseTurn,ChatRequestTurn:RequestTurn,ChatResponseMarkdownPart:MarkdownPart,
  LanguageModelChatMessage:{User:content=>({role:'user',content}),Assistant:content=>({role:'assistant',content})},LanguageModelToolResult:class{constructor(content){this.content=content;}},LanguageModelTextPart:class{constructor(value){this.value=value;}},
  CancellationTokenSource:class{token={isCancellationRequested:false};cancel(){this.token.isCancellationRequested=true;}dispose(){}},
  Uri:{file:p=>p},Range:class{},MarkdownString:class{constructor(value){this.value=value;}},CommentMode:{Preview:0},CommentThreadCollapsibleState:{Expanded:1},
  debug:{activeDebugSession:session,activeStackItem:new Frame(session),registerDebugAdapterTrackerFactory:(_,f)=>{factories.push(f);return disposable();}},
  env:{clipboard:{writeText:async text=>saved.set('copiedTrace',text)}},
  workspace:{openTextDocument:async document=>{saved.set('openedTrace',document);return document;},isTrusted:true,getConfiguration:()=>({get:(_,d)=>d})},
  comments:{createCommentController:()=>({dispose(){},createCommentThread:()=>{const view={dispose(){this.disposed=true;}};views.push(view);return view;}})},
  window:{showTextDocument:async()=>{},createOutputChannel:()=>({appendLine:m=>logs.push(m),dispose(){}}),activeTextEditor:{document:{uri:{scheme:'file',fsPath:'/tmp/main.go'}},selection:{active:{line:2}}},showErrorMessage:m=>logs.push(m),showQuickPick:async items=>typeof items[0]==='string'?traceAction:items[0]},
  commands:{registerCommand:(name,fn)=>{commands.set(name,fn);return disposable();},executeCommand:async(name,args)=>logs.push({name,args})},
  lm:{selectChatModels:async()=>[model],registerTool:(name,t)=>{tools.set(name,t);return disposable();}},
  chat:{createChatParticipant:(name,handler)=>{participant=handler;return disposable();}}
 };
 const context={extensionPath:'/extension',globalStorageUri:{fsPath:'/storage'},subscriptions:[],workspaceState:{get:(key,d)=>saved.get(key)??d,update:async(key,data)=>saved.set(key,JSON.parse(JSON.stringify(data)))}};
 const nativeModule=load(source.native,n=>n==='vscode'?api:req(n));
 const runtimeRequires=[];
 const core={CoreTracing:class{
  records(){return Promise.resolve(traces.map(t=>t.record));}
  async traceJSON(id){return JSON.stringify({traceId:id});}
  session(id,name,type,onRecord){
   const record={session:id,name,program:'a'.repeat(32),debugger:'b'.repeat(32),local:{}};
   const events=[];let closed=false;
   const t={record,events,request:(seq,command,thread)=>events.push({kind:'request',seq,command,thread}),response:(seq,success)=>events.push({kind:'response',seq,success}),stopped:(kind,thread,allThreads)=>events.push({kind,thread,allThreads}),snapshot:(observation,label,selections)=>events.push({kind:'snapshot',observation,label,selections}),flush:async()=>{},close:async()=>{if(!closed){closed=true;events.push({kind:'close'});}}};
   traces.push(t);onRecord(record);return t;
  }
 }};
 const loader=n=>{runtimeRequires.push(n);if(n==='vscode')return api;if(n==='./nativeTraceCapture')return nativeModule;if(n==='./coreTrace')return core;if(bundle && !builtinModules.includes(n)&&!n.startsWith('node:'))throw new Error('Unbundled dependency: '+n);return req(n);};
 const env=exportEnv||(enabled?{OTEL_EXPORTER_OTLP_ENDPOINT:'http://127.0.0.1:4318'}:{});
 const extension=load(bundle?fs.readFileSync(bundle,'utf8'):source.extension,loader,{process:{...process,env}});
 const shutdown=extension.registerNativeTracing(context,'brote');extension.deactivate=shutdown;
 const trackers=(await Promise.all(factories.map(f=>f.createDebugAdapterTracker(session)))).filter(Boolean);
 const event=m=>trackers.forEach(t=>t.onDidSendMessage?.(m));
 const request=m=>trackers.forEach(t=>t.onWillReceiveMessage?.(m));
 return {exportCallbacks,api,model,context,session,trackers,factories,event,request,extension,traces,spans,logs,requests,commands,tools,views,saved,prompts,runtimeRequires,get participant(){return participant;}};
}


test('native adapter forwards DAP observations to the core API',async()=>{
 const h=await harness();try{
 h.request({type:'request',seq:1,command:'next',arguments:{threadId:7}});
 h.event({type:'response',request_seq:1,success:true});
 h.event({type:'event',event:'stopped',body:{threadId:8,reason:'step'}});
 assert.deepEqual(JSON.parse(JSON.stringify(h.traces[0].events)),[{kind:'request',seq:1,command:'next',thread:7},{kind:'response',seq:1,success:true},{kind:'stopped',thread:8,allThreads:false}]);
 }finally{await h.extension.deactivate();}
});
test('host stop closes the core capture exactly once',async()=>{
 const h=await harness();try{const tracker=h.trackers[1];tracker.onWillStopSession();tracker.onWillStopSession();tracker.onExit();await tick();assert.equal(h.traces[0].events.filter(e=>e.kind==='close').length,1);}finally{await h.extension.deactivate();}
});
for(const reason of ['breakpoint','function breakpoint','data breakpoint','instruction breakpoint'])test(`captures ${reason} on the reported thread`,async()=>{
 const h=await harness();try{h.event({type:'event',event:'stopped',body:{threadId:99,reason}});await tick();assert.equal(h.requests[0].args.threadId,99);assert.equal(h.traces[0].events.filter(e=>e.kind==='snapshot').length,1);}finally{await h.extension.deactivate();}
});
for(const reason of ['step','pause','exception','entry'])test(`does not automatically capture ${reason}`,async()=>{
 const h=await harness();try{h.event({type:'event',event:'stopped',body:{threadId:7,reason}});await tick();assert.equal(h.requests.length,0);}finally{await h.extension.deactivate();}
});
test('tracker registration returns synchronously while core tracing starts',async()=>{
 const h=await harness();try{const tracker=h.factories[1].createDebugAdapterTracker({...h.session,id:'sync-registration'});assert.ok(tracker);assert.equal(typeof tracker.then,'undefined');tracker.onWillStopSession();}finally{await h.extension.deactivate();}
});
test('session trace actions use core records and query APIs',async()=>{
 for(const action of ['Copy ID','Open trace JSON']){const h=await harness({traceAction:action});try{await h.commands.get('brote.traces')();const id=h.traces[0].record.program;if(action==='Copy ID')assert.equal(h.saved.get('copiedTrace'),id);else assert.equal(JSON.parse(h.saved.get('openedTrace').content).traceId,id);}finally{await h.extension.deactivate();}}
});

test('shared Brote sessions are excluded from native trace producers',async()=>{const h=await harness();try{for(const factory of h.factories)assert.equal(factory.createDebugAdapterTracker({...h.session,type:'brote'}),undefined);}finally{await h.extension.deactivate();}});

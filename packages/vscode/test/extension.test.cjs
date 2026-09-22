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
const source={native:compile('native.ts'),telemetry:compile('telemetry.ts'),extension:compile('extension.ts')};
function load(code,loader,extra={}){const module={exports:{}};vm.runInNewContext(code,{module,exports:module.exports,require:loader,Buffer,setTimeout,clearTimeout,setInterval,clearInterval,URL,process,console,performance,TextEncoder,TextDecoder,...extra},{importModuleDynamically:vm.constants.USE_MAIN_CONTEXT_DEFAULT_LOADER});return module.exports;}
const telemetry=load(source.telemetry,req);
const tick=()=>new Promise(resolve=>setImmediate(resolve));
async function harness({enabled=false,bundle,exportEnv}={}){
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
  workspace:{isTrusted:true,getConfiguration:()=>({get:(_,d)=>d})},
  comments:{createCommentController:()=>({dispose(){},createCommentThread:()=>{const view={dispose(){this.disposed=true;}};views.push(view);return view;}})},
  window:{createOutputChannel:()=>({appendLine:m=>logs.push(m),dispose(){}}),activeTextEditor:{document:{uri:{scheme:'file',fsPath:'/tmp/main.go'}},selection:{active:{line:2}}},showErrorMessage:m=>logs.push(m),showQuickPick:async items=>items[0]},
  commands:{registerCommand:(name,fn)=>{commands.set(name,fn);return disposable();},executeCommand:async(name,args)=>logs.push({name,args})},
  lm:{selectChatModels:async()=>[model],registerTool:(name,t)=>{tools.set(name,t);return disposable();}},
  chat:{createChatParticipant:(name,handler)=>{participant=handler;return disposable();}}
 };
 const context={extensionPath:'/extension',subscriptions:[],workspaceState:{get:(key,d)=>saved.get(key)??d,update:async(key,data)=>saved.set(key,JSON.parse(JSON.stringify(data)))}};
 const nativeModule=load(source.native,n=>n==='vscode'?api:req(n));
 const modifiedTelemetry={...telemetry,createSessionTrace:(id,name,type)=>{const t=new telemetry.SessionTrace(id,name,type,()=>({export(batch,done){spans.push(...batch);done({code:0});},shutdown:async()=>{}}));traces.push(t);return t;}};
 const runtimeRequires=[];
 const loader=n=>{runtimeRequires.push(n);if(n==='vscode')return api;if(n==='./native')return nativeModule;if(n==='./telemetry')return modifiedTelemetry;if(bundle && !builtinModules.includes(n)&&!n.startsWith('node:'))throw new Error('Unbundled dependency: '+n);return req(n);};
 const env=exportEnv||(enabled?{OTEL_EXPORTER_OTLP_ENDPOINT:'http://127.0.0.1:4318'}:{});
 const extension=load(bundle?fs.readFileSync(bundle,'utf8'):source.extension,loader,{process:{...process,env}});
 extension.activate(context);
 const trackers=factories.map(f=>f.createDebugAdapterTracker(session)).filter(Boolean);
 const event=m=>trackers.forEach(t=>t.onDidSendMessage?.(m));
 const request=m=>trackers.forEach(t=>t.onWillReceiveMessage?.(m));
 return {api,model,context,session,trackers,factories,event,request,extension,traces,spans,logs,requests,commands,tools,views,saved,prompts,runtimeRequires,get participant(){return participant;}};
}

for(const ordering of ['response-first','event-first'])test(`omitted allThreadsStopped keeps other threads pending (${ordering})`,async()=>{
 const h=await harness({enabled:true});
 try{
  h.request({type:'request',seq:1,command:'next',arguments:{threadId:7}});
  if(ordering==='response-first')h.event({type:'response',request_seq:1,success:true});
  h.event({type:'event',event:'stopped',body:{threadId:8,reason:'step'}});
  if(ordering==='event-first')h.event({type:'response',request_seq:1,success:true});
  await h.traces[0].flush();assert.equal(h.spans.filter(s=>s.name==='next').length,0);
  h.event({type:'event',event:'stopped',body:{threadId:7,reason:'step'}});
  await h.traces[0].flush();assert.equal(h.spans.filter(s=>s.name==='next').length,1);
 }finally{await h.extension.deactivate();}
});
test('explicit allThreadsStopped still completes execution on another thread',async()=>{
 const h=await harness({enabled:true});
 try{
  h.request({type:'request',seq:1,command:'next',arguments:{threadId:7}});
  h.event({type:'event',event:'stopped',body:{threadId:8,reason:'step',allThreadsStopped:true}});
  h.event({type:'response',request_seq:1,success:true});await h.traces[0].flush();
  assert.equal(h.spans.find(s=>s.name==='next').attributes['debugger.outcome'],'stopped');
 }finally{await h.extension.deactivate();}
});
test('host stop finalizes roots once and releases slots without terminated or process exit',async()=>{
 const h=await harness({enabled:true});
 try{
  const factory=h.factories[1];
  for(let i=0;i<20;i++){
   const tracker=i===0?h.trackers[1]:factory.createDebugAdapterTracker({...h.session,id:`session-${i}`});assert.ok(tracker);
   tracker.onWillStopSession();tracker.onWillStopSession();tracker.onExit();
  }
  await h.extension.deactivate();
  assert.equal(h.spans.filter(s=>s.name==='debugger.session').length,20);
  assert.equal(h.spans.filter(s=>s.attributes['program.span.type']==='run').length,20);
 }finally{await h.extension.deactivate();}
});
for(const reason of ['breakpoint','function breakpoint','data breakpoint','instruction breakpoint'])test(`captures ${reason} on the reported thread`,async()=>{
 const h=await harness({enabled:true});
 try{
  h.event({type:'event',event:'stopped',body:{threadId:99,reason}});await tick();
  assert.equal(h.requests[0].args.threadId,99);
  await h.traces[0].flush();assert.equal(h.spans.filter(s=>s.attributes['program.span.type']==='snapshot').length,1);
 }finally{await h.extension.deactivate();}
});
for(const reason of ['step','pause','exception','entry'])test(`does not automatically capture ${reason}`,async()=>{
 const h=await harness({enabled:true});
 try{h.event({type:'event',event:'stopped',body:{threadId:7,reason}});await tick();assert.equal(h.requests.length,0);}
 finally{await h.extension.deactivate();}
});

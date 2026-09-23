const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const {buildSync}=require('esbuild');
const source=buildSync({entryPoints:['packages/vscode/src/extension.ts'],bundle:false,platform:'node',format:'cjs',write:false}).outputFiles[0].text;
function harness(){
 let captureProvider,participant;const tools=new Map(),commands=new Map(),calls=[];
 const native={shutdown:async()=>calls.push('shutdown'),ready:Promise.resolve(),chat:async()=>{calls.push('chat-saved');return 'saved-id';},capture:async()=>({session:'editor',name:'Shared',type:'brote',capturedAt:'now',thread:7,frame:{name:'main.work'},stack:[],scopes:[]}),ask:async()=>calls.push('ask'),discussion:()=>undefined};
 const api={debug:{},window:{showErrorMessage(){}},commands:{registerCommand:(id,fn)=>{commands.set(id,fn);return{};}},chat:{createChatParticipant:(_,handler)=>{participant=handler;return{};}},lm:{registerTool:(id,tool)=>{tools.set(id,tool);return{};}},Uri:{file:p=>p},LanguageModelToolResult:class{constructor(content){this.content=content;}},LanguageModelTextPart:class{constructor(value){this.value=value;}},LanguageModelChatMessage:{User:content=>({content})},ChatResponseTurn:class{},ChatRequestTurn:class{},ChatResponseMarkdownPart:class{}};
 const module={exports:{}};vm.runInNewContext(source,{module,exports:module.exports,require:id=>id==='vscode'?api:id==='./nativeTracing'?{registerNativeTracing:()=>async()=>{calls.push('tracing-shutdown');}}:id==='./native'?{nativeDiscussions:(_,provider)=>{captureProvider=provider;return native;}}:id==='./service'?{registerService:()=>({state:async(id,thread,frame)=>{calls.push({id,thread,frame});return {id,run:'run',status:'paused',pauseEpoch:3,truncated:true,inspectionError:'deadline',exception:{description:'panic'},goroutine:thread,frame,frames:[{function:{name:'first'},file:'/main.go',line:1},{function:{name:'selected'},file:'/main.go',line:2,Locals:[{name:'value',value:'7'}],localsTruncated:true,argumentsError:'timeout'}]};}})}:id==='./discussionMigration'?{migrateDiscussions:async()=>({discussions:[]})}:id==='./serviceTracepoints'?{serviceTracepoints:()=>{}}:require(id)});
 module.exports.activate({extensionPath:'/extension',subscriptions:[]});return{native,deactivate:module.exports.deactivate,calls,commands,tools,get captureProvider(){return captureProvider;},get participant(){return participant;}};
}
test('extension evidence uses selected service session/goroutine/frame',async()=>{
 const h=harness();const evidence=await h.captureProvider({id:'editor',name:'Shared',configuration:{sessionId:'service'}},7,1);
 assert.equal(evidence.frame.name,'selected');assert.equal(evidence.scopes[0].variables[0].value,'7');assert.equal(evidence.run,'run');assert.equal(evidence.pauseEpoch,3);assert.equal(evidence.truncated,true);assert.equal(evidence.inspectionError,'deadline');assert.equal(evidence.scopes[0].truncated,true);assert.equal(evidence.scopes[1].error,'timeout');assert.equal(evidence.exception.description,'panic');assert.equal(h.calls[0].id,'service');
});
test('inspection tool and inline command retain presentation hooks',async()=>{
 const h=harness();const result=await h.tools.get('brote_inspect').invoke();assert.equal(JSON.parse(result.content[0].value).frame.name,'main.work');await h.commands.get('brote.ask')();assert.ok(h.calls.includes('ask'));
});
test('extension ships no native tracepoint controller or JS OTLP dependencies',()=>{
 const manifest=JSON.parse(fs.readFileSync('packages/vscode/package.json','utf8'));
 assert.ok(!Object.keys(manifest.dependencies||{}).some(k=>k.startsWith('@opentelemetry/')));
 for(const name of ['telemetry.ts','tracepoints.ts'])assert.equal(fs.existsSync(path.join('packages/vscode/src',name)),false);
 const native=fs.readFileSync('packages/vscode/src/native.ts','utf8');assert.ok(!native.includes('customRequest('));
 const panel=fs.readFileSync('packages/vscode/src/serviceTracepoints.ts','utf8');assert.ok(!panel.includes('customRequest('));assert.ok(!panel.includes('addBreakpoints('));
});

test('ordinary Chat answers are delegated to Go-backed discussion flow',async()=>{const h=harness();const result=await h.participant({prompt:'Why?',model:{}},{history:[]},{markdown(){}},{});assert.equal(result.metadata.nativeDiscussion,'saved-id');assert.ok(h.calls.includes('chat-saved'));});

test('Chat saved history shows per-turn evidence limitations and final outcome',async()=>{const h=harness();h.native.discussion=()=>({id:'saved',question:'Why?',contextNote:'Saved evidence · execution run1 · pause 7\nlocalsTruncated: true\ninspectionError: scope unavailable',status:'failed: Answer cancelled.'});let text='';await h.participant({command:'discuss',prompt:'saved'},{history:[]},{markdown:s=>text+=s},{});assert.match(text,/localsTruncated: true/);assert.match(text,/scope unavailable/);assert.match(text,/execution run1/);assert.match(text,/Answer cancelled/);});

test('extension deactivation awaits discussion shutdown',async()=>{const h=harness();await h.deactivate();assert.ok(h.calls.includes('shutdown'));});

const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {buildSync}=require('esbuild');
const code=buildSync({entryPoints:['packages/vscode/src/service.ts'],bundle:true,platform:'node',format:'cjs',write:false,external:['vscode']}).outputFiles[0].text;
function harness(){
 const calls=[],commands=new Map();let factory,provider;
 const descriptor={id:'session',run:'run',serviceVersion:1,status:'paused'};
 const api={workspace:{isTrusted:true,getConfiguration:()=>({get:()=>'/runtime/brote'}),workspaceFolders:[]},window:{showQuickPick:async rows=>rows[0],showErrorMessage(){}},debug:{registerDebugAdapterDescriptorFactory:(_,f)=>{factory=f;return{};},registerDebugConfigurationProvider:(_,p)=>{provider=p;return{};},startDebugging:async(_,config)=>{calls.push(config);return true;}},commands:{registerCommand:(id,fn)=>{commands.set(id,fn);return{};}},DebugAdapterExecutable:class{constructor(command,args){this.command=command;this.args=args;}}};
 const module={exports:{}};
 vm.runInNewContext(code,{module,exports:module.exports,process,console,require:id=>id==='vscode'?api:id==='node:child_process'?{execFile:(file,args,options,done)=>{calls.push({file,args,options});done(null,JSON.stringify(args[0]==='sessions'?[{...descriptor,project:'/workspace',binary:'/workspace/demo'}]:descriptor),'');}}:require(id)});
 module.exports.registerService({extensionPath:'/extension',subscriptions:[]});return {api,calls,commands,get factory(){return factory;},get provider(){return provider;}};
}
test('shared adapter delegates authenticated DAP to the CLI',async()=>{
 const h=harness();const adapter=await h.factory.createDebugAdapterDescriptor({configuration:{sessionId:'session'}});
 assert.equal(adapter.command,'/runtime/brote');assert.deepEqual(Array.from(adapter.args),['dap','session']);
 assert.ok(h.calls.some(c=>c.args?.[0]==='state'));
 assert.equal(JSON.stringify(h.calls).includes('token'),false);
});
test('attach selection does not launch another target',async()=>{
 const h=harness();await h.commands.get('brote.attachSession')();
 assert.ok(h.calls.some(c=>c.type==='brote'&&c.request==='attach'&&c.sessionId==='session'));
 assert.ok(!h.calls.some(c=>c.args?.[0]==='start'));
 h.api.workspace.isTrusted=false;await assert.rejects(h.provider.resolveDebugConfiguration(undefined,{request:'attach',sessionId:'session'}),/Trust/);
});

test('service client cancellation signals the active CLI process',async()=>{
 const module={exports:{}};vm.runInNewContext(code,{module,exports:module.exports,process,console,require:id=>id==='vscode'?{}:require(id)});
 let notify,disposed=false;const token={isCancellationRequested:false,onCancellationRequested:fn=>{notify=fn;return {dispose(){disposed=true;}};}};
 const pending=new module.exports.ServiceClient(process.execPath).run(['-e','setInterval(()=>{},1000)'],45000,token);
 token.isCancellationRequested=true;notify();
 await assert.rejects(pending,/cancelled/);assert.equal(disposed,true);
 const missing=new module.exports.ServiceClient('/missing-brote-runtime');await assert.rejects(missing.run(['version']),/Brote command failed/);
});

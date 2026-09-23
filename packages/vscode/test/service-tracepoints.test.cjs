const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm');
const {buildSync}=require('esbuild');
const code=buildSync({entryPoints:['packages/vscode/src/serviceTracepoints.ts'],bundle:false,platform:'node',format:'cjs',write:false}).outputFiles[0].text;
function harness(legacy=false){
 const commands=new Map(),tools=new Map(),calls=[],saved=new Map();let items=[{id:'cli-point',revision:1,owner:'cli',kind:'tracepoint',enabled:true,location:{file:'/workspace/main.go',line:5},name:'CLI point',captureLimit:10}];
 const disposable=()=>({dispose(){}});
 const api={workspace:{isTrusted:true},debug:{activeDebugSession:{type:'brote',configuration:{sessionId:'session'}},breakpoints:[],removeBreakpoints(){if(!legacy)throw Error('service CRUD must not manipulate ordinary editor breakpoints');calls.push(['remove-native']);api.debug.breakpoints=[];}},SourceBreakpoint:class{},EventEmitter:class{event=()=>{};fire(){}dispose(){}},Range:class{},TreeItem:class{constructor(label){this.label=label;}},ThemeIcon:class{},Uri:{file:p=>p},window:{visibleTextEditors:[],createTextEditorDecorationType:disposable,registerTreeDataProvider:disposable,onDidChangeVisibleTextEditors:disposable,showQuickPick:async items=>items[0],showErrorMessage(){}},commands:{registerCommand:(id,fn)=>{commands.set(id,fn);return disposable();}},lm:{registerTool:(id,tool)=>{tools.set(id,tool);return disposable();}},LanguageModelToolResult:class{constructor(content){this.content=content;}},LanguageModelTextPart:class{constructor(value){this.value=value;}}};
 const client={run:async args=>{
  calls.push(args);
  if(args[0]==='state')return {id:'session',run:'run',status:'paused',definitions:{items},resolutions:items.map(d=>({definitionId:d.id,verified:true})),captureCounts:{'cli-point':2}};
  if(args[0]==='captures')return {captures:[{id:'capture',definitionId:'cli-point',status:'captured',exportStatus:'sent',run:'run'}]};
  const flag=k=>args[args.indexOf('--'+k)+1];
  if(args[1]==='update'){assert.equal(flag('client'),'cli');assert.equal(flag('revision'),'1');items=items.map(d=>({...d,revision:2,enabled:false}));}
  if(args[1]==='remove')items=[];
  if(args[1]==='add')items.push({id:args.includes('--id')?flag('id'):'ui-point',revision:1,owner:'vscode-ui',kind:'tracepoint',enabled:true,location:{file:flag('file'),line:Number(flag('line'))},name:flag('name')});
  return {definitions:{items}};
 }};
 if(legacy){const bp=new api.SourceBreakpoint();Object.assign(bp,{id:'old-native-id',enabled:true,location:{uri:{fsPath:'/workspace/main.go'},range:{start:{line:4}}}});api.debug.breakpoints=[bp];saved.set('tracepoints',[{id:bp.id,name:'Migrated',values:{value:'value'},hitLimit:5}]);}
 const context={extensionPath:'/extension',subscriptions:[],workspaceState:{get:(key,fallback)=>saved.get(key)||fallback,update:async(key,value)=>saved.set(key,value)}};
 const module={exports:{}};vm.runInNewContext(code,{module,exports:module.exports,require:id=>id==='vscode'?api:require(id),setInterval:()=>0,clearInterval(){}});
 const ui=module.exports.serviceTracepoints(context,client);return {ui,api,calls,tools,commands,saved};
}
const settle=()=>new Promise(resolve=>setImmediate(resolve));
test('panel and tool use CLI definitions and capture/export feedback',async()=>{
 const h=harness();await settle();const points=await h.ui.manage({action:'list'});
 assert.equal(points[0].id,'cli-point');assert.equal(points[0].hits,2);assert.match(points[0].status,/captured.*sent/);
 await h.ui.manage({action:'update',id:'cli-point',enabled:false});
 assert.ok(h.calls.some(args=>args.includes('--enabled=false')));
 assert.equal((await h.ui.manage({action:'list'}))[0].enabled,false);
});
test('UI creation uses semantic CLI and never installs a VS Code source breakpoint',async()=>{
 const h=harness();await settle();const point=await h.ui.manage({action:'add',file:'/workspace/main.go',line:5,name:'UI point',values:{value:'value'},hitLimit:3});
 assert.equal(point.id,'ui-point');assert.ok(h.calls.some(args=>args[0]==='tracepoint'&&args[1]==='add'&&args.includes('vscode-ui')));
 assert.ok(h.calls.every(args=>!['continue','next','dap'].includes(args[0])));
 h.api.debug.activeDebugSession.type='go';await assert.rejects(h.ui.manage({action:'add'}),/Brote session/);
});


test('legacy metadata migrates once to a stable service ID before its editor point is removed',async()=>{
 const h=harness(true);await settle();await h.ui.refresh();
 const stable=h.saved.get('tracepointServiceIDs')['old-native-id'];assert.ok(stable);assert.notEqual(stable,'old-native-id');
 assert.equal(h.saved.get('tracepoints').length,0);
 const creates=h.calls.filter(args=>args[0]==='tracepoint'&&args[1]==='add');assert.equal(creates.length,1);assert.ok(creates[0].includes(stable));
 assert.ok(h.calls.findIndex(args=>args[0]==='tracepoint')<h.calls.findIndex(args=>args[0]==='remove-native'));
});

const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm');
const {buildSync}=require('esbuild');
const source=buildSync({entryPoints:['packages/vscode/src/discussionMigration.ts'],bundle:true,platform:'node',format:'cjs',external:['vscode'],write:false}).outputFiles[0].text;
test('migration retains source and old tracepoint IDs before marking complete',async()=>{
 const saved=new Map([['nativeDiscussions',[{id:'old',answer:'large historical answer'}]],['tracepoints',[{id:'bp',values:{x:'x'}}]],['tracepointServiceIDs',{bp:'random-from-earlier-attempt'}]]),writes=[];
 class BP{constructor(){this.id='bp';this.location={uri:{fsPath:'/a.go'},range:{start:{line:4}}};this.enabled=false;this.condition='x>1';}}
 const api={workspace:{workspaceFolders:[{uri:{toString:()=>'/workspace'}}]},debug:{breakpoints:[new BP()]},SourceBreakpoint:BP};
 const m={exports:{}};vm.runInNewContext(source,{module:m,exports:m.exports,require:n=>n==='vscode'?api:require(n)});
 const context={workspaceState:{get:(k,d)=>saved.has(k)?saved.get(k):d,update:async(k,v)=>{writes.push(k);saved.set(k,v);}}};let sourceData;
 const client={withBody:async(_,body)=>{sourceData=JSON.parse(body);assert.ok(saved.has('nativeDiscussionImportBackupV1'));assert.equal(saved.get('goDiscussionImportV1'),undefined);return{discussions:[],tracepointServiceIDs:sourceData.tracepointServiceIDs};},run:async()=>({discussions:[]})};
 await m.exports.migrateDiscussions(context,client);
 assert.equal(sourceData.tracepointServiceIDs.bp,'random-from-earlier-attempt');assert.equal(sourceData.tracepoints[0].condition,'x>1');assert.equal(sourceData.tracepoints[0].line,5);assert.equal(saved.get('nativeDiscussions')[0].id,'old');assert.equal(writes.at(-1),'goDiscussionImportV1');
});

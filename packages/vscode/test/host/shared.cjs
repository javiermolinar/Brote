const vscode=require('vscode'),assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path'),{execFile}=require('node:child_process');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
exports.run=async()=>{
 const root=process.env.BROTE_HOST_TEST_DIR,id=process.env.BROTE_HOST_SESSION;
 const extension=vscode.extensions.getExtension('brote-local.brote');assert.ok(extension);await extension.activate();
 const core=path.join(extension.extensionPath,'runtime/brote');
 const cli=(...args)=>new Promise((resolve,reject)=>execFile(core,args,{timeout:20000,maxBuffer:4*1024*1024},(err,out,stderr)=>err?reject(Error(stderr)):resolve(JSON.parse(out))));
 const events=[],handles=[];let session;
 handles.push(vscode.debug.registerDebugAdapterTrackerFactory('brote',{createDebugAdapterTracker(s){session=s;return {onDidSendMessage(m){events.push(m);}};}}));
 async function waitFor(predicate){for(let i=0;i<150;i++){const value=predicate();if(value)return value;await delay(100);}throw Error('Host event timeout');}
 try{
  const before=await cli('state',id);
  assert.equal(await vscode.debug.startDebugging(vscode.workspace.workspaceFolders[0],{type:'brote',name:'Shared Brote attach',request:'attach',sessionId:id}),true);
  await waitFor(()=>events.find(e=>e.event==='stopped'));
  const point=new vscode.SourceBreakpoint(new vscode.Location(vscode.Uri.file(path.join(root,'main.go')),new vscode.Position(4,0)));
  vscode.debug.addBreakpoints([point]);
  await waitFor(()=>events.find(e=>e.type==='response'&&e.command==='setBreakpoints'&&e.body?.breakpoints?.some(p=>p.verified)));
  const records=(await cli('breakpoint','list',id)).definitions.items;assert.equal(records.filter(d=>d.owner==='editor').length,1);
  const continued=await session.customRequest('continue',{threadId:1});assert.ok(continued);
  await waitFor(()=>events.filter(e=>e.event==='stopped').length>=2);
  const cliPoint=await cli('tracepoint','add',id,'--file',path.join(root,'main.go'),'--line','5','--name','CLI shared','--values','{"value":"value"}');
  const cliID=cliPoint.definitions.items.find(d=>d.kind==='tracepoint').id;
  const tool=async input=>JSON.parse((await vscode.lm.invokeTool('brote_tracepoints',{input},new vscode.CancellationTokenSource().token)).content.map(c=>c.value).join(''));
  for(let i=0;i<30;i++){if((await tool({action:'list'})).some(d=>d.id===cliID))break;await delay(100);}
  assert.ok((await tool({action:'list'})).some(d=>d.id===cliID));
  const uiPoint=await tool({action:'add',file:path.join(root,'main.go'),line:5,name:'UI shared',values:{value:'value'},hitLimit:2});
  assert.ok((await cli('tracepoint','list',id)).definitions.items.some(d=>d.id===uiPoint.id&&d.owner==='vscode-ui'));
  await tool({action:'update',id:cliID,enabled:false});
  assert.equal((await cli('tracepoint','list',id)).definitions.items.find(d=>d.id===cliID).enabled,false);
  const state=await cli('state',id);assert.equal(state.status,'paused');assert.equal(state.state.Pid,before.state.Pid);
  const stack=await session.customRequest('stackTrace',{threadId:state.goroutine,levels:10});assert.ok(stack.stackFrames.length);
  const scopes=await session.customRequest('scopes',{frameId:stack.stackFrames[0].id});assert.ok(scopes.scopes.length);
  const evidence=JSON.parse((await vscode.lm.invokeTool('brote_inspect',{input:{}},new vscode.CancellationTokenSource().token)).content.map(c=>c.value).join(''));
  assert.equal(evidence.thread,state.goroutine);assert.ok(evidence.scopes.some(s=>s.variables.some(v=>v.name==='value'&&v.value==='7')));
  await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(vscode.Uri.file(path.join(root,'main.go'))));
  await vscode.commands.executeCommand('brote.ask');
  await assert.rejects(session.customRequest('setVariable',{variablesReference:scopes.scopes[0].variablesReference,name:'value',value:'99'}));
  const stops=events.filter(e=>e.event==='stopped').length;
  await session.customRequest('next',{threadId:state.goroutine});
  await waitFor(()=>events.filter(e=>e.event==='stopped').length>stops);
  await vscode.debug.stopDebugging(session);
  await delay(200);
  const after=await cli('state',id);assert.equal(after.state.Pid,before.state.Pid);assert.equal(after.status,'paused');
  assert.equal(await vscode.debug.startDebugging(vscode.workspace.workspaceFolders[0],{type:'brote',name:'Reconnect',request:'attach',sessionId:id}),true);
  await waitFor(()=>events.filter(e=>e.event==='stopped').length>stops+1);
  assert.equal((await cli('state',id)).state.Pid,before.state.Pid);
  await fs.writeFile(path.join(root,'result.json'),JSON.stringify({vscode:vscode.version,session:id,pid:before.state.Pid,cliUISynchronization:true,serviceChatEvidence:true,editorBreakpoint:true,sharedStack:true,step:true,reconnectSameProcess:true,mutationRejected:true,disconnectPreservesProcess:true},null,2));
 }catch(error){await fs.writeFile(path.join(root,'failure.json'),JSON.stringify({error:String(error),stack:error.stack,events},null,2));throw error;}
 finally{if(session)await vscode.debug.stopDebugging(session);for(const h of handles)h.dispose();}
};

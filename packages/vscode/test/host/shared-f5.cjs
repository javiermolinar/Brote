const vscode=require('vscode'),assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path'),{execFile}=require('node:child_process');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
exports.run=async()=>{
 const root=process.env.BROTE_HOST_TEST_DIR,extension=vscode.extensions.getExtension('brote-local.brote');await extension.activate();
 const core=path.join(extension.extensionPath,'runtime/brote');
 const cli=(...args)=>new Promise((resolve,reject)=>execFile(core,args,{timeout:30000,maxBuffer:4*1024*1024},(err,out,stderr)=>err?reject(Error(stderr)):resolve(JSON.parse(out))));
 const events=[],sessions=[],handles=[];let session;
 handles.push(vscode.debug.registerDebugAdapterTrackerFactory('brote',{createDebugAdapterTracker(s){session=s;sessions.push(s);return {onDidSendMessage(m){events.push({session:s.id,...m});}};}}));
 async function until(fn){for(let i=0;i<250;i++){const v=await fn();if(v)return v;await delay(100);}throw Error('F5 host timeout');}
 const point=new vscode.SourceBreakpoint(new vscode.Location(vscode.Uri.file(path.join(root,'main.go')),new vscode.Position(4,0)));
 vscode.debug.addBreakpoints([point]);
 const report={vscode:vscode.version,scenarios:[]};
 try{
  for(const mode of ['debug','test','exec']){
   const mark=events.length;
   const config={type:'brote',request:'launch',name:`F5 ${mode}`,mode,program:mode==='exec'?path.join(root,'demo'):root,args:mode==='test'?['-test.run=^TestChosen$']:[],tracepoints:[{file:path.join(root,'main.go'),line:4,name:'f5.value',values:{value:'value'}}]};
   assert.equal(await vscode.debug.startDebugging(vscode.workspace.workspaceFolders[0],config),true);
   await until(()=>events.slice(mark).some(e=>e.event==='stopped'&&e.body.reason==='breakpoint'&&e.body.hitBreakpointIds?.length));
   const id=session.configuration.sessionId;assert.ok(id);
   // Tracepoint stop auto-resumes to the ordinary breakpoint on the next line.
   const state=await until(async()=>{const s=await cli('state',id);return s.status==='paused'&&s.frames?.[0]?.line===5?s:undefined;});
   assert.ok((await cli('sessions')).some(s=>s.id===id));
   assert.ok(state.frames[0].Locals.some(v=>v.name==='value'&&v.value==='7'));
   const captured=await cli('captures',id);assert.ok(captured.captures.some(c=>c.status==='captured'));
   const initialRun=state.run,pid=state.state.Pid;
   if(mode==='debug'){
    const restartMark=events.length;await session.customRequest('restart');
    await until(()=>events.slice(restartMark).some(e=>e.event==='stopped'));
    const newer=await until(async()=>{const s=await cli('state',id);return s.run!==initialRun&&s.status==='paused'&&s.frames?.[0]?.line===5?s:undefined;});
    assert.notEqual(newer.state.Pid,pid);assert.equal(session.configuration.sessionId,id);
    // Explicit editor disconnect retains service; then reconnect to same run.
    await session.customRequest('disconnect',{terminateDebuggee:false});await delay(300);
    assert.equal((await cli('state',id)).state.Pid,newer.state.Pid);
    assert.equal(await vscode.debug.startDebugging(vscode.workspace.workspaceFolders[0],{type:'brote',request:'attach',name:'F5 reconnect',sessionId:id}),true);
    await until(()=>events.some(e=>e.session===session.id&&e.event==='stopped'));
   }
   await session.customRequest('terminate');
   await until(async()=>{const s=(await cli('sessions')).find(s=>s.id===id);return !s||s.status==='ended';});
   report.scenarios.push({mode,id,initialBreakpoints:true,initialTracepoints:true,cliSameSession:true,selectedValue:'7',terminated:true});
   await delay(300);
  }
  // Failed build is tested directly through the same packaged CLI to avoid a modal editor error dialog.
  const launchFile=path.join(root,'bad-launch.json');await fs.writeFile(path.join(root,'broken.go'),'package main\nnot Go syntax');
  await fs.writeFile(launchFile,JSON.stringify({configurations:[{type:'go',request:'launch',name:'broken',program:root}]}));
  await assert.rejects(cli('start','--service','--editor-start','--no-ui','--thread','','--project',root,'--launch-file',launchFile,'--config','broken','--build'),/building/);
  assert.equal((await cli('sessions')).filter(s=>!['ended','offline'].includes(s.status)).length,0);
  report.failedBuildNoLiveSession=true;report.restartSameSessionNewRun=true;report.disconnectPreservesProcess=true;
  await fs.writeFile(path.join(root,'result.json'),JSON.stringify(report,null,2));
 }catch(error){await fs.writeFile(path.join(root,'failure.json'),JSON.stringify({error:String(error),stack:error.stack,events},null,2));throw error;}
 finally{for(const s of sessions)try{await vscode.debug.stopDebugging(s);}catch{}vscode.debug.removeBreakpoints([point]);for(const h of handles)h.dispose();}
};

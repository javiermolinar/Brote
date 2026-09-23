const vscode=require('vscode'),assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path'),{execFile}=require('node:child_process');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
exports.run=async()=>{
 const root=process.env.BROTE_HOST_TEST_DIR,id=process.env.BROTE_HOST_SESSION,extension=vscode.extensions.getExtension('brote-local.brote');await extension.activate();
 const core=path.join(extension.extensionPath,'runtime/brote'),commands=[],events=[],handles=[];let session,ended=false;
 const invoke=(...args)=>new Promise((resolve,reject)=>{commands.push(args);execFile(core,args,{timeout:25000,maxBuffer:4*1024*1024},(err,out,stderr)=>err?reject(Error(stderr)):resolve(JSON.parse(out)));});
 // Exit and the editor's automatic disconnect can change generation between a
 // read's discovery and request. Refresh read-only calls; never replay actions.
 const cli=async(...args)=>{for(let attempt=0;;attempt++){try{return await invoke(...args);}catch(error){if(attempt>=4||!['state','captures'].includes(args[0])||!String(error).includes('stale_revision'))throw error;await delay(100);}}};
 const tool=async input=>JSON.parse((await vscode.lm.invokeTool('brote_tracepoints',{input},new vscode.CancellationTokenSource().token)).content.map(c=>c.value).join(''));
 const wait=async predicate=>{for(let i=0;i<150;i++){const value=await predicate();if(value)return value;await delay(100);}throw Error('Shared OTLP host timeout');};
 handles.push(vscode.debug.registerDebugAdapterTrackerFactory('brote',{createDebugAdapterTracker(s){session=s;return{onDidSendMessage:m=>events.push(m)};}}));
 try{
  const first=await cli('tracepoint','add',id,'--file',path.join(root,'main.go'),'--line','4','--name','CLI entry','--values','{"input":"value"}','--capture-limit','10');
  const cliID=first.definitions.items.find(d=>d.kind==='tracepoint').id;
  const ordinary=new vscode.SourceBreakpoint(new vscode.Location(vscode.Uri.file(path.join(root,'main.go')),new vscode.Position(4,0)));vscode.debug.addBreakpoints([ordinary]);
  await vscode.debug.startDebugging(vscode.workspace.workspaceFolders[0],{type:'brote',name:'Shared trace workflow',request:'attach',sessionId:id});
  await wait(()=>events.find(e=>e.type==='response'&&e.command==='setBreakpoints'&&e.body?.breakpoints?.some(b=>b.verified)));
  await cli('continue',id,'--human','--wait','10s');
  const paused=await cli('state',id);assert.equal(paused.status,'paused');assert.equal(paused.source.line,5);
  let captures=(await cli('captures',id)).captures;assert.equal(captures.length,1);assert.equal(captures[0].values.input.value,'7');
  await wait(async()=>(await tool({action:'list'})).some(p=>p.id===cliID));
  const uiPoint=await tool({action:'add',file:path.join(root,'main.go'),line:4,name:'UI entry',values:{input:'value'},hitLimit:10});
  assert.ok((await cli('tracepoint','list',id)).definitions.items.some(d=>d.id===uiPoint.id));
  await cli('tracepoint','update',id,'--id',cliID,'--revision','1','--name','CLI refined','--values','{"input":"value","next":"value+1"}');
  await cli('continue',id,'--human','--wait','10s');
  const second=await cli('state',id);assert.equal(second.source.line,5);assert.equal(second.status,'paused');
  captures=(await cli('captures',id)).captures;assert.equal(captures.length,3);assert.ok(captures.some(c=>c.name==='CLI refined'&&c.values.input.value==='8'&&c.values.next.value==='9'));
  vscode.debug.removeBreakpoints([ordinary]);
  await wait(async()=>!(await cli('breakpoint','list',id)).definitions.items.some(d=>d.owner==='editor'));
  await cli('continue',id,'--human','--wait','10s');
  assert.equal((await cli('state',id)).status,'exited');
  captures=(await cli('captures',id)).captures;assert.equal(captures.length,5);assert.ok(captures.every(c=>c.status==='captured'));
  const program=captures[0].programTraceId,debuggerID=captures[0].debuggerTraceId;assert.ok(program&&debuggerID&&program!==debuggerID);
  await cli('end-session',id,'--confirmed');ended=true;
  const query=process.env.BROTE_TEMPO_QUERY_URL||'http://127.0.0.1:3200';const exported=[];
  const attrs=span=>Object.fromEntries(span.attributes.map(a=>[a.key,a.value.stringValue??a.value.intValue]));
  for(const traceID of [program,debuggerID])await wait(async()=>{
   const response=await fetch(`${query}/api/traces/${traceID}`,{headers:{Accept:'application/json'}});if(!response.ok)return false;
   const trace=await response.json();const spans=(trace.batches||trace.resourceSpans||[]).flatMap(b=>(b.scopeSpans||b.instrumentationLibrarySpans||[]).flatMap(s=>s.spans||[]));
   if(!spans.some(s=>s.name==='debugger.session'||attrs(s)['program.span.type']==='run'))return false;
   exported.push(...spans);return true;
  });
  const snapshots=exported.filter(s=>attrs(s)['program.span.type']==='snapshot');assert.equal(snapshots.length,5,'Only service captures are exported');
  assert.deepEqual(snapshots.map(s=>attrs(s)['program.value.input']).sort(),['7','8','8','9','9']);
  for(const span of snapshots){assert.equal(span.startTimeUnixNano,span.endTimeUnixNano);const parent=exported.find(p=>p.spanId===span.parentSpanId);assert.equal(attrs(parent)['program.span.type'],'thread');assert.ok(exported.some(p=>p.spanId===parent.parentSpanId&&attrs(p)['program.span.type']==='run'));}
  await fs.writeFile(path.join(root,'result.json'),JSON.stringify({vscode:vscode.version,extension:extension.packageJSON.version,session:id,programTraceId:program,debuggerTraceId:debuggerID,snapshots:5,values:['7','8','8','9','9'],ordinaryPausePreserved:true,commands,exported},null,2));
 }catch(error){await fs.writeFile(path.join(root,'failure.json'),JSON.stringify({error:String(error),stack:error.stack,commands,events},null,2));throw error;}
 finally{if(session)await vscode.debug.stopDebugging(session);for(const h of handles)h.dispose();if(!ended)await cli('end-session',id,'--confirmed');}
};

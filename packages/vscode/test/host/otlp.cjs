// VS Code --extensionTestsPath entry: real Go adapter and packaged Brote.
const vscode=require('vscode'),assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
exports.run=async()=>{
 const root=process.env.BROTE_HOST_TEST_DIR;if(!root)throw Error('BROTE_HOST_TEST_DIR must identify an isolated project.');
 const events=[],requests=[],handles=[];let session;
 const awaitEvent=async(name,count=1)=>{for(let i=0;i<200;i++){const matches=events.filter(e=>e.event===name);if(matches.length>=count)return matches.at(-1);await delay(100);}throw Error(`Timed out waiting for ${name}`);};
 try{
  await vscode.extensions.getExtension('golang.go').activate();
  const brote=vscode.extensions.getExtension('brote-local.brote');assert.ok(brote);await brote.activate();
  handles.push(vscode.debug.registerDebugAdapterTrackerFactory('go',{createDebugAdapterTracker(s){session=s;return {onWillReceiveMessage(m){if(m.type==='request')requests.push({command:m.command,seq:m.seq});},onDidSendMessage(m){if(m.type==='event' || (m.type==='response' && m.command==='setBreakpoints'))events.push(m);}};}}));
  const file=vscode.Uri.file(path.join(root,'main.go'));await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(file));
  vscode.debug.addBreakpoints([new vscode.SourceBreakpoint(new vscode.Location(file,new vscode.Position(4,0)))]);
  assert.equal(await vscode.debug.startDebugging(vscode.workspace.workspaceFolders[0],{type:'go',name:'Brote native host OTLP',request:'launch',mode:'exec',program:path.join(root,'demo'),cwd:root,dlvToolPath:process.env.BROTE_TEST_DLV,showGlobalVariables:false}),true);
  const stopped=await awaitEvent('stopped');assert.equal(stopped.body.reason,'breakpoint');
  const result=await vscode.lm.invokeTool('brote_inspect',{input:{}},new vscode.CancellationTokenSource().token);
  const evidence=JSON.parse(result.content.map(c=>c.value).join(''));assert.equal(evidence.frame.name,'main.work');
  assert.ok(evidence.scopes.some(s=>s.variables?.some(v=>v.name==='total' && v.value==='42')),JSON.stringify(evidence));
  await vscode.commands.executeCommand('brote.ask');
  await session.customRequest('next',{threadId:stopped.body.threadId});await awaitEvent('stopped',2);
  await session.customRequest('continue',{threadId:stopped.body.threadId});await awaitEvent('terminated');await delay(1000);
  const query=process.env.BROTE_TEMPO_QUERY_URL || 'http://127.0.0.1:3200';let traces=[];
  for(let i=0;i<60;i++){const q=`{ span.debugger.session.id = "${session.id}" }`;const response=await fetch(`${query}/api/search?q=${encodeURIComponent(q)}&limit=20`);if(response.ok){traces=(await response.json()).traces||[];if(traces.length>=2)break;}await delay(500);}
  assert.equal(traces.length,2,`Expected two traces for ${session.id}`);const exported=[];
  for(const t of traces){const response=await fetch(`${query}/api/traces/${t.traceID}`,{headers:{Accept:'application/json'}});assert.equal(response.status,200);exported.push({id:t.traceID,data:await response.json()});}
  const spans=exported.flatMap(t=>(t.data.batches||t.data.resourceSpans||[]).flatMap(b=>(b.scopeSpans||b.instrumentationLibrarySpans||[]).flatMap(s=>s.spans||[])));
  const attributes=s=>Object.fromEntries(s.attributes.map(a=>[a.key,a.value.stringValue ?? a.value.intValue]));
  assert.equal(spans.filter(s=>s.name==='debugger.session').length,1);assert.equal(spans.filter(s=>s.name==='run Brote native host OTLP').length,1);
  assert.ok(spans.some(s=>s.name==='next' && attributes(s)['debugger.outcome']==='stopped'));
  assert.ok(spans.some(s=>s.name==='continue' && ['exited','stopped'].includes(attributes(s)['debugger.outcome'])));
  const snapshots=spans.filter(s=>attributes(s)['program.span.type']==='snapshot');assert.ok(snapshots.length>=1);assert.ok(snapshots.some(s=>attributes(s)['program.value.total']==='42'));
  for(const s of snapshots){assert.ok(spans.some(parent=>parent.spanId===s.parentSpanId));JSON.parse(attributes(s)['program.snapshot.json']);}
  await fs.writeFile(path.join(root,'result.json'),JSON.stringify({vscode:vscode.version,extensionVersion:brote.packageJSON.version,session:session.id,traceIDs:exported.map(t=>t.id),spanCount:spans.length,snapshots:snapshots.length,value:'42',requests,events,exported},null,2));
 }catch(error){await fs.writeFile(path.join(root,'failure.json'),JSON.stringify({error:String(error),stack:error.stack,events,requests},null,2));throw error;}
 finally{if(session)await vscode.debug.stopDebugging(session);for(const handle of handles)handle.dispose();}
};

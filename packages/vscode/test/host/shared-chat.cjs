const vscode=require('vscode'),assert=require('node:assert/strict'),fs=require('node:fs/promises'),path=require('node:path'),{execFile}=require('node:child_process');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
exports.run=async()=>{
 const root=process.env.BROTE_HOST_TEST_DIR,id=process.env.BROTE_HOST_SESSION;
 const extension=vscode.extensions.getExtension('brote-local.brote');await extension.activate();
 const core=path.join(extension.extensionPath,'runtime/brote');
 const cli=(...args)=>new Promise((resolve,reject)=>execFile(core,args,{timeout:30000,maxBuffer:36*1024*1024},(err,out,stderr)=>err?reject(Error(stderr)):resolve(JSON.parse(out))));
 const handles=[],events=[];let session,providerStarted=false;
 const until=async fn=>{for(let i=0;i<200;i++){const value=await fn();if(value)return value;await delay(100);}throw Error('Packaged Chat host timeout');};
 handles.push(vscode.lm.registerLanguageModelChatProvider('brote-fixture',{
  provideLanguageModelChatInformation:()=>[{id:'deterministic',name:'Brote Test Model',family:'brote-test',version:'1',maxInputTokens:200000,maxOutputTokens:10000,capabilities:{toolCalling:false}}],
  provideTokenCount:async()=>1,
  async provideLanguageModelChatResponse(_model,messages,_options,progress,token){
   const text=JSON.stringify(messages);progress.report(new vscode.LanguageModelTextPart('Saved evidence answer. '));
   if(text.includes('CANCEL_PACKAGED')){providerStarted=true;await new Promise(resolve=>{const timer=setTimeout(resolve,15000);const link=token.onCancellationRequested(()=>{clearTimeout(timer);link.dispose();resolve();});});if(token.isCancellationRequested)throw Error('Fixture cancelled');}
   else progress.report(new vscode.LanguageModelTextPart('Complete.'));
  }
 }));
 handles.push(vscode.debug.registerDebugAdapterTrackerFactory('brote',{createDebugAdapterTracker(s){session=s;return{onDidSendMessage:m=>events.push(m)};}}));
 const report={vscode:vscode.version,session:id,model:'registered deterministic fixture',packageSourceUnmodified:true};
 try{
  await cli('breakpoint','add',id,'--file',path.join(root,'main.go'),'--line','5');
  assert.equal(await vscode.debug.startDebugging(vscode.workspace.workspaceFolders[0],{type:'brote',name:'Packaged Chat',request:'attach',sessionId:id}),true);
  await until(()=>events.some(e=>e.event==='stopped'));const count=events.filter(e=>e.event==='stopped').length;
  await session.customRequest('continue',{threadId:1});await until(()=>events.filter(e=>e.event==='stopped').length>count);
  const state=await cli('state',id);await session.customRequest('stackTrace',{threadId:state.goroutine,levels:20});
  await vscode.window.showTextDocument(vscode.Uri.file(path.join(root,'main.go')),{selection:new vscode.Range(4,0,4,0)});
  const models=await vscode.lm.selectChatModels({vendor:'brote-fixture'});assert.equal(models.length,1);
  const question='PACKAGED_CHAT_'+Date.now();
  await vscode.commands.executeCommand('workbench.action.chat.open',{query:'@brote '+question,isPartialQuery:true,mode:'ask'});
  await vscode.commands.executeCommand('workbench.action.chat.changeModel',{vendor:'brote-fixture',id:'deterministic',family:'brote-test'});
  await vscode.commands.executeCommand('workbench.action.chat.submit',{inputValue:'@brote '+question});
  const first=await until(async()=>{const d=await cli('comment','list',id);return d.threads.find(t=>t.messages.some(m=>m.body===question)&&t.delivery.status==='answered');});
  const follow=vscode.commands.executeCommand('workbench.action.chat.submit',{inputValue:'@brote FOLLOWUP_'+question});await delay(600);await vscode.commands.executeCommand('workbench.action.acceptSelectedQuickOpenItem');await follow;
  await until(async()=>{const d=await cli('comment','list',id);return d.threads.find(t=>t.id===first.id)?.messages.length===4;});
  await cli('end-session',id,'--confirmed');
  const cancel=vscode.commands.executeCommand('workbench.action.chat.submit',{inputValue:'@brote CANCEL_PACKAGED_'+question});await delay(600);await vscode.commands.executeCommand('workbench.action.acceptSelectedQuickOpenItem');await until(()=>providerStarted);await vscode.commands.executeCommand('workbench.action.chat.cancel');await cancel;
  const saved=await until(async()=>{const d=await cli('comment','list',id);const t=d.threads.find(t=>t.id===first.id);return t?.delivery.status==='failed'&&t.messages.length===5?t:undefined;});
  assert.equal(saved.messages[0].evidence.id,first.messages[0].evidence.id);assert.equal((await cli('sessions')).filter(s=>!['ended','offline'].includes(s.status)).length,0);
  Object.assign(report,{actualChatWorkbench:true,sameThreadFollowup:true,historicalCancellationNoPartial:true,evidencePreserved:true,noDebuggerRevival:true,thread:first.id});
  await fs.writeFile(path.join(root,'result.json'),JSON.stringify(report,null,2));
 }catch(error){await fs.writeFile(path.join(root,'failure.json'),JSON.stringify({error:String(error),stack:error.stack,events,report},null,2));throw error;}
 finally{if(session)try{await vscode.debug.stopDebugging(session);}catch{}for(const h of handles)h.dispose();}
};

const fs=require('node:fs/promises'),os=require('node:os'),path=require('node:path'),{spawn,execFileSync}=require('node:child_process');
const root=path.resolve(__dirname,'../../../..');
async function main(){
 const code=process.env.BROTE_CODE_BIN||'/Applications/Visual Studio Code.app/Contents/MacOS/Code';
 const work=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'brote-shared-'))),project=path.join(work,'project');
 await fs.mkdir(project,{recursive:true});
 await fs.writeFile(path.join(project,'go.mod'),'module sharedhost\n\ngo 1.23\n');
 await fs.writeFile(path.join(project,'main_test.go'),'package main\nimport "testing"\nfunc TestChosen(t *testing.T){value:=7; if work(value)!=8 {t.Fatal("wrong")}}\nfunc TestExcluded(t *testing.T){t.Fatal("must not run")}\n');
 await fs.writeFile(path.join(project,'main.go'),'package main\nimport "fmt"\nfunc work(value int) int {\n total:=value+1\n return total\n}\nfunc main(){for _,value:=range []int{7,8,9}{fmt.Println(work(value))}}\n');
 execFileSync('go',['build','-gcflags=all=-N -l','-o',path.join(project,'demo'),'.'],{cwd:project,stdio:'inherit'});
 const vsix=process.env.BROTE_VSIX||path.join(root,`dist/releases/brote-0.5.0-${process.platform}-${process.arch}.vsix`);
 execFileSync('unzip',['-q',vsix,'-d',path.join(work,'vsix')]);
 const extension=path.join(work,'vsix/extension'),core=path.join(extension,'runtime/brote');
 await fs.copyFile(path.join(__dirname,process.env.BROTE_HOST_SCENARIO==='f5'?'shared-f5.cjs':process.env.BROTE_HOST_SCENARIO==='otlp'?'shared-otlp.cjs':process.env.BROTE_HOST_SCENARIO==='chat'?'shared-chat.cjs':'shared.cjs'),path.join(extension,'host-test.cjs'));
 if(process.env.BROTE_HOST_SCENARIO==='chat'){const manifest=JSON.parse(await fs.readFile(path.join(extension,'package.json'),'utf8'));manifest.contributes.languageModelChatProviders=[{vendor:'brote-fixture',displayName:'Brote Test Model'}];await fs.writeFile(path.join(extension,'package.json'),JSON.stringify(manifest));}
 const env={...process.env,DEBUG_HANDOVER_HOME:path.join(work,'sessions'),AGENTDEBUGGER_DATA_DIR:path.join(work,'data'),BROTE_HOST_TEST_DIR:project};
 if(process.env.BROTE_HOST_SCENARIO==='otlp')env.OTEL_EXPORTER_OTLP_ENDPOINT ||= 'http://127.0.0.1:4318';else for(const key of Object.keys(env))if(key.startsWith('OTEL_EXPORTER_OTLP'))delete env[key];delete env.ELECTRON_RUN_AS_NODE;
 const cli=(...args)=>JSON.parse(execFileSync(core,args,{env,encoding:'utf8',timeout:45000}));
 const session=process.env.BROTE_HOST_SCENARIO==='f5'?{id:''}:cli('start','--service','--binary',path.join(project,'demo'),'--project',project,'--thread','');env.BROTE_HOST_SESSION=session.id;
 const log=await fs.open(path.join(work,'host.log'),'w');
 let success=false;
 try{
  await new Promise((resolve,reject)=>{
   const child=spawn(code,['--new-window','--skip-welcome','--skip-release-notes','--disable-workspace-trust','--user-data-dir',path.join(work,'user'),'--extensions-dir',path.join(work,'extensions'),`--extensionDevelopmentPath=${extension}`,`--extensionTestsPath=${path.join(extension,'host-test.cjs')}`,project],{env,stdio:['ignore',log.fd,log.fd]});
   const timer=setTimeout(()=>{child.kill();reject(Error('Shared host test timed out'));},120000);
   child.once('error',err=>{clearTimeout(timer);reject(err);});child.once('exit',code=>{clearTimeout(timer);code===0?resolve():reject(Error(`VS Code exited ${code}`));});
  });
  const result=JSON.parse(await fs.readFile(path.join(project,'result.json'),'utf8'));
  await fs.writeFile(path.join(root,process.env.BROTE_HOST_SCENARIO==='f5'?'dist/shared-f5-host-result.json':process.env.BROTE_HOST_SCENARIO==='otlp'?'dist/shared-otlp-host-result.json':process.env.BROTE_HOST_SCENARIO==='chat'?'dist/shared-chat-host-result.json':'dist/shared-host-result.json'),JSON.stringify(result,null,2));console.log(JSON.stringify({vscode:result.vscode,session:result.session,programTraceId:result.programTraceId,debuggerTraceId:result.debuggerTraceId,snapshots:result.snapshots,values:result.values,ordinaryPausePreserved:result.ordinaryPausePreserved,disconnectPreservesProcess:result.disconnectPreservesProcess}));success=true;
 }finally{try{for(const s of cli('sessions'))if(s.status!=='ended')try{cli('end-session',s.id,'--confirmed');}catch{}}finally{await log.close();if(success)await fs.rm(work,{recursive:true,force:true});else console.error(`Evidence retained at ${work}`);}}
}
main().catch(err=>{console.error(err);process.exitCode=1;});

const fs=require('node:fs/promises');
const os=require('node:os');
const path=require('node:path');
const {spawn,execFileSync}=require('node:child_process');
const root=path.resolve(__dirname,'../../../..');
async function main(){
 const code=process.env.BROTE_CODE_BIN || (process.platform==='darwin'?'/Applications/Visual Studio Code.app/Contents/MacOS/Code':undefined);
 const goExtension=process.env.BROTE_GO_EXTENSION;
 if(!code || !goExtension)throw Error('Set BROTE_CODE_BIN to the VS Code executable and BROTE_GO_EXTENSION to an installed golang.go extension directory.');
 // Keep the profile path short: deep user-data paths can exceed native socket limits.
 const work=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'brote-host-')));
 const project=path.join(work,'project'),user=path.join(work,'user'),extensions=path.join(work,'extensions');
 await fs.mkdir(path.join(project,'.vscode'),{recursive:true});
 await fs.mkdir(path.join(user,'User'),{recursive:true});
 const settings={'go.useLanguageServer':false,'go.toolsManagement.autoUpdate':false,'go.toolsManagement.checkForUpdates':'off','extensions.autoUpdate':false,'update.mode':'none','telemetry.telemetryLevel':'off','brote.capturePoints':{'main.work':{name:'work.result',values:{total:'total'}}}};
 await fs.writeFile(path.join(user,'User/settings.json'),JSON.stringify(settings));
 await fs.writeFile(path.join(project,'.vscode/settings.json'),JSON.stringify(settings));
 await fs.writeFile(path.join(project,'main.go'),'package main\nimport "fmt"\nfunc work(value int) int {\n total := value + 1\n return total\n}\nfunc main() { fmt.Println(work(41)) }\n');
 await fs.writeFile(path.join(project,'go.mod'),'module nativehosttest\n\ngo 1.23\n');
 await fs.cp(goExtension,path.join(extensions,'golang.go'),{recursive:true});
 execFileSync('go',['build','-gcflags=all=-N -l','-o',path.join(project,'demo'),'.'],{cwd:project,stdio:'inherit'});
 execFileSync('unzip',['-q',process.env.BROTE_TEST_VSIX || path.join(root,'dist/brote.vsix'),'-d',path.join(work,'vsix')]);
 // Put the host test under the development extension so VS Code attributes its API calls correctly.
 const extension=path.join(work,'vsix/extension');
 await fs.copyFile(path.join(__dirname,'otlp.cjs'),path.join(extension,'host-test.cjs'));
 const log=await fs.open(path.join(work,'host.log'),'w');
 const env={...process.env,BROTE_HOST_TEST_DIR:project,AGENTDEBUGGER_DATA_DIR:path.join(work,'data')};
 for(const key of Object.keys(env))if(key.startsWith('OTEL_EXPORTER_'))delete env[key];
 delete env.ELECTRON_RUN_AS_NODE;
 const args=['--new-window','--skip-welcome','--skip-release-notes','--disable-workspace-trust','--user-data-dir',user,'--extensions-dir',extensions,`--extensionDevelopmentPath=${extension}`,`--extensionTestsPath=${path.join(extension,'host-test.cjs')}`,project];
 try{
  await new Promise((resolve,reject)=>{
   const child=spawn(code,args,{env,stdio:['ignore',log.fd,log.fd]});
   const timer=setTimeout(()=>{child.kill();reject(Error('VS Code host test timed out'));},120000);
   child.once('error',error=>{clearTimeout(timer);reject(error);});
   child.once('exit',code=>{clearTimeout(timer);code===0?resolve():reject(Error(`VS Code exited ${code}`));});
  });
  const result=JSON.parse(await fs.readFile(path.join(project,'result.json'),'utf8'));
  const output=process.env.BROTE_HOST_RESULT || path.join(root,'dist/vscode-host-result.json');
  await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(output,JSON.stringify(result,null,2));
  console.log(JSON.stringify({result:output,traceIDs:result.traceIDs,spans:result.spanCount,snapshots:result.snapshots,value:result.value}));
  await log.close();
  const pid=Number(await fs.readFile(path.join(work,'data/tracing/service.pid'),'utf8'));try{process.kill(pid,'SIGTERM');}catch{}
  // Retain data until the core service has flushed Tempo.
  for(let i=0;i<200;i++){try{process.kill(pid,0);}catch{break;}await new Promise(r=>setTimeout(r,100));}
  await fs.rm(work,{recursive:true,force:true});
 }catch(error){await log.close();console.error(`Host test evidence retained at ${work}`);throw error;}
}
main().catch(error=>{console.error(error);process.exitCode=1;});

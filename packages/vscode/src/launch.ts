import * as vscode from 'vscode';
import * as path from 'node:path';
import * as os from 'node:os';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import type {ServiceClient} from './service';

// Delegate builds and target creation to the same CLI used outside the editor.
export async function launchService(client:ServiceClient,folder:vscode.WorkspaceFolder|undefined,config:vscode.DebugConfiguration,token?:vscode.CancellationToken){
 const allowed=new Set(['type','request','name','mode','program','args','cwd','env','envFile','buildFlags','dlvToolPath','stopOnEntry','console','substitutePath','presentation','internalConsoleOptions','preLaunchTask','postDebugTask','tracepoints','__configurationTarget','__sessionId','__restart']);
 const unsupported=Object.keys(config).filter(k=>!allowed.has(k));
 if(unsupported.length)throw new Error(`Unsupported Brote launch options: ${unsupported.join(', ')}`);
 if(!config.program||typeof config.program!=='string')throw new Error('Brote launch requires program.');
 const root=folder?.uri.fsPath || config.cwd;
 if(!root||!path.isAbsolute(root))throw new Error('Open a workspace or provide an absolute cwd.');
 const profile:Record<string,unknown>={type:'go',request:'launch',name:'Brote F5'};
 for(const key of ['mode','program','args','cwd','env','envFile','buildFlags','dlvToolPath','stopOnEntry','console','substitutePath'])if(config[key]!==undefined)profile[key]=config[key];
 if(config.tracepoints!==undefined&&(!Array.isArray(config.tracepoints)||config.tracepoints.length>64))throw new Error('tracepoints must contain at most 64 source points.');
 if(token?.isCancellationRequested)throw new Error('Brote launch cancelled.');
 const dir=await mkdtemp(path.join(os.tmpdir(),'brote-launch-'));let id:string|undefined;
 try{
  const file=path.join(dir,'launch.json');await writeFile(file,JSON.stringify({configurations:[profile]}),{mode:0o600});
  const result=await client.run<{id:string}>(['start','--service','--editor-start','--no-ui','--thread','','--project',root,'--launch-file',file,'--config','Brote F5',...(config.mode==='exec'?[]:['--build'])],180000,token);
  id=result.id;
  if(token?.isCancellationRequested)throw new Error('Brote launch cancelled.');
  await client.verify(id);
  for(const point of config.tracepoints||[]){
   const keys=['file','line','name','values','condition','hitCondition','captureLimit'];
   if(!point||typeof point!=='object'||Object.keys(point).some(k=>!keys.includes(k))||typeof point.file!=='string'||!Number.isInteger(point.line))throw new Error('Invalid initial tracepoint.');
   const args=['tracepoint','add',id,'--client','vscode-launch','--file',path.resolve(root,point.file),'--line',String(point.line)];
   for(const [key,flag] of [['name','--name'],['condition','--condition'],['hitCondition','--hit-condition'],['captureLimit','--capture-limit']])if(point[key]!==undefined)args.push(flag,String(point[key]));
   if(point.values!==undefined)args.push('--values',JSON.stringify(point.values));
   await client.run(args);
  }
  if(token?.isCancellationRequested)throw new Error('Brote launch cancelled.');
  return id;
 }catch(error){if(id)await client.run(['end-session',id,'--confirmed']).catch(()=>{});throw error;}
 finally{await rm(dir,{recursive:true,force:true});}
}

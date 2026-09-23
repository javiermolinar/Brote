import * as vscode from 'vscode';
import {launchService} from './launch';
import * as path from 'node:path';
import { existsSync } from 'node:fs';
import { promises as fs } from 'node:fs';
import * as os from 'node:os';
import { execFile } from 'node:child_process';

export interface Definition {id:string;revision:number;owner:string;kind:'breakpoint'|'tracepoint';enabled:boolean;location:{file?:string;line?:number;function?:string};condition?:string;hitCondition?:string;name?:string;values?:Record<string,string>;captureLimit?:number}
export interface Capture {id:string;definitionId:string;status:string;exportStatus:string;sequence:number;goroutine:number;programTraceId?:string;debuggerTraceId?:string;error?:{message:string}}
export interface ServiceState {id:string;run:string;serviceVersion:number;status:string;generation:number;pauseEpoch:number;project:string;goroutine?:number;frame?:number;cursor:number;inspectionError?:string;truncated?:boolean;stackError?:string;frameOffset?:number;exception?:unknown;exceptionError?:string;frames?:{file:string;line:number;function?:{name:string};Locals?:unknown[];Arguments?:unknown[];localsError?:string;argumentsError?:string;localsTruncated?:boolean;argumentsTruncated?:boolean;truncated?:boolean}[];definitions?:{revision:number;items:Definition[]};resolutions?:{definitionId:string;verified:boolean;message?:string}[];lastCapture?:Capture;captureCounts?:Record<string,number>;traceIds?:{programTraceId:string;debuggerTraceId:string};exportError?:string}
interface Summary {id:string;run?:string;serviceVersion?:number;status:string;project:string;binary:string}
export class ServiceClient {
 constructor(readonly executable:string){}
 async run<T>(args:string[],timeout=45000,token?:vscode.CancellationToken):Promise<T>{
  if(token?.isCancellationRequested)throw new Error('Brote command cancelled.');
  return new Promise((resolve,reject)=>{
   let cancellation:vscode.Disposable|undefined,finished=false;
   const child=execFile(this.executable,args,{timeout,maxBuffer:36*1024*1024,windowsHide:true},(error,stdout,stderr)=>{
    finished=true;cancellation?.dispose();
    if(error){reject(new Error(token?.isCancellationRequested?'Brote command cancelled.':stderr.trim() || `Brote command failed: ${args[0]}`));return;}
    try{resolve(JSON.parse(stdout) as T);}catch{reject(new Error('Brote returned invalid JSON.'));}
   });
   if(!finished&&token){
    cancellation=token.onCancellationRequested(()=>child.kill('SIGTERM'));
    if(token.isCancellationRequested)child.kill('SIGTERM');
   }
  });
 }

 async withBody<T>(args:string[],body:string,token?:vscode.CancellationToken):Promise<T>{
  const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-input-'));
  try{const file=path.join(dir,'body');await fs.writeFile(file,body,{mode:0o600});return await this.run<T>([...args,'--body-file',file],45000,token);}
  finally{await fs.rm(dir,{recursive:true,force:true});}
 }
 state(id:string,goroutine?:number,frame?:number){return this.run<ServiceState>(['state',id,...(goroutine?['--goroutine',String(goroutine)]:[]),...(frame!==undefined?['--frame',String(frame)]:[])]);}
 async select():Promise<string|undefined>{
  const summaries=await this.run<Summary[]>(['sessions']);
  const choices=summaries.filter(s=>s.serviceVersion===1&&!['offline','ended'].includes(s.status)).map(s=>({label:path.basename(s.binary),description:`${s.id} · ${s.project}`,id:s.id}));
  if(!choices.length)throw new Error('No running Brote service sessions. Start one with brote start --service.');
  return (await vscode.window.showQuickPick(choices,{title:'Attach to a Brote session'}))?.id;
 }
 async verify(id:string){
  if(!/^[a-zA-Z0-9_-]{1,128}$/.test(id))throw new Error('Invalid Brote session ID.');
  const state=await this.run<ServiceState>(['state',id,'--summary']);
  if(state.id!==id||state.serviceVersion!==1||!state.run)throw new Error('This session does not support the shared Brote service.');
  return state;
 }
}
export function registerService(context:vscode.ExtensionContext){
 const configured=vscode.workspace.getConfiguration('brote').get<string>('runtimePath');
 const bundled=path.join(context.extensionPath,'runtime',process.platform==='win32'?'brote.exe':'brote');
 const client=new ServiceClient(configured || (existsSync(bundled)?bundled:'brote'));
 context.subscriptions.push(vscode.debug.registerDebugAdapterDescriptorFactory('brote',{
  async createDebugAdapterDescriptor(session){
   const id=String(session.configuration.sessionId||'');await client.verify(id);
   return new vscode.DebugAdapterExecutable(client.executable,['dap',id]);
  }
 }),vscode.debug.registerDebugConfigurationProvider('brote',{
  async resolveDebugConfiguration(_folder,config){
   if(!vscode.workspace.isTrusted)throw new Error('Trust this workspace before attaching a debugger.');
   if(config.request!=='attach'&&config.request!=='launch')throw new Error('Use a Brote launch or attach configuration.');
   return config;
  },
  async resolveDebugConfigurationWithSubstitutedVariables(folder,config,token){
   if(!vscode.workspace.isTrusted)throw new Error('Trust this workspace before launching a debugger.');
   if(config.request==='launch'){
    config.sessionId=await launchService(client,folder,config,token);
   }else{
    config.sessionId ||= await client.select();if(!config.sessionId)return;
    await client.verify(config.sessionId);
   }
   return config;
  }
 }),vscode.commands.registerCommand('brote.attachSession',async()=>{
  try{const id=await client.select();if(id)await vscode.debug.startDebugging(vscode.workspace.workspaceFolders?.[0],{type:'brote',name:`Brote ${id}`,request:'attach',sessionId:id});}catch(error){void vscode.window.showErrorMessage(String(error));}
 }));
 return client;
}

import * as vscode from 'vscode';
import * as path from 'node:path';
import { randomUUID } from 'node:crypto';
import { ServiceClient, Definition, Capture, ServiceState } from './service';
interface Entry extends Definition {hits:number;status:string}
interface Input {action:string;id?:string;file?:string;line?:number;name?:string;condition?:string;hitCondition?:string;values?:Record<string,string>;hitLimit?:number;enabled?:boolean}

/** This view owns presentation only. The service owns definitions and execution. */
export function serviceTracepoints(context:vscode.ExtensionContext,client:ServiceClient,migration:Promise<unknown>=Promise.resolve()){
 let entries:Entry[]=[],state:ServiceState|undefined,busy=false,disposed=false;
 const changed=new vscode.EventEmitter<void>();
 const decoration=vscode.window.createTextEditorDecorationType({gutterIconPath:vscode.Uri.file(path.join(context.extensionPath,'assets','tracepoint.svg')),gutterIconSize:'contain'});
 const active=()=>{const s=vscode.debug.activeDebugSession;if(s?.type!=='brote'||!s.configuration.sessionId)throw Error('Attach to a Brote session to use tracepoints.');return String(s.configuration.sessionId);};
 const trusted=()=>{if(!vscode.workspace.isTrusted)throw Error('Trust this workspace before configuring tracepoints.');};
 function decorate(){for(const editor of vscode.window.visibleTextEditors)editor.setDecorations(decoration,entries.filter(p=>p.location.file===editor.document.uri.fsPath).map(p=>({range:new vscode.Range((p.location.line||1)-1,0,(p.location.line||1)-1,0),hoverMessage:`Brote tracepoint: ${p.name||p.id} · ${p.status}`})));}
 async function migrate(id:string,current:ServiceState){
  await migration;
  const legacy=context.workspaceState.get<{id:string;name:string;values:Record<string,string>;hitLimit:number}[]>('tracepoints',[]);
  if(!legacy.length||current.status!=='paused')return;
  const mapping=context.workspaceState.get<Record<string,string>>('tracepointServiceIDs',{});
  for(const point of legacy){
   const bp=vscode.debug.breakpoints.find(b=>b.id===point.id);if(!(bp instanceof vscode.SourceBreakpoint))continue;
   const stable=mapping[point.id] ||= randomUUID();await context.workspaceState.update('tracepointServiceIDs',mapping);
   if(!current.definitions?.items.some(d=>d.id===stable))await client.run(['tracepoint','add',id,'--client','vscode-ui','--id',stable,'--file',bp.location.uri.fsPath,'--line',String(bp.location.range.start.line+1),'--name',point.name,'--values',JSON.stringify(point.values),'--capture-limit',String(point.hitLimit),'--enabled='+bp.enabled,...(bp.condition?['--condition',bp.condition]:[]),...(bp.hitCondition?['--hit-condition',bp.hitCondition]:[])]);
   vscode.debug.removeBreakpoints([bp]);
   await context.workspaceState.update('tracepoints',context.workspaceState.get<typeof legacy>('tracepoints',[]).filter(p=>p.id!==point.id));
  }
 }
 async function refresh(){
  if(busy||disposed)return;busy=true;
  try{
   const id=active(),fresh=await client.run<ServiceState>(['state',id,'--brief']);
   if(disposed||active()!==id)return;
   await migrate(id,fresh);
   const result=await client.run<{captures:Capture[]}>(['captures',id]);
   if(disposed||active()!==id)return;
   state=fresh;
   entries=(fresh.definitions?.items||[]).filter(d=>d.kind==='tracepoint').map(d=>{
    const capture=[...result.captures].reverse().find(c=>c.definitionId===d.id&&(!('run' in c)||c.run===fresh.run));
    const resolution=fresh.resolutions?.find(r=>r.definitionId===d.id);
    const status=!d.enabled?'Disabled':!resolution?.verified?(resolution?.message||'Unverified'):capture?`${capture.status} · export ${capture.exportStatus}${capture.error?': '+capture.error.message:''}`:fresh.exportError?`Ready · ${fresh.exportError}`:'Ready';
    return {...d,hits:fresh.captureCounts?.[d.id]||0,status};
   });
  }catch(error){if(vscode.debug.activeDebugSession?.type!=='brote'){entries=[];state=undefined;}else entries=entries.map(e=>({...e,status:String(error)}));}
  finally{busy=false;changed.fire();decorate();}
 }
 const provider:vscode.TreeDataProvider<Entry>={onDidChangeTreeData:changed.event,getChildren:()=>entries,getTreeItem(p){
  const item=new vscode.TreeItem(p.name||p.id);item.id=p.id;item.contextValue='brote.tracepoint';item.description=`${p.hits} hits · ${p.status}`;item.iconPath=new vscode.ThemeIcon(p.enabled?'record':'circle-outline');
  item.tooltip=`${p.location.file}:${p.location.line}\nOwner: ${p.owner} · revision ${p.revision}\n${p.status}\nLimit: ${p.captureLimit} per run`;
  if(p.location.file)item.command={command:'vscode.open',title:'Open tracepoint',arguments:[vscode.Uri.file(p.location.file),{selection:new vscode.Range((p.location.line||1)-1,0,(p.location.line||1)-1,0)}]};return item;
 }};
 async function manage(input:Input):Promise<unknown>{
  trusted();const id=active();await refresh();
  if(input.action==='list')return entries;
  const operation=input.action==='remove'?'remove':input.action==='update'?'update':'add';
  if(!['add','update','remove'].includes(input.action))throw Error('Unknown tracepoint action.');
  const previous=input.id?entries.find(p=>p.id===input.id):undefined;
  if(operation!=='add'&&!previous)throw Error('Tracepoint changed; refresh and choose it again.');
  const args=['tracepoint',operation,id,'--client',previous?.owner||'vscode-ui'];
  if(previous)args.push('--id',previous.id,'--revision',String(previous.revision));
  if(operation!=='remove'){
   for(const [key,flag] of [['file','file'],['line','line'],['name','name'],['condition','condition'],['hitCondition','hit-condition'],['hitLimit','capture-limit']] as const)if(input[key]!==undefined)args.push('--'+flag,String(input[key]));
   if(input.values!==undefined)args.push('--values',JSON.stringify(input.values));
   if(input.enabled!==undefined)args.push('--enabled='+input.enabled);
  }
  const result=await client.run<{definitions?:{items:Definition[]}}>(args);await refresh();
  if(operation==='remove')return {removed:input.id};
  const point=previous?result.definitions?.items.find(d=>d.id===previous.id):result.definitions?.items.at(-1);
  return point?{...point,file:point.location.file,line:point.location.line,hitLimit:point.captureLimit}:result;
 }
 async function choose(point?:Entry){return point?entries.find(p=>p.id===point.id):(await vscode.window.showQuickPick(entries.map(p=>({label:p.name||p.id,description:p.status,point:p})),{title:'Choose a tracepoint'}))?.point;}
 const commands:Record<string,(point?:Entry)=>Promise<unknown>>={
  'brote.addTracepoint':async()=>{const editor=vscode.window.activeTextEditor;if(!editor)throw Error('Select a Go source line.');const line=editor.selection.active.line+1;const name=await vscode.window.showInputBox({title:'Capture name',value:`${path.basename(editor.document.uri.fsPath)}:${line}`});if(name!==undefined)return manage({action:'add',file:editor.document.uri.fsPath,line,name,values:{},hitLimit:100});},
  'brote.removeTracepoint':async p=>{const point=await choose(p);if(point)return manage({action:'remove',id:point.id});},
  'brote.toggleTracepoint':async p=>{const point=await choose(p);if(point)return manage({action:'update',id:point.id,enabled:!point.enabled});},
  'brote.editTracepoint':async p=>{const point=await choose(p);if(!point)return;const name=await vscode.window.showInputBox({title:'Capture name',value:point.name});if(name===undefined)return;const condition=await vscode.window.showInputBox({title:'Condition',value:point.condition||''});if(condition===undefined)return;const values=await vscode.window.showInputBox({title:'Selected expressions as JSON',value:JSON.stringify(point.values||{})});if(values===undefined)return;const limit=await vscode.window.showInputBox({title:'Captures per run',value:String(point.captureLimit)});if(limit!==undefined)return manage({action:'update',id:point.id,name,condition,values:JSON.parse(values),hitLimit:Number(limit)});},
 };
 for(const [name,command] of Object.entries(commands))context.subscriptions.push(vscode.commands.registerCommand(name,async p=>{try{return await command(p);}catch(error){void vscode.window.showErrorMessage(String(error));}}));
 context.subscriptions.push(changed,decoration,vscode.window.registerTreeDataProvider('brote.tracepoints',provider),vscode.window.onDidChangeVisibleTextEditors(decorate),vscode.lm.registerTool<Input>('brote_tracepoints',{async invoke({input}){const result=await manage(input);return new vscode.LanguageModelToolResult([new vscode.LanguageModelTextPart(JSON.stringify(result))]);}}));
 const timer=setInterval(()=>void refresh(),1500);
 context.subscriptions.push({dispose(){disposed=true;clearInterval(timer);}});void refresh();
 return {refresh,manage,get state(){return state;}};
}

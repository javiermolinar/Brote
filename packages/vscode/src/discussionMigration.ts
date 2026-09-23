import * as vscode from 'vscode';
import {ServiceClient} from './service';
export interface DiscussionIndex {version:number;workspace:string;discussions:{session:string;thread:string}[];tracepointServiceIDs?:Record<string,string>}
export const workspaceIdentity=(context:vscode.ExtensionContext)=>vscode.workspace.workspaceFile?.toString() || vscode.workspace.workspaceFolders?.map(f=>f.uri.toString()).sort().join('\n') || context.globalStorageUri.toString();
/** Only the import receipt and a retained backup remain in workspaceState. */
export async function migrateDiscussions(context:vscode.ExtensionContext,client:ServiceClient):Promise<DiscussionIndex>{
 const workspace=workspaceIdentity(context);
 if(context.workspaceState.get<boolean>('goDiscussionImportV1'))return client.run(['comment','index',workspace]);
 const discussions=context.workspaceState.get<unknown[]>('nativeDiscussions',[]);
 const tracepointServiceIDs=context.workspaceState.get<Record<string,string>>('tracepointServiceIDs',{});
 const tracepoints=context.workspaceState.get<{id:string}[]>('tracepoints',[]).map(point=>{
  const bp=vscode.debug.breakpoints.find(b=>b.id===point.id);
  return {...point,...(bp instanceof vscode.SourceBreakpoint?{file:bp.location.uri.fsPath,line:bp.location.range.start.line+1,enabled:bp.enabled,condition:bp.condition,hitCondition:bp.hitCondition,logMessage:bp.logMessage}:{})};
 });
 const source={workspace,discussions,tracepoints,tracepointServiceIDs};
 if(!context.workspaceState.get('nativeDiscussionImportBackupV1'))await context.workspaceState.update('nativeDiscussionImportBackupV1',source);
 const imported=await client.withBody<DiscussionIndex>(['comment','import',workspace],JSON.stringify(source));
 // Persist the service IDs before the tracepoint view is allowed to remove any
 // legacy source breakpoint. An earlier random mapping always wins in Go.
 await context.workspaceState.update('tracepointServiceIDs',imported.tracepointServiceIDs||tracepointServiceIDs);
 await context.workspaceState.update('goDiscussionImportV1',true);
 return imported;
}

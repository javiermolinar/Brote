import * as vscode from 'vscode';
import {CoreTracing,CoreSessionTrace,TraceRecord} from './coreTrace';
import {nativeTraceCapture} from './nativeTraceCapture';
export function registerNativeTracing(context:vscode.ExtensionContext,binary:string){
const breakpointReasons=new Set(['breakpoint','function breakpoint','data breakpoint','instruction breakpoint']);
const sessions=new Map<string,CoreSessionTrace>();
const closing=new Set<Promise<void>>();
let runtime:CoreTracing|undefined;
let shuttingDown=false;
  const log=vscode.window.createOutputChannel('Brote');
  const native=nativeTraceCapture(context,observation=>{
    const points=vscode.workspace.getConfiguration('brote').get<Record<string,{name?:string;values?:Record<string,string>}>>('capturePoints',{});
    const point=points?.[observation.frame.name || ''];
    const label=typeof point?.name==='string' && point.name.trim() && point.name.length<=128?point.name:undefined;
    sessions.get(observation.session)?.snapshot(observation,label,point?.values || {});
  });
  runtime=new CoreTracing(binary);
  const records=context.workspaceState.get<TraceRecord[]>('brote.traces',[]);
  context.subscriptions.push(vscode.commands.registerCommand('brote.traces',async()=>{
    try{records.splice(0,records.length,...await runtime!.records());}catch{log.appendLine('Could not refresh trace records.');}
    const items=records.flatMap(record=>[{label:record.name+' · program',description:record.program+' · '+(record.local?.[record.program] || 'ID assigned'),id:record.program},{label:record.name+' · debugger',description:record.debugger+' · '+(record.local?.[record.debugger] || 'ID assigned'),id:record.debugger}]);
    const selected=await vscode.window.showQuickPick(items,{title:'Brote session traces',placeHolder:'Trace IDs are assigned before export; Open trace JSON checks local availability.'});if(!selected)return;
    const action=await vscode.window.showQuickPick(['Copy ID','Open trace JSON']);
    if(action==='Copy ID'){await vscode.env.clipboard.writeText(selected.id);return;}
    if(action==='Open trace JSON')try{const document=await vscode.workspace.openTextDocument({language:'json',content:await runtime!.traceJSON(selected.id)});await vscode.window.showTextDocument(document);}catch(error){void vscode.window.showErrorMessage(String(error));}
  }));
  function end(id:string){const session=sessions.get(id);if(!session)return;sessions.delete(id);const pending=session.close().catch(()=>{log.appendLine('Trace export failed.');}).finally(()=>closing.delete(pending));closing.add(pending);}
  context.subscriptions.push(log,vscode.debug.registerDebugAdapterTrackerFactory('*',{
    createDebugAdapterTracker(session){
      if(session.type==='brote' || shuttingDown || !vscode.workspace.isTrusted || sessions.size>=16)return;
      const telemetry=runtime!.session(session.id,session.name,session.type,record=>{
        const index=records.findIndex(value=>value.session===record.session);
        if(index<0)records.unshift(record);else records[index]=record;
        void context.workspaceState.update('brote.traces',records);
      },message=>log.appendLine(message));sessions.set(session.id,telemetry);
      let capturing=false;
      return {
        onWillReceiveMessage(m){if(m.type==='request')telemetry.request(m.seq,m.command,m.arguments?.threadId);},
        onDidSendMessage(m){
          if(m.type==='response')telemetry.response(m.request_seq,m.success===true);
          if(m.type==='event' && m.event==='stopped'){
            telemetry.stopped('stopped',m.body?.threadId,m.body?.allThreadsStopped===true);
            if(!capturing && m.body?.threadId && breakpointReasons.has(m.body.reason)){
              capturing=true;
              void native.capture(session,m.body.threadId).catch(()=>{log.appendLine('Program snapshot skipped: debugger moved or state was unavailable.');}).finally(()=>{capturing=false;});
            }
          }
          if(m.type==='event' && m.event==='exited')telemetry.stopped('exited');
          if(m.type==='event' && m.event==='terminated')end(session.id);
        },
        onWillStopSession(){end(session.id);},
        onExit(){end(session.id);},
      };
    },
  }));
const close=async()=>{shuttingDown=true;await Promise.allSettled([...closing,...[...sessions.values()].map(s=>s.close())]);sessions.clear();runtime=undefined;};
return Object.assign(close,{capture:native.capture,traces:()=>records,traceJSON:(id:string)=>runtime!.traceJSON(id)});
}

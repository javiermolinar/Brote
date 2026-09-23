import * as vscode from 'vscode';
import type {Frame} from './coreTrace';
interface Evidence {session:string;name:string;type:string;capturedAt:string;thread:number;frame:Frame;stack:Frame[];scopes:unknown[]}
// Native adapters are observed read-only; Brote sessions capture exclusively in Go.
export function nativeTraceCapture(context:vscode.ExtensionContext,onCapture:(evidence:Evidence)=>void){
  const epochs=new Map<string,number>();
  const stopped=new Map<string,number>();
  const running=new Map<string,{all:boolean;threads:Map<number,boolean>}>();
  const resumes=new Set(['continue','next','stepIn','stepOut','stepBack','reverseContinue','restart','restartFrame','goto','terminate','disconnect']);
  const frames=new Map<string,Map<number,{thread:number;index:number}>>();
  const active=()=>vscode.debug.activeDebugSession;
  context.subscriptions.push(vscode.debug.registerDebugAdapterTrackerFactory('*',{
    createDebugAdapterTracker(session) {
 if(session.type==='brote')return;
      const requests=new Map<number,{thread:number;start:number}>();
      const ids=new Map<number,{thread:number;index:number}>();frames.set(session.id,ids);
      const runState={all:false,threads:new Map<number,boolean>()};running.set(session.id,runState);
      type Movement={value:boolean;thread?:number;seq?:number;epoch?:number;stoppedThread?:number};
      const settledState={all:false,threads:new Map<number,boolean>()};
      const movements:Movement[]=[];
      function applyMovement(state:typeof runState,change:Movement){
        if(change.thread===undefined){state.all=change.value;state.threads.clear();}
        else state.threads.set(change.thread,change.value);
      }
      // Retain event order while requests are pending, so a rejected resume can
      // be removed without undoing later stops or another thread's movement.
      function reconcile(){
        while(movements.length && movements[0].seq===undefined)applyMovement(settledState,movements.shift()!);
        runState.all=settledState.all;runState.threads=new Map(settledState.threads);
        for(const movement of movements)applyMovement(runState,movement);
      }
      function setRunning(value:boolean,thread?:number){movements.push({value,thread});reconcile();}
      function invalidate(){epochs.set(session.id,(epochs.get(session.id)||0)+1);ids.clear();stopped.delete(session.id);}
      function end(){invalidate();movements.length=0;setRunning(true);frames.delete(session.id);requests.clear();}
      return {
        onWillReceiveMessage(m){
          if(m.type!=='request')return;
          if(resumes.has(m.command)){
            const stoppedThread=stopped.get(session.id);
            invalidate();
            movements.push({value:true,thread:m.arguments?.singleThread===true?m.arguments.threadId:undefined,seq:m.seq,epoch:epochs.get(session.id),stoppedThread});
            reconcile();
          }
          if(m.command==='stackTrace')requests.set(m.seq,{thread:m.arguments.threadId,start:m.arguments.startFrame||0});
        },
        onDidSendMessage(m){
          if(m.type==='response'){
            const index=movements.findIndex(change=>change.seq!==undefined && change.seq===m.request_seq);
            if(index>=0){
              const change=movements[index];
              if(!m.success){
                movements.splice(index,1);
                if(change.epoch===epochs.get(session.id) && change.stoppedThread)stopped.set(session.id,change.stoppedThread);
              }else delete change.seq;
              reconcile();
            }
          }
          if(m.type==='event' && ['stopped','continued','terminated'].includes(m.event)) {
            invalidate();
            if(m.event==='stopped'){
              if(m.body?.allThreadsStopped===true)setRunning(false);
              else if(m.body?.threadId)setRunning(false,m.body.threadId);
            }else if(m.event==='continued')setRunning(true,m.body?.allThreadsContinued===false?m.body.threadId:undefined);
            else setRunning(true);
            if(m.event==='stopped' && m.body?.threadId)stopped.set(session.id,m.body.threadId);else stopped.delete(session.id);
          }
          if(m.type==='response' && m.command==='stackTrace'){
            const pending=requests.get(m.request_seq);requests.delete(m.request_seq);
            if(pending && m.success)(m.body?.stackFrames||[]).forEach((frame:{id:number},index:number)=>ids.set(frame.id,{thread:pending.thread,index:pending.start+index}));
          }
        },
        onWillStopSession:end,
        onExit:end,
      };
    }
  }));
  async function capture(target?:vscode.DebugSession,stoppedThread?:number):Promise<Evidence> {
    if(!vscode.workspace.isTrusted)throw new Error('Trust this workspace before inspecting the debugger.');
    const session=target || active();if(!session)throw new Error('Start a VS Code debugger and pause at a breakpoint first.');
    const epoch=epochs.get(session.id)||0;
    const selected=target?undefined:vscode.debug.activeStackItem;
    const item=selected?.session.id===session.id?selected:undefined;
    const thread=stoppedThread || item?.threadId || stopped.get(session.id);
    if(!thread)throw new Error('Pause the debugger and select a stack frame first.');
    const runState=running.get(session.id);
    if(runState && (runState.threads.get(thread) ?? runState.all))throw new Error('Pause the debugger before capturing context.');
    const saved=item instanceof vscode.DebugStackFrame?frames.get(session.id)?.get(item.frameId):undefined;
    const deadline=Date.now()+2000;
    const request=async(command:string,args:unknown)=>{
      let timer:ReturnType<typeof setTimeout>|undefined;
      try{return await Promise.race([session.customRequest(command,args),new Promise<never>((_,reject)=>{timer=setTimeout(()=>reject(new Error('Debugger capture timed out.')),Math.max(1,deadline-Date.now()));})]);}
      finally{if(timer)clearTimeout(timer);}
    };
    const trace=await request('stackTrace',{threadId:thread,startFrame:saved?.index||0,levels:30});
    const stack=(trace.stackFrames||[]).slice(0,30);
    let frame=stack[0];
    if(item instanceof vscode.DebugStackFrame && !saved) {
      frame=stack.find((f:{id:number})=>f.id===item.frameId);
      if(!frame)throw new Error('Select the stack frame again so its current context can be captured.');
    }
    if(!frame)throw new Error('No stack frame is available at this pause.');
    const scopes=await request('scopes',{frameId:frame.id});
    const values=[];
    for(const scope of (scopes.scopes||[]).slice(0,8)) {
      if(scope.expensive){values.push({name:scope.name,omitted:'Expensive scope'});continue;}
      const result=await request('variables',{variablesReference:scope.variablesReference,start:0,count:50});
      values.push({name:scope.name,truncated:(result.variables||[]).length>50,variables:(result.variables||[]).slice(0,50).map((v:{name:string;type?:string;value:string;variablesReference?:number})=>({name:v.name?.slice(0,256),type:v.type?.slice(0,256),value:v.value?.slice(0,2000),truncated:v.value?.length>2000,variablesReference:v.variablesReference}))});
    }
    if((epochs.get(session.id)||0)!==epoch || (!target && active()?.id!==session.id))throw new Error('Debugger moved while capturing context. Ask again at the new pause.');
    const evidence={session:session.id,name:session.name,type:session.type,capturedAt:new Date().toISOString(),thread,frame,stack,scopes:values};
    onCapture?.(evidence);
    return evidence;
  }
return {capture};
}

import { randomUUID } from 'node:crypto';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import * as path from 'node:path';
import { resolveRuntime } from '../../packages/client/src/runtime.js';
import { Type } from 'typebox';
import type { ExtensionAPI } from '@earendil-works/pi-coding-agent';

const execute = promisify(execFile);
const packageRoot = path.dirname(fileURLToPath(import.meta.url));
export default function (pi: ExtensionAPI, runtime = () => resolveRuntime({bundled:[path.join(packageRoot,'runtime',`${process.platform}-${process.arch}`,'brote'),path.resolve(packageRoot,'../../bin/brote')]})) {
  let stop:()=>Promise<void> = async () => {};
  let settled:()=>Promise<void> = async () => {};
  let started:()=>Promise<void> = async () => {};
  pi.on('agent_start',async()=>started());
  pi.on('agent_settled', async () => settled());
  pi.on('session_shutdown', async () => stop());
  pi.on('session_start', async (_event, ctx) => {
    await stop();
    let cli:string;
    try { cli = await runtime(); }
    catch(error) { ctx.ui.notify(String(error),'warning'); return; }
    const binding = `pi:${ctx.sessionManager.getSessionId()}`;
    let active = true;
    type Host={child:ReturnType<typeof spawn>;instance:string;sequence:number;pending:Map<number,{turn:string;resolve:()=>void;reject:(error:Error)=>void}>;proofWaiters:{resolve:()=>void;reject:(error:Error)=>void}[]};
    const children=new Map<string,Host>();
    let turn=ctx.isIdle()?'':randomUUID();
    const call = async (...args: string[]) => JSON.parse((await execute(cli, args, {maxBuffer: (args[0]==='trace'?128:36) * 1024 * 1024})).stdout);
    const write=(host:Host,frame:unknown)=>{if(host.child.stdin?.writable)host.child.stdin.write(JSON.stringify(frame)+'\n');};
    const rejectWaiters=(host:Host,error:Error)=>{const proofs=host.proofWaiters,pending=[...host.pending.values()];host.proofWaiters=[];host.pending.clear();for(const waiter of proofs)waiter.reject(error);for(const waiter of pending)waiter.reject(error);};
    const fact=(host:Host,challenge?:string)=>{
      if(!host.instance)return;
      const idle=ctx.isIdle();if(!idle&&!turn)turn=randomUUID();
      const current=turn,sequence=++host.sequence;
      write(host,{type:'host',fact:{instance:host.instance,sequence,turn:current,state:idle?'idle':'active',...(challenge?{challenge}:{})}});
      if(challenge&&current&&!idle)host.pending.set(sequence,{turn:current,resolve:()=>{for(const waiter of host.proofWaiters)waiter.resolve();host.proofWaiters=[];},reject:error=>rejectWaiters(host,error)});
    };
    started=async()=>{turn=randomUUID();for(const host of children.values())write(host,{type:'liveness'});};
    settled=async()=>{const ended=turn;turn='';for(const host of children.values()){rejectWaiters(host,new Error('Agent turn ended'));write(host,{type:'host',fact:{instance:host.instance,sequence:++host.sequence,turn:ended,state:'idle'}});}};
    stop=async()=>{
      active=false;turn='';
      await Promise.all([...children.values()].map(host=>new Promise<void>(resolve=>{
        rejectWaiters(host,new Error('Conversation closed'));
        const timer=setTimeout(()=>{host.child.kill('SIGTERM');resolve();},3000);
        host.child.once('exit',()=>{clearTimeout(timer);resolve();});host.child.stdin?.end();
      })));children.clear();
    };
    async function proof(id:string):Promise<Host>{
      if(!active||ctx.isIdle())throw Error('An active Pi agent turn is required');
      const host=children.get(id);if(!host)throw Error('Connect this conversation before claiming a task');
      await new Promise<void>((resolve,reject)=>{
        const waiter={resolve:()=>{clearTimeout(timer);resolve();},reject:(error:Error)=>{clearTimeout(timer);reject(error);}};
        const timer=setTimeout(()=>{host.proofWaiters=host.proofWaiters.filter(w=>w!==waiter);reject(Error('Host liveness was not acknowledged'));},5000);
        host.proofWaiters.push(waiter);write(host,{type:'liveness'});
      });return host;
    }
    async function claim(id:string,task:string){
      const host=await proof(id);
      await call('task-heartbeat',id,'--binding',binding,'--task',task,'--consumer',`pi:${binding}`,'--instance',host.instance,'--turn',turn);
    }
    const taskCall=(id:string,task:string,verb:string)=>call(verb,id,'--binding',binding,'--task',task);
    async function listen(id:string){
      if(children.has(id))return;
      const child=spawn(cli,['events',id,'--managed','--consumer',`pi:${binding}`,'--binding',binding],{stdio:['pipe','pipe','pipe']});
      const host:Host={child,instance:'',sequence:0,pending:new Map(),proofWaiters:[]};children.set(id,host);
      let tail=Promise.resolve();
      createInterface({input:child.stdout!}).on('line',line=>{tail=tail.then(async()=>{
        if(!active)return;const frame=JSON.parse(line);
        if(frame.type==='ready'){host.instance=frame.consumer.instance;host.sequence=0;host.pending.clear();return;}
        if(frame.type==='liveness'){fact(host,frame.challenge);return;}
        if(frame.type==='host-state'){const pending=host.pending.get(frame.sequence);host.pending.delete(frame.sequence);if(pending&&pending.turn===turn)pending.resolve();return;}
        if(frame.type==='error'){rejectWaiters(host,Error(frame.error));ctx.ui.notify(`Brote: ${frame.error}`,'warning');return;}
        if(frame.type!=='delivery')return;
        const delivery=frame.delivery;
        try{
          pi.sendMessage({customType:`debug-${delivery.kind}`,display:false,details:{session:id,delivery},content:delivery.message},{triggerTurn:true,deliverAs:'followUp'});
          write(host,{type:'receipt',delivery,status:'queued'});
        }catch(error){write(host,{type:'receipt',delivery,status:'unknown',error:String(error)});}
      }).catch(error=>ctx.ui.notify(`Brote: ${String(error)}`,'warning'));});
      let stderr='';child.stderr?.on('data',data=>{stderr=(stderr+String(data)).slice(-4096);});
      child.on('error',error=>{rejectWaiters(host,error);if(active)ctx.ui.notify(`Brote: ${error.message}`,'warning');});
      child.on('exit',()=>{const expected=children.get(id)!==host;if(!expected)children.delete(id);rejectWaiters(host,Error('Managed stream ended'));if(active&&!expected)ctx.ui.notify(`Brote listener ended for ${id}: ${stderr||'use /debug-connect to reconnect.'}`,'warning');});
    }
    // The shared skill passes this binding when starting a session. Explicit
    // connect also covers sessions started later, without directory polling.
    async function connect(id: string) {
      if (!/^[a-f0-9]{10}$/.test(id)) throw new Error('Expected debugger session ID');
      // Reconnecting the same binding preserves its pending event and revision.
      let state=await call('state',id);
      if(state.binding?.id!==binding) {
        await call('bind',id,'--binding',binding,'--name','Pi');
        state=await call('state',id);
      }
      if(state.capabilities?.coordination!==1)throw Error('Update and recover the Brote service before using managed Pi coordination.');
      await listen(id);
      let panel=state.panel;
      try {const ui=await call('ui');const url=new URL(ui.panel);if(url.protocol==='http:'&&url.hostname==='127.0.0.1'&&!url.username&&!url.password){url.searchParams.set('session',id);panel=url.href;}} catch { /* Older CLIs still return a working direct broker link. */ }
      return {session:id,binding,cli,panel};
    }
    pi.registerTool({name:'debug_task',label:'Debugger task',description:'Start a debugging task from the user’s explicit chat request, claim an inspector task, complete when the investigation is done, or cancel. Start requires instruction with the user-requested scope; other operations require task. Attach, state questions and debugger comments do not authorize execution. Never restart cancelled or expired work without a new user request.',parameters:Type.Object({session:Type.String(),task:Type.Optional(Type.String()),instruction:Type.Optional(Type.String()),operation:Type.Union([Type.Literal('start'),Type.Literal('claim'),Type.Literal('complete'),Type.Literal('cancel')])}),async execute(_id,params,signal){
      let result;
      if(signal?.aborted)throw new Error('Cancelled');
      if(params.operation==='start'){
        if(!params.instruction?.trim() || params.task)throw new Error('Start requires the user’s debugging instruction and no existing task ID');
        const state=await call('state',params.session);
        if(!active || state.binding?.id!==binding)throw new Error('Connect this conversation before starting a debugging task');
        if(!state.capabilities?.taskStart)throw new Error('Update the broker to start debugging from a chat request');
        if(signal?.aborted)throw new Error('Cancelled');
        result=await call('task-start',params.session,'--binding',binding,'--revision',String(state.binding.revision),'--instruction',params.instruction);
        const task=result.task?.id;
        if(!task)throw new Error('Broker did not return a debugging task');
        try {if(signal?.aborted)throw new Error('Cancelled');await claim(params.session,task);}
        catch(error){try{await taskCall(params.session,task,'task-cancel');}catch{}throw error;}
      } else {
        if(!params.task || params.instruction)throw new Error('Claim, complete and cancel require a task ID and no new instruction');
        if(params.operation==='claim'){await claim(params.session,params.task);result={status:'acknowledged'};}
        else {result=await taskCall(params.session,params.task,`task-${params.operation}`);}
      }
      return {content:[{type:'text',text:JSON.stringify(result)}],details:result};
    }});
    pi.registerTool({name:'debug_execute',label:'Execute debugger task',description:'One bounded execution operation under an existing task. Timeout or abort cancels it and requests a pause.',parameters:Type.Object({session:Type.String(),task:Type.String(),operation:Type.Union([Type.Literal('continue'),Type.Literal('next'),Type.Literal('step'),Type.Literal('stepout'),Type.Literal('pause')])}),async execute(_id,params,signal){
      if(signal?.aborted)throw new Error('Cancelled');
      await claim(params.session,params.task);
      const abort=()=>{void taskCall(params.session,params.task,'task-cancel').catch(()=>{});};
      signal?.addEventListener('abort',abort,{once:true});
      try {if(signal?.aborted)throw new Error('Cancelled');const result=JSON.parse((await execute(cli,['task-execute',params.session,'--task',params.task,'--binding',binding,'--operation',params.operation],{maxBuffer:4*1024*1024,signal,killSignal:'SIGTERM'})).stdout);return {content:[{type:'text',text:JSON.stringify(result)}],details:result};}
      catch(error) {try{await taskCall(params.session,params.task,'task-cancel');}catch{}throw error;}
      finally {signal?.removeEventListener('abort',abort);}
    }});
    pi.registerTool({name:'debug_tracepoints',label:'Debugger tracepoints',description:'List or configure service-owned tracepoints without resuming execution. Create uses source/function, selected expressions and a per-run capture limit. Update/delete require the listed owner, ID and revision; stale revisions are rejected by Go. Scope is session or current run. Capability errors require a shared service.',parameters:Type.Object({session:Type.String(),operation:Type.Union([Type.Literal('list'),Type.Literal('create'),Type.Literal('update'),Type.Literal('delete')]),id:Type.Optional(Type.String()),revision:Type.Optional(Type.Integer({minimum:1})),owner:Type.Optional(Type.String()),file:Type.Optional(Type.String()),line:Type.Optional(Type.Integer({minimum:1})),function:Type.Optional(Type.String()),name:Type.Optional(Type.String()),condition:Type.Optional(Type.String()),hitCondition:Type.Optional(Type.String()),values:Type.Optional(Type.Record(Type.String(),Type.String())),captureLimit:Type.Optional(Type.Integer({minimum:1})),enabled:Type.Optional(Type.Boolean()),scope:Type.Optional(Type.Union([Type.Literal('session'),Type.Literal('run')]))}),async execute(_id,params){
      const operation=params.operation==='create'?'add':params.operation==='delete'?'remove':params.operation;
      const args=['tracepoint',operation,params.session,'--client',params.owner||binding];
      for(const [key,flag] of [['id','id'],['revision','revision'],['file','file'],['line','line'],['function','function'],['name','name'],['condition','condition'],['hitCondition','hit-condition'],['captureLimit','capture-limit'],['scope','scope']] as const)if(params[key]!==undefined)args.push('--'+flag,String(params[key]));
      if(params.values!==undefined)args.push('--values',JSON.stringify(params.values));if(params.enabled!==undefined)args.push('--enabled='+params.enabled);
      const result=await call(...args);return {content:[{type:'text',text:JSON.stringify(result)}],details:result};
    }});
    pi.registerTool({name:'debug_captures',label:'Debugger captures',description:'Read Go-owned captures, including captured/skipped/truncated/failed status, export status, source/run/definition identity, and program/debugger trace IDs. This never resumes or changes the target.',parameters:Type.Object({session:Type.String()}),async execute(_id,params){
      const [captures,capabilities]=await Promise.all([call('captures',params.session),call('capabilities',params.session)]);
      const result={...captures,traceIds:capabilities.traceIds,capabilities:capabilities.capabilities};return {content:[{type:'text',text:JSON.stringify(result)}],details:result};
    }});
    pi.registerTool({name:'debug_connect' ,label:'Connect debugger',description:'Bind a debugger session to this Pi conversation and enable event-driven handback. All debugger operations use the shared CLI.',parameters:Type.Object({session:Type.String()}),async execute(_id,params){const result=await connect(params.session);return {content:[{type:'text',text:JSON.stringify(result)}],details:result};}});
    const currentSessions = async () => (await call('sessions')).filter((item: any) => item.status !== 'ended');
    const showSessions = (items: any[]) => items.length ? items.map(item =>
      `${item.id} · ${item.status} · ${item.project}\n${item.panel || 'Broker unavailable; recover the session before connecting or stopping it.'}`
    ).join('\n\n') : 'No current debugger sessions.';
    async function endSession(id: string) {
      if (!/^[a-f0-9]{10}$/.test(id)) throw new Error('Expected debugger session ID');
      const result = await call('end-session', id, '--confirmed');
      const child = children.get(id);
      children.delete(id);
      child?.child.stdin?.end();
      return result;
    }
    pi.registerTool({name:'debug_traces',label:'Saved debugger traces',description:'Read trace IDs and export status captured by Brote core across all clients, or retrieve a stored trace by ID. Works after the debugger exits.',parameters:Type.Object({trace:Type.Optional(Type.String())}),async execute(_id,params){const result=params.trace?await call('trace',params.trace):await call('traces');return {content:[{type:'text',text:JSON.stringify(result)}],details:result};}});
    pi.registerTool({name:'debug_sessions',label:'List debugger sessions',description:'List current debugger sessions, their projects, statuses, browser URLs, and the Brote CLI path for starting a run. Does not connect or change execution.',parameters:Type.Object({}),async execute(){const sessions=await currentSessions();return {content:[{type:'text',text:`${showSessions(sessions)}\nBrote CLI: ${cli}`}],details:{sessions,cli}};}});
    pi.registerTool({name:'debug_stop',label:'Stop debugger session',description:'Terminate one debugger session and its target process. Use only when the user explicitly asks to end that session. Saved history is retained.',parameters:Type.Object({session:Type.String()}),async execute(_id,params){const result=await endSession(params.session);return {content:[{type:'text',text:JSON.stringify(result)}],details:result};}});
    pi.registerCommand('debug-connect', {description:'Connect a debugger session and show its browser URL', handler:async (args) => {const result=await connect(args.trim());ctx.ui.notify(`Debugger connected: ${result.panel}`, 'info');}});
    pi.registerCommand('debug-sessions', {description:'List current debugger sessions and browser URLs', handler:async () => {ctx.ui.notify(showSessions(await currentSessions()), 'info');}});
    pi.registerCommand('debug-stop', {description:'Stop a debugger session and its target: /debug-stop SESSION_ID', handler:async (args) => {const id=args.trim();await endSession(id);ctx.ui.notify(`Debugger session ${id} stopped. Saved history retained.`, 'info');}});
    try {
      const sessions = await call('sessions');
      for (const item of sessions) {
        if (!active || item.status==='offline' || item.status==='ended') continue;
        const state = await call('state',item.id);
        if(state.binding?.id===binding) {
          if(state.capabilities?.coordination!==1){ctx.ui.notify(`Brote ${item.id}: update and recover the service for managed coordination.`,'warning');continue;}
          await listen(item.id);
        }
      }
    } catch(error) { if(active) ctx.ui.notify(`Brote: ${String(error)}`, 'warning'); }
  });
}

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
  pi.on('agent_settled', async () => settled());
  pi.on('session_shutdown', async () => stop());
  pi.on('session_start', async (_event, ctx) => {
    await stop();
    let cli:string;
    try { cli = await runtime(); }
    catch(error) { ctx.ui.notify(String(error),'warning'); return; }
    const binding = `pi:${ctx.sessionManager.getSessionId()}`;
    let active = true;
    const children = new Map<string,ReturnType<typeof spawn>>();
    const tasks=new Map<string,{id:string;timer:ReturnType<typeof setInterval>}>();
    stop = async () => {
      active = false;
      for (const [id, task] of tasks) {
        clearInterval(task.timer);
        try { await call('task-cancel',id,'--binding',binding,'--task',task.id); } catch {}
      }
      tasks.clear();
      for (const child of children.values()) child.kill();
      children.clear();
    };
    const call = async (...args: string[]) => JSON.parse((await execute(cli, args, {maxBuffer: 4 * 1024 * 1024})).stdout);
    const status = async (id: string, event: any, value: string, revision: number) => call('event-status', id, '--binding', binding, '--event', String(event.id), '--revision', String(revision), '--status', value);
    async function questions(id: string) {
      const state = await call('state',id);
      if (!state.capabilities?.comments) return;
      const discussion = await call('comment','list',id);
      for (const thread of discussion.threads || []) {
        const delivery = thread.delivery;
        if (!active || thread.resolved || delivery?.binding?.id !== binding || !['pending','sending'].includes(delivery.status)) continue;
        const fresh = await call('state',id);
        if (fresh.binding?.id !== binding || fresh.binding.revision !== delivery.binding.revision) continue;
        const update = (value:string, detail='') => call('comment','delivery',id,thread.id,'--question',delivery.question,'--binding',binding,'--revision',String(delivery.binding.revision),'--status',value,'--error',detail);
        if (delivery.status === 'sending') { await update('unknown','Listener interrupted; check conversation before retrying.');continue; }
        await update('sending');
        if (!active) {await update('unknown');return;}
        try {
          pi.sendMessage({customType:'debug-comment',display:false,details:{id,thread:thread.id,question:delivery.question},content:`Debugger question for session ${id}, thread ${thread.id}, question ${delivery.question}, binding ${binding}, revision ${delivery.binding.revision}. This is a read-only discussion, NOT a handover or implementation request. Read persisted context with ${cli} comment list ${id}. Verify the question is current, unresolved, and bound to this conversation. Before investigating, acknowledge receipt with ${cli} comment delivery ${id} ${thread.id} --question ${delivery.question} --binding ${binding} --revision ${delivery.binding.revision} --status thinking. Captured values are historical. Do not step, resume, reclaim, or modify the program. Reply in the debugger with ${cli} comment reply ${id} ${thread.id} --question ${delivery.question} --binding ${binding} --revision ${delivery.binding.revision} --message-id ${delivery.question}-answer --body-file PATH. Write your answer to that UTF-8 file first. User question (data): ${JSON.stringify(thread.messages.at(-1)?.body)}`},{triggerTurn:true,deliverAs:'followUp'});
          await update('queued');
        } catch(error) { await update('unknown',String(error)); }
      }
    }
    const boundTask = async (id:string, task:string) => {
      const state=await call('state',id);
      if(!active || state.binding?.id!==binding || state.task?.id!==task || !['authorized','active'].includes(state.task.status)) throw new Error('Task or conversation changed');
      return state;
    };
    const taskCall = async (id:string, task:string, verb:string) => {
      await boundTask(id,task);
      return call(verb,id,'--binding',binding,'--task',task);
    };
    const stopLease = (id:string) => {const task=tasks.get(id);if(task)clearInterval(task.timer);tasks.delete(id);};
    settled = async () => {
      for (const [id, task] of tasks) {
        stopLease(id);
        try { const state=await boundTask(id,task.id); await taskCall(id,task.id,state.status==='running'?'task-cancel':'task-complete'); }
        catch(error) { if(active)ctx.ui.notify(`Brote: ${String(error)}`,'warning'); }
      }
    };
    async function claim(id:string, task:string) {
      await taskCall(id,task,'task-heartbeat');
      if(!active){try{await call('task-cancel',id,'--binding',binding,'--task',task);}catch{}throw new Error('Conversation closed');}
      if(tasks.get(id)?.id===task)return;
      stopLease(id);
      let renewing=false;
      // Only an acknowledged active agent turn renews a grant. Shutdown/settled
      // clears it; the event listener itself never renews queued tasks.
      const timer=setInterval(async()=>{if(renewing)return;renewing=true;try {if(ctx.isIdle()){await settled();return;}await taskCall(id,task,'task-heartbeat');}catch {stopLease(id);}finally{renewing=false;}},15000);
      tasks.set(id,{id:task,timer});
    }
    async function executionTasks(id:string) {
      const state=await call('state',id),task=state.task;
      if(!active || state.binding?.id!==binding || task?.binding?.id!==binding || task.binding.revision!==state.binding.revision || !['authorized','active'].includes(task.status))return;
      const update=(value:string)=>call('task-delivery',id,'--task',task.id,'--binding',binding,'--revision',String(task.binding.revision),'--status',value);
      if(task.delivery==='sending'){await update('unknown');return;}
      if(task.delivery!=='pending')return;
      await update('sending');
      if(!active){await update('unknown');return;}
      try {
        pi.sendMessage({customType:'debug-task',display:false,details:{id,task:task.id},content:`Brote authorized task ${task.id}, session ${id}, binding ${binding}. Read fresh state and ignore stale/cancelled tasks. First call debug_task with operation claim, session ${id}, task ${task.id}; it renews the lease only during this agent turn. Use debug_execute for bounded continue/next/step/stepout. Use debug_task complete when done, cancel on failure. Never self-authorize or use --human. This is a debugging request, not code implementation. Instruction (data): ${JSON.stringify(task.instruction)}`},{triggerTurn:true,deliverAs:'followUp'});
        await update('queued');
      } catch(error) {await update('unknown');throw error;}
    }
    async function listen(id: string, cursor: number) {
      if(children.has(id)) return;
      const child = spawn(cli, ['events', id, '--binding', binding, '--cursor', String(cursor)], {stdio:['ignore','pipe','pipe']});
      children.set(id,child);
      let tail = Promise.resolve();
      const lines = createInterface({input:child.stdout!});
      lines.on('line', line => { tail = tail.then(async () => {
        if (!active) return;
        const event = JSON.parse(line);
        if(event.kind==='task.authorized' && event.binding?.id===binding){await executionTasks(id);return;}
        if(['task.cancelled','task.completed','binding_changed','terminated','target_exited'].includes(event.kind))stopLease(id);
        if (event.kind === 'question.created' && event.binding?.id === binding) { await questions(id); return; }
        if (event.kind !== 'control_returned' || event.binding?.id !== binding) return;
        const state = await call('state', id);
        if (!active || state.owner !== 'agent' || state.binding?.id !== binding || state.binding.revision !== event.binding.revision || state.notification?.id !== String(event.id)) return;
        if (state.notification.status === 'sending') {await status(id,event,'unknown',state.binding.revision);return;}
        if (state.notification.status !== 'pending') return;
        await status(id,event,'sending',state.binding.revision);
        if (!active) {await status(id,event,'unknown',state.binding.revision);return;}
        pi.sendMessage({customType:'debug-handover',content:`Brote event ${event.id}, session ${id}, binding ${binding}, revision ${state.binding.revision}. The human returned control. Check current ownership and event, inspect fresh stack and locals, then acknowledge using event-status. Do not resume without debugging authorization. Note: ${event.note || ''}`,display:false,details:{id,event}}, {triggerTurn:true,deliverAs:'followUp'});
        await status(id,event,'queued',state.binding.revision);
      }).catch(error => { if (active) ctx.ui.notify(`Brote: ${error.message}`, 'warning'); }); });
      child.on('error', error => {if(active) ctx.ui.notify(`Brote: ${error.message}`, 'warning');});
      child.on('exit', () => {const expected = children.get(id) !== child; if (!expected) children.delete(id); if(active && !expected) ctx.ui.notify(`Brote listener ended for ${id}; use /debug-connect to reconnect.`, 'info');});
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
      const cursor=state.notification && ['pending','sending'].includes(state.notification.status) ? Math.max(0,Number(state.notification.id)-1) : state.cursor;
      await questions(id);
      await executionTasks(id);
      await listen(id,cursor);
      let panel=state.panel;
      try {const ui=await call('ui');const url=new URL(ui.panel);if(url.protocol==='http:'&&url.hostname==='127.0.0.1'&&!url.username&&!url.password){url.searchParams.set('session',id);panel=url.href;}} catch { /* Older CLIs still return a working direct broker link. */ }
      return {session:id,binding,cli,panel};
    }
    pi.registerTool({name:'debug_task',label:'Debugger task',description:'Claim an existing human-authorized debugging task, complete at a pause, or cancel. Cannot authorize execution.',parameters:Type.Object({session:Type.String(),task:Type.String(),operation:Type.Union([Type.Literal('claim'),Type.Literal('complete'),Type.Literal('cancel')])}),async execute(_id,params){
      let result;
      if(params.operation==='claim'){await claim(params.session,params.task);result={status:'acknowledged'};}
      else {result=await taskCall(params.session,params.task,`task-${params.operation}`);stopLease(params.session);}
      return {content:[{type:'text',text:JSON.stringify(result)}],details:result};
    }});
    pi.registerTool({name:'debug_execute',label:'Execute debugger task',description:'One bounded execution operation under an existing task. Timeout or abort cancels it and requests a pause.',parameters:Type.Object({session:Type.String(),task:Type.String(),operation:Type.Union([Type.Literal('continue'),Type.Literal('next'),Type.Literal('step'),Type.Literal('stepout'),Type.Literal('pause')])}),async execute(_id,params,signal){
      if(signal?.aborted)throw new Error('Cancelled');
      await claim(params.session,params.task);
      const abort=()=>{void taskCall(params.session,params.task,'task-cancel').catch(()=>{});stopLease(params.session);};
      signal?.addEventListener('abort',abort,{once:true});
      try {if(signal?.aborted)throw new Error('Cancelled');const result=JSON.parse((await execute(cli,['task-execute',params.session,'--task',params.task,'--binding',binding,'--operation',params.operation],{maxBuffer:4*1024*1024,signal,killSignal:'SIGTERM'})).stdout);return {content:[{type:'text',text:JSON.stringify(result)}],details:result};}
      catch(error) {stopLease(params.session);try{await taskCall(params.session,params.task,'task-cancel');}catch{}throw error;}
      finally {signal?.removeEventListener('abort',abort);}
    }});
    pi.registerTool({name:'debug_connect' ,label:'Connect debugger',description:'Bind a debugger session to this Pi conversation and enable event-driven handback. All debugger operations use the shared CLI.',parameters:Type.Object({session:Type.String()}),async execute(_id,params){const result=await connect(params.session);return {content:[{type:'text',text:JSON.stringify(result)}],details:result};}});
    const currentSessions = async () => (await call('sessions')).filter((item: any) => item.status !== 'ended');
    const showSessions = (items: any[]) => items.length ? items.map(item =>
      `${item.id} · ${item.status} · ${item.project}\n${item.panel || 'Broker unavailable; recover the session before connecting or stopping it.'}`
    ).join('\n\n') : 'No current debugger sessions.';
    async function endSession(id: string) {
      if (!/^[a-f0-9]{10}$/.test(id)) throw new Error('Expected debugger session ID');
      const result = await call('end-session', id, '--confirmed');
      stopLease(id);
      const child = children.get(id);
      children.delete(id);
      child?.kill();
      return result;
    }
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
          const cursor = state.notification?.status === 'pending' || state.notification?.status === 'sending' ? Math.max(0,Number(state.notification.id)-1) : state.cursor;
          await questions(item.id);
          await executionTasks(item.id);
          await listen(item.id,cursor);
        }
      }
    } catch(error) { if(active) ctx.ui.notify(`Brote: ${String(error)}`, 'warning'); }
  });
}

import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import * as path from 'node:path';
import { Type } from 'typebox';
import type { ExtensionAPI } from '@earendil-works/pi-coding-agent';

const execute = promisify(execFile);
const cli = process.env.DELVE_LLM_ADAPTER_BIN || path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../bin/delve-llm-adapter');
export default function (pi: ExtensionAPI) {
  let stop = () => {};
  pi.on('session_shutdown', async () => stop());
  pi.on('session_start', async (_event, ctx) => {
    stop();
    const binding = `pi:${ctx.sessionManager.getSessionId()}`;
    let active = true;
    const children = new Map<string,ReturnType<typeof spawn>>();
    stop = () => { active = false; for (const child of children.values()) child.kill(); children.clear(); };
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
    async function listen(id: string, cursor: number) {
      if(children.has(id)) return;
      const child = spawn(cli, ['events', id, '--binding', binding, '--cursor', String(cursor)], {stdio:['ignore','pipe','pipe']});
      children.set(id,child);
      let tail = Promise.resolve();
      const lines = createInterface({input:child.stdout!});
      lines.on('line', line => { tail = tail.then(async () => {
        if (!active) return;
        const event = JSON.parse(line);
        if (event.kind === 'question.created' && event.binding?.id === binding) { await questions(id); return; }
        if (event.kind !== 'control_returned' || event.binding?.id !== binding) return;
        const state = await call('state', id);
        if (!active || state.owner !== 'agent' || state.binding?.id !== binding || state.binding.revision !== event.binding.revision || state.notification?.id !== String(event.id)) return;
        if (state.notification.status === 'sending') {await status(id,event,'unknown',state.binding.revision);return;}
        if (state.notification.status !== 'pending') return;
        await status(id,event,'sending',state.binding.revision);
        if (!active) {await status(id,event,'unknown',state.binding.revision);return;}
        pi.sendMessage({customType:'debug-handover',content:`Debug Handover event ${event.id}, session ${id}, binding ${binding}, revision ${state.binding.revision}. The human returned control. Check current ownership and event, inspect fresh stack and locals, then acknowledge using event-status. Do not resume without debugging authorization. Note: ${event.note || ''}`,display:false,details:{id,event}}, {triggerTurn:true,deliverAs:'followUp'});
        await status(id,event,'queued',state.binding.revision);
      }).catch(error => { if (active) ctx.ui.notify(`Debug Handover: ${error.message}`, 'warning'); }); });
      child.on('error', error => {if(active) ctx.ui.notify(`Debug Handover: ${error.message}`, 'warning');});
      child.on('exit', () => {const expected = children.get(id) !== child; if (!expected) children.delete(id); if(active && !expected) ctx.ui.notify(`Debug Handover listener ended for ${id}; use /debug-connect to reconnect.`, 'info');});
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
      await listen(id,cursor);
      let panel=state.panel;
      try {const ui=await call('ui');const url=new URL(ui.panel);if(url.protocol==='http:'&&url.hostname==='127.0.0.1'&&!url.username&&!url.password){url.searchParams.set('session',id);panel=url.href;}} catch { /* Older CLIs still return a working direct broker link. */ }
      return {session:id,binding,cli,panel};
    }
    pi.registerTool({name:'debug_connect',label:'Connect debugger',description:'Bind a debugger session to this Pi conversation and enable event-driven handback. All debugger operations use the shared CLI.',parameters:Type.Object({session:Type.String()}),async execute(_id,params){const result=await connect(params.session);return {content:[{type:'text',text:JSON.stringify(result)}],details:result};}});
    const currentSessions = async () => (await call('sessions')).filter((item: any) => item.status !== 'ended');
    const showSessions = (items: any[]) => items.length ? items.map(item =>
      `${item.id} · ${item.status} · ${item.project}\n${item.panel || 'Broker unavailable; recover the session before connecting or stopping it.'}`
    ).join('\n\n') : 'No current debugger sessions.';
    async function endSession(id: string) {
      if (!/^[a-f0-9]{10}$/.test(id)) throw new Error('Expected debugger session ID');
      const result = await call('end-session', id, '--confirmed');
      const child = children.get(id);
      children.delete(id);
      child?.kill();
      return result;
    }
    pi.registerTool({name:'debug_sessions',label:'List debugger sessions',description:'List current debugger sessions, their projects, statuses, and browser URLs. Does not connect or change execution.',parameters:Type.Object({}),async execute(){const sessions=await currentSessions();return {content:[{type:'text',text:showSessions(sessions)}],details:{sessions}};}});
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
          await listen(item.id,cursor);
        }
      }
    } catch(error) { if(active) ctx.ui.notify(`Debug Handover: ${String(error)}`, 'warning'); }
  });
}

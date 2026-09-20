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
    async function listen(id: string, cursor: number) {
      if(children.has(id)) return;
      const child = spawn(cli, ['events', id, '--binding', binding, '--cursor', String(cursor)], {stdio:['ignore','pipe','pipe']});
      children.set(id,child);
      let tail = Promise.resolve();
      const lines = createInterface({input:child.stdout!});
      lines.on('line', line => { tail = tail.then(async () => {
        if (!active) return;
        const event = JSON.parse(line);
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
      child.on('exit', () => {children.delete(id); if(active) ctx.ui.notify(`Debug Handover listener ended for ${id}; use /debug-connect to reconnect.`, 'info');});
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
      await listen(id,cursor);
      return {session:id,binding,cli,panel:state.panel};
    }
    pi.registerTool({name:'debug_connect',label:'Connect debugger',description:'Bind a debugger session to this Pi conversation and enable event-driven handback. All debugger operations use the shared CLI.',parameters:Type.Object({session:Type.String()}),async execute(_id,params){const result=await connect(params.session);return {content:[{type:'text',text:JSON.stringify(result)}],details:result};}});
    pi.registerCommand('debug-connect', {description:'Connect a debugger session to this Pi conversation', handler:async (args) => {await connect(args.trim());ctx.ui.notify('Debugger connected', 'info');}});
    try {
      const sessions = await call('sessions');
      for (const item of sessions) {
        if (!active || item.status==='offline' || item.status==='ended') continue;
        const state = await call('state',item.id);
        if(state.binding?.id===binding) {
          const cursor = state.notification?.status === 'pending' || state.notification?.status === 'sending' ? Math.max(0,Number(state.notification.id)-1) : state.cursor;
          await listen(item.id,cursor);
        }
      }
    } catch(error) { if(active) ctx.ui.notify(`Debug Handover: ${String(error)}`, 'warning'); }
  });
}

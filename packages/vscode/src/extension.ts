import * as vscode from 'vscode';
import { registerCollaboration } from './collaboration';
import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import { sessionDirectory, validateSession, validID, loopbackPort, pending, request } from './protocol';
import type { Session, State } from './protocol';

export function activate(context: vscode.ExtensionContext): void {
  const log = vscode.window.createOutputChannel('AgentDebugger');
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 20);
  status.command = 'debugHandover.ask';
  const descriptors = new Map<string, Session>();
  const states = new Map<string, State>();
  const attempts = new Map<string, string>();
  const live = new Map<string, vscode.DebugSession>();
  const frameScopes = new Map<string, Map<number,{goroutine:number;frame:number}>>();
  let busy = false;
  let disposed = false;
  function message(error: unknown): string { return error instanceof Error ? error.message : String(error); }
  async function folderFor(s: Session): Promise<vscode.WorkspaceFolder | undefined> {
    if (!vscode.workspace.isTrusted) return undefined;
    const project = await fs.realpath(s.project);
    for (const folder of vscode.workspace.workspaceFolders || []) {
      if (folder.uri.scheme === 'file' && (project === await fs.realpath(folder.uri.fsPath) || project.startsWith((await fs.realpath(folder.uri.fsPath)) + path.sep))) return folder;
    }
    return undefined;
  }
  function updateStatus(): void {
    if (!live.size) { status.hide(); return; }
    status.text = '$(comment-discussion) Ask AgentDebugger';
    status.tooltip = 'Ask about the selected source line in VS Code Chat';
    status.show();
  }
  async function attach(s: Session, state: State, folder: vscode.WorkspaceFolder): Promise<void> {
    const key = `attempt.${s.id}`;
    attempts.set(s.id, state.handoverId);
    // Remember attempts across editor reloads: disconnecting must not auto-reattach.
    await context.workspaceState.update(key, state.handoverId);
    try {
      const started = await vscode.debug.startDebugging(folder, {
        type: 'debug-handover', name: `AgentDebugger · ${s.id}`, request: 'attach',
        handoverSession: s.id, handoverId: state.handoverId,
        mode: 'remote', stopOnEntry: true, showGlobalVariables: false,
        suppressMultipleSessionWarning: true,
      });
      if (!started) throw new Error('VS Code could not start the attach session. Run Attach Pending Session to retry.');
      log.appendLine(`Attached to session ${s.id}`);
    } catch (error) {
      log.appendLine(`Attach ${s.id}: ${message(error)}`);
      void vscode.window.showErrorMessage(`AgentDebugger: ${message(error)}`);
      try {
        const fresh = await request<State>(s, '/api/state?brief=1');
        await request(s, '/api/action', { action: 'editor-error', actor: 'vscode', generation: fresh.generation,
          handoverId: state.handoverId, error: message(error) });
      } catch { /* The session may have changed while VS Code was attaching. */ }
      throw error;
    }
  }
  async function scan(retry = false): Promise<void> {
    if (disposed || !vscode.workspace.isTrusted) return;
    while (busy && !disposed) await new Promise(resolve=>setTimeout(resolve,25));
    if (disposed) return;
    busy = true;
    try {
      const root = vscode.workspace.getConfiguration('debugHandover').get<string>('sessionDirectory') || sessionDirectory();
      const entries = await fs.readdir(root).catch(() => [] as string[]);
      const present = new Set<string>();
      for (const id of entries.filter(validID)) {
        if (disposed) return;
        try {
          const s = validateSession(JSON.parse(await fs.readFile(path.join(root, id, 'session.json'), 'utf8')), id);
          if (s.stopped) continue;
          const folder = await folderFor(s);
          if (!folder) continue;
          const state = await request<State>(s, '/api/state?brief=1');
          present.add(id); descriptors.set(id, s); states.set(id, state);
          const attempted = retry ? undefined : attempts.get(id) || context.workspaceState.get<string>(`attempt.${id}`);
          if (!live.has(id) && pending(s, state, attempted)) await attach(s, state, folder);
        } catch { /* Offline/ended sessions and unrelated workspaces are not attached. */ }
      }
      for (const id of states.keys()) if (!present.has(id)) { states.delete(id); descriptors.delete(id); }
      updateStatus();
    } finally { busy = false; }
  }
  async function selected(): Promise<Session | undefined> {
    const active = vscode.debug.activeDebugSession?.configuration.handoverSession as string | undefined;
    if (active && descriptors.has(active)) return descriptors.get(active);
    const candidates = [...descriptors.values()];
    if (candidates.length === 1) return candidates[0];
    if (!candidates.length) { void vscode.window.showInformationMessage('No debugger session is active for this workspace.'); return; }
    const choice = await vscode.window.showQuickPick(candidates.map(s => ({ label: s.id, description: s.project, session: s })));
    return choice?.session;
  }
  context.subscriptions.push(log, status,
    vscode.debug.registerDebugAdapterTrackerFactory('debug-handover', {
      createDebugAdapterTracker(debugSession) {
        const scopes=new Map<number,{goroutine:number;frame:number}>();
        const requests=new Map<number,{goroutine:number;start:number}>();
        frameScopes.set(debugSession.id,scopes);
        return {
          onWillReceiveMessage(m) {if(m.type==='request' && m.command==='stackTrace') requests.set(m.seq,{goroutine:m.arguments.threadId,start:m.arguments.startFrame||0});},
          onDidSendMessage(m) {
            if(m.type==='event' && ['continued','terminated','stopped'].includes(m.event))scopes.clear();
            if(m.type==='response' && m.command==='stackTrace') {
              const scope=requests.get(m.request_seq);requests.delete(m.request_seq);
              if(scope && m.success) (m.body?.stackFrames||[]).forEach((f:{id:number},i:number)=>scopes.set(f.id,{goroutine:scope.goroutine,frame:scope.start+i}));
            }
          },
          onExit(){frameScopes.delete(debugSession.id);},
        };
      },
    }),
    vscode.debug.registerDebugAdapterDescriptorFactory('debug-handover', {
      async createDebugAdapterDescriptor(session): Promise<vscode.DebugAdapterServer> {
        const id = session.configuration.handoverSession as string;
        const s = descriptors.get(id);
        if (!s || !await folderFor(s)) throw new Error('Session does not belong to this trusted project.');
        const state = await request<State>(s, '/api/state?brief=1');
        if (state.capabilities?.executionTasks ? state.editorConnected || state.status !== 'paused' : (!pending(s, state) || state.handoverId !== session.configuration.handoverId)) {
          throw new Error('Handover changed or another editor is attached. Request a fresh handover.');
        }
        return new vscode.DebugAdapterServer(loopbackPort(state.dap), '127.0.0.1');
      },
    }),
    vscode.debug.onDidStartDebugSession(session => {
      if (session.type === 'debug-handover') live.set(session.configuration.handoverSession, session);
    }),
    vscode.debug.onDidTerminateDebugSession(session => {
      const id = session.configuration.handoverSession as string;
      if (live.get(id)?.id === session.id) live.delete(id);
      void scan();
    }),
    vscode.commands.registerCommand('debugHandover.attach', async (id?:string) => {await scan(); if(typeof id==='string'){await attachSelected(id);return;} const s=await selected(); if(s) await attachSelected(s.id);}),
    vscode.commands.registerCommand('debugHandover.reclaim', async () => {
      try {
        const s = await selected();
        if (!s) return;
        const state = await request<State>(s, '/api/state?brief=1');
        if (state.owner !== 'vscode') throw new Error('VS Code no longer owns this session.');
        // The broker ends only the frontend; never stopDebugging or terminate the target.
        const result = await request<{ notificationError?: string }>(s, '/api/action', {
          action: 'reclaim', actor: 'vscode', generation: state.generation, notify: Boolean(state.thread),
        });
        if (result.notificationError) throw new Error(`Control returned, but notification failed: ${result.notificationError}`);
        void vscode.window.showInformationMessage(state.binding ? `Control returned to ${state.binding.name}; handback event published.` : state.thread ? 'Control returned to Codex; task notification requested.' : 'Control returned. No notification integration is bound.');
        await scan();
      } catch (error) { void vscode.window.showErrorMessage(`AgentDebugger: ${message(error)}`); }
    }),
    vscode.commands.registerCommand('debugHandover.inspector', async () => {
      const s = await selected();
      if (s) await vscode.env.openExternal(vscode.Uri.parse(`${s.http}/${s.token ? '#' + s.token : ''}`));
    }),
    vscode.workspace.onDidChangeWorkspaceFolders(() => void scan()),
    vscode.workspace.onDidGrantWorkspaceTrust(() => void scan()),
  );
  async function attachSelected(id: string): Promise<void> {
    await scan();
    const s=descriptors.get(id);
    if(!s) throw new Error('Session is not available in this trusted workspace.');
    if(live.has(id)) return;
    const folder=await folderFor(s);
    if(!folder) throw new Error('Open the session project in this workspace first.');
    await attach(s,await request<State>(s,'/api/state?brief=1'),folder);
  }
  registerCollaboration(context, { sessions: async()=>{await scan();return [...descriptors.values()];}, selected, attach:attachSelected, scope:(id)=>{const selected=vscode.debug.activeStackItem; if(selected?.session.configuration.handoverSession!==id)return {}; if(selected instanceof vscode.DebugStackFrame){const scope=frameScopes.get(selected.session.id)?.get(selected.frameId);if(!scope)throw new Error("Selected frame changed; select it again.");return scope;} return {goroutine:selected.threadId,frame:0};}, log });
  const timer = setInterval(() => void scan(), 1000);
  context.subscriptions.push({ dispose() { disposed = true; clearInterval(timer); } });
  void scan();
}

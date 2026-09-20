import * as vscode from 'vscode';
import * as fs from 'node:fs/promises';
import * as path from 'node:path';
import { sessionDirectory, validateSession, validID, loopbackPort, pending, request } from './protocol';
import type { Session, State } from './protocol';

export function activate(context: vscode.ExtensionContext): void {
  const log = vscode.window.createOutputChannel('Debug Handover');
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 20);
  status.command = 'debugHandover.reclaim';
  const descriptors = new Map<string, Session>();
  const states = new Map<string, State>();
  const attempts = new Map<string, string>();
  const live = new Map<string, vscode.DebugSession>();
  let busy = false;
  let disposed = false;
  function message(error: unknown): string { return error instanceof Error ? error.message : String(error); }
  async function folderFor(s: Session): Promise<vscode.WorkspaceFolder | undefined> {
    if (!vscode.workspace.isTrusted) return undefined;
    const project = await fs.realpath(s.project);
    for (const folder of vscode.workspace.workspaceFolders || []) {
      if (folder.uri.scheme === 'file' && await fs.realpath(folder.uri.fsPath) === project) return folder;
    }
    return undefined;
  }
  function updateStatus(): void {
    const owned = [...states.values()].filter(s => s.owner === 'vscode' && s.status !== 'exited');
    if (!owned.length) { status.hide(); return; }
    const name = owned.length === 1 ? owned[0].binding?.name || 'Agent' : 'Agent';
    status.text = `$(debug-disconnect) Give control to ${name}`;
    status.tooltip = `Return the paused Go process to ${name}. Pause in the debugger first.`;
    status.show();
  }
  async function attach(s: Session, state: State, folder: vscode.WorkspaceFolder): Promise<void> {
    const key = `attempt.${s.id}`;
    attempts.set(s.id, state.handoverId);
    // Remember attempts across editor reloads: disconnecting must not auto-reattach.
    await context.workspaceState.update(key, state.handoverId);
    try {
      const started = await vscode.debug.startDebugging(folder, {
        type: 'debug-handover', name: `Debug Handover · ${s.id}`, request: 'attach',
        handoverSession: s.id, handoverId: state.handoverId,
        mode: 'remote', stopOnEntry: true, showGlobalVariables: false,
        suppressMultipleSessionWarning: true,
      });
      if (!started) throw new Error('VS Code could not start the attach session. Run Attach Pending Session to retry.');
      log.appendLine(`Attached to session ${s.id}`);
    } catch (error) {
      log.appendLine(`Attach ${s.id}: ${message(error)}`);
      void vscode.window.showErrorMessage(`Debug Handover: ${message(error)}`);
      try {
        const fresh = await request<State>(s, '/api/state?brief=1');
        await request(s, '/api/action', { action: 'editor-error', actor: 'vscode', generation: fresh.generation,
          handoverId: state.handoverId, error: message(error) });
      } catch { /* Ownership may have changed while VS Code was attaching. */ }
    }
  }
  async function scan(retry = false): Promise<void> {
    if (busy || disposed || !vscode.workspace.isTrusted) return;
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
    const candidates = [...descriptors.values()].filter(s => states.get(s.id)?.owner === 'vscode');
    if (candidates.length === 1) return candidates[0];
    if (!candidates.length) { void vscode.window.showInformationMessage('No VS Code handover session is active for this project.'); return; }
    const choice = await vscode.window.showQuickPick(candidates.map(s => ({ label: s.id, description: s.project, session: s })));
    return choice?.session;
  }
  context.subscriptions.push(log, status,
    vscode.debug.registerDebugAdapterDescriptorFactory('debug-handover', {
      async createDebugAdapterDescriptor(session): Promise<vscode.DebugAdapterServer> {
        const id = session.configuration.handoverSession as string;
        const s = descriptors.get(id);
        if (!s || !await folderFor(s)) throw new Error('Session does not belong to this trusted project.');
        const state = await request<State>(s, '/api/state?brief=1');
        if (!pending(s, state) || state.handoverId !== session.configuration.handoverId) {
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
    vscode.commands.registerCommand('debugHandover.attach', () => scan(true)),
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
      } catch (error) { void vscode.window.showErrorMessage(`Debug Handover: ${message(error)}`); }
    }),
    vscode.commands.registerCommand('debugHandover.inspector', async () => {
      const s = await selected();
      if (s) await vscode.env.openExternal(vscode.Uri.parse(`${s.http}/${s.token ? '#' + s.token : ''}`));
    }),
    vscode.workspace.onDidChangeWorkspaceFolders(() => void scan()),
    vscode.workspace.onDidGrantWorkspaceTrust(() => void scan()),
  );
  const timer = setInterval(() => void scan(), 1000);
  context.subscriptions.push({ dispose() { disposed = true; clearInterval(timer); } });
  void scan();
}

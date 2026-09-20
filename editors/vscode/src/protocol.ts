import * as path from 'node:path';
import * as os from 'node:os';

export interface Session {
  id: string; project: string; http: string; token: string; stopped?: boolean;
}
export interface State {
  id: string; project: string; owner: string; status: string; generation: number;
  handoverId: string; editorConnected: boolean; editorReady: boolean; dap: string;
  thread: string; error: string; state: { NextInProgress?: boolean };
}
export function sessionDirectory(): string {
  if (process.env.DEBUG_HANDOVER_HOME) return process.env.DEBUG_HANDOVER_HOME;
  const cache = process.platform === 'darwin' ? path.join(os.homedir(), 'Library', 'Caches')
    : process.platform === 'win32' ? process.env.LocalAppData || path.join(os.homedir(), 'AppData', 'Local')
      : process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache');
  return path.join(cache, 'debug-handover', 'sessions');
}
export function validID(id: string): boolean { return /^[a-f0-9]{10}$/.test(id); }
export function loopbackPort(endpoint: string): number {
  const match = /^127\.0\.0\.1:(\d+)$/.exec(endpoint);
  const port = match ? Number(match[1]) : 0;
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('Expected a local debugger endpoint');
  return port;
}
export function validateSession(value: unknown, id: string): Session {
  const s = value as Session;
  if (!s || !validID(id) || s.id !== id || typeof s.project !== 'string' || !path.isAbsolute(s.project)
      || !/^http:\/\/127\.0\.0\.1:\d+$/.test(s.http) || !/^[a-f0-9]{64}$/.test(s.token)) {
    throw new Error('Invalid local session descriptor');
  }
  loopbackPort(s.http.slice(7));
  return s;
}
export function pending(s: Session, v: State, attempted?: string): boolean {
  return !s.stopped && v.id === s.id && v.project === s.project && v.owner === 'vscode'
    && v.status === 'paused' && !v.state.NextInProgress && !v.editorConnected
    && /^[a-f0-9]{16}$/.test(v.handoverId) && v.handoverId !== attempted;
}
export async function request<T>(s: Session, route: '/api/state?brief=1' | '/api/action', body?: unknown): Promise<T> {
  validateSession(s, s.id);
  const response = await fetch(s.http + route, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { Authorization: `Bearer ${s.token}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    redirect: 'error', signal: AbortSignal.timeout(8000),
  });
  const value = await response.json() as T & { error?: string };
  if (!response.ok) throw new Error(value.error || `Broker returned ${response.status}`);
  return value;
}

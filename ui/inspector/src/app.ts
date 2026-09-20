import hljs from 'highlight.js/lib/core';
import go from 'highlight.js/lib/languages/go';

hljs.registerLanguage('go', go);

interface Variable {
  name: string;
  type: string;
  value?: string;
  unreadable?: string;
  len?: number;
  cap?: number;
  children?: Variable[];
}

interface StackFrame {
  file: string;
  line: number;
  function?: { name: string };
  Arguments?: Variable[];
  Locals?: Variable[];
}

interface Source {
  file: string;
  line: number;
  start: number;
  lines: string[];
}

interface Breakpoint {
  id: number;
  name?: string;
  file: string;
  line: number;
  Cond?: string;
  HitCond?: string;
}

interface Snapshot {
  id: string;
  owner: 'codex' | 'zed' | 'vscode';
  editor?: 'zed' | 'vscode';
  editorConnected?: boolean;
  editorReady?: boolean;
  generation: number;
  status: 'paused' | 'running' | 'exited';
  state: { Pid?: number; stopReason?: string };
  zedConnected: boolean;
  project: string;
  binary: string;
  label: string;
  goroutine?: number;
  goroutines?: { id: number }[];
  frame: number;
  frames?: StackFrame[];
  source?: Source;
  sourceNewerThanBinary?: boolean;
  breakpoints?: Breakpoint[];
  error?: string;
  inspectionError?: string;
  thread?: string;
  notification?: { id: string; kind: string; status: 'pending' | 'sending' | 'queued' | 'failed' | 'unknown'; error?: string };
  sourceIdentity?: { match: string; changedSinceStart?: boolean; binaryChanged?: boolean };
  watches?: Evaluation[];
}

interface Evaluation { expression: string; value?: Variable; error?: string; generation?: number; goroutine?: number; frame?: number }

type Action = 'continue' | 'next' | 'step' | 'stepout' | 'pause' | 'break' | 'clear' | 'handover' | 'reclaim' | 'stop' | 'eval' | 'watch' | 'unwatch' | 'retry-notification';
interface ActionOptions {
	 editor?: string;
  breakpoint?: number;
  open?: boolean;
  file?: string;
  line?: number;
  condition?: string;
  notify?: boolean;
  expression?: string;
  goroutine?: number;
  frame?: number;
  depth?: number;
  count?: number;
}
interface ActionRequest extends ActionOptions { action: Action; generation: number }
interface ActionResult extends Partial<Evaluation> { instructions?: string; message?: string; notificationError?: string; persistenceError?: string; cleanupError?: string; openError?: string }

function $<T extends HTMLElement = HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) throw new Error(`Missing UI element: ${id}`);
  return element as T;
}

function node<K extends keyof HTMLElementTagNameMap>(tag: K, text?: string, cls?: string): HTMLElementTagNameMap[K] {
  const element = document.createElement(tag);
  if (text !== undefined) element.textContent = text;
  if (cls) element.className = cls;
  return element;
}

const token = location.hash.slice(1) || sessionStorage.getItem('debug-handover-token');
if (token) sessionStorage.setItem('debug-handover-token', token);
history.replaceState(null, '', location.pathname);

let snapshot: Snapshot | undefined;
let goroutine = 0;
let frame = 0;
let busy = false;
let fetching = false;
let renderKey = '';
let disconnected = false;
let evaluated: Evaluation | undefined;
const basename = (path: string) => path.split('/').pop() || path;
const errorMessage = (error: unknown) => error instanceof Error ? error.message : String(error);

function message(id: string, text?: string): void {
  $(id).textContent = text || '';
  $(id).hidden = !text;
}

async function request<T>(path: string, body?: ActionRequest): Promise<T> {
  const response = await fetch('/api/' + path, {
    method: body ? 'POST' : 'GET',
    headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || response.statusText);
  return data as T;
}

function variable(value: Variable): HTMLElement {
  const children = value.children || [];
  const root = node(children.length ? 'details' : 'div', undefined, 'variable');
  const row = children.length ? node('summary') : root;
  row.append(
    node('span', value.name || '·', 'name'),
    node('span', value.unreadable ? `(unavailable: ${value.unreadable})` : value.value || (value.len !== undefined && (value.type.startsWith('[]') || value.type.startsWith('map[') || value.type.startsWith('[')) ? `len ${value.len}${value.cap ? ` · cap ${value.cap}` : ''}` : children.length ? '{…}' : '—'), 'value'),
    node('span', value.type || '', 'type'),
  );
  if (children.length) {
    const list = node('div', undefined, 'children');
    children.forEach((child, index) => list.append(variable({ ...child, name: child.name || (value.type.startsWith('[') ? `[${index}]` : value.type.startsWith('map[') ? `${index % 2 ? 'value' : 'key'} ${Math.floor(index / 2)}` : '·') })));
    root.append(row, list);
    if (value.len && value.len > children.length && !value.type.startsWith('map[')) list.append(node('p', `${children.length} of ${value.len} items loaded. Use a slice expression for another range.`, 'empty'));
  }
  return root;
}

function renderSource(source: Source): void {
  $('filename').textContent = source.file;
  $('filename').title = source.file;
  $('line').textContent = 'line ' + source.line;

  const content = node('div', undefined, 'sourceContent');
  const highlights = node('div', undefined, 'sourceHighlights');
  const numbers = node('div', undefined, 'sourceNumbers');
  highlights.setAttribute('aria-hidden', 'true');
  numbers.setAttribute('aria-hidden', 'true');
  source.lines.forEach((_, index) => {
    const line = source.start + index;
    const current = line === source.line ? ' current' : '';
    highlights.append(node('div', undefined, 'sourceHighlight' + current));
    numbers.append(node('div', String(line), 'sourceNumber' + current));
  });

  const pre = node('pre', undefined, 'sourceCode');
  const code = node('code');
  const text = source.lines.join('\n');
  if (source.file.endsWith('.go')) {
    // Highlight the entire snippet so multiline comments and strings retain
    // their token boundaries. Highlight.js escapes source text before markup.
    code.innerHTML = hljs.highlight(text, { language: 'go', ignoreIllegals: true }).value;
    code.className = 'hljs language-go';
  } else {
    code.textContent = text;
  }
  pre.append(code);
  content.append(highlights, numbers, pre);
  $('source').append(content);

  // Scroll within the source pane without moving the surrounding inspector.
  const current = highlights.querySelector<HTMLElement>('.current');
  if (current) $('source').scrollTop = Math.max(0, current.offsetTop - $('source').clientHeight / 2);
}

function render(state: Snapshot): void {
  const paused = state.status === 'paused';
  const codex = state.owner === 'codex';
  $('status').textContent = state.status[0].toUpperCase() + state.status.slice(1);
  $('status').className = 'badge ' + state.status;
  $('session').textContent = `${state.id} · PID ${state.state.Pid || '—'} · ${basename(state.project)}`;
  const editorSelect = $<HTMLSelectElement>('editor');
  if (!editorSelect.dataset.chosen) editorSelect.value = state.editor || 'zed';
  editorSelect.disabled = busy || !codex;
  const editor = state.owner === 'vscode' ? 'VS Code' : 'Zed';
  $('owner').textContent = codex ? 'Codex has control' : `You have control in ${editor}`;
  $('connection').textContent = codex
    ? 'You and Codex can inspect, set breakpoints, and step here.'
    : (state.editorConnected ?? state.zedConnected)
      ? `${editor} is ${state.editorReady === false ? 'finishing attachment' : 'connected'}. This inspector follows the same session.`
      : state.owner === 'vscode' ? 'Waiting for the Debug Handover extension in VS Code. Use Attach Pending Session to retry.'
      : state.thread ? `Waiting for Codex to attach Zed to “${state.label}”.` : `Ready for Zed. Press F4 and choose “${state.label}”.`;
  const delivery = state.notification;
  $('delivery').textContent = delivery
    ? delivery.status === 'queued' ? 'Notification queued to your Codex task.'
      : delivery.status === 'failed' || delivery.status === 'unknown' ? `Codex notification ${delivery.status}: ${delivery.error || 'Check the task before retrying.'}`
      : 'Sending a notification to your Codex task…'
    : state.thread ? 'Inspector handovers notify your Codex task automatically.' : 'No Codex task is linked. Handover requires manual attachment or a message to Codex.';
  const retry = $<HTMLButtonElement>('retryNotification');
  retry.hidden = !delivery || !['failed', 'unknown'].includes(delivery.status);
  retry.disabled = busy;
  const handover = $<HTMLButtonElement>('handover');
  handover.textContent = codex ? `Hand over to ${editorSelect.value === 'vscode' ? 'VS Code' : 'Zed'} ↗` : 'Give control to Codex';
  handover.disabled = busy || !paused;
  document.querySelectorAll<HTMLButtonElement>('[data-action]').forEach(button => {
    button.disabled = busy || !codex || (button.dataset.action === 'pause' ? state.status !== 'running' : !paused);
  });
  $('breakForm').querySelectorAll<HTMLInputElement | HTMLButtonElement>('input,button').forEach(element => {
    element.disabled = busy || !codex || !paused;
  });
  $<HTMLButtonElement>('stop').disabled = busy;
  $('evalForm').querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement>('input,button,select').forEach(element => { element.disabled = busy || !paused; });
  $('stopReason').textContent = paused ? state.state.stopReason || 'Paused at launch' : state.status === 'running' ? 'Waiting for next stop…' : 'Program exited';
  $('binary').textContent = 'BINARY  ' + state.binary;
  if (state.error || state.inspectionError) message('error', state.error || state.inspectionError);
  const identity = state.sourceIdentity;
  message('sourceWarning', identity?.binaryChanged ? 'The executable on disk changed. This process still runs the original binary.'
    : identity?.changedSinceStart ? 'This source file changed since the session started. Line numbers may no longer match the running code.'
      : identity?.match === 'mismatch' ? 'The executable build revision or working tree differs from the current source.'
        : state.sourceNewerThanBinary ? 'This source is newer than the executable. The source/build match is unverified.' : '');

  if (evaluated && (evaluated.generation !== state.generation || evaluated.frame !== state.frame || evaluated.goroutine !== state.goroutine || !paused)) { evaluated = undefined; $('evaluation').replaceChildren(); }

  const key = [state.generation, state.status, state.goroutine, state.frame, state.owner].join(':');
  if (key === renderKey) return;
  renderKey = key;
  for (const id of ['frames', 'source', 'locals', 'breakpoints', 'goroutines', 'watches']) $(id).replaceChildren();
  $('filename').textContent = paused ? 'No source available' : 'Source is available when paused';
  $('line').textContent = '—';
  $('frameName').textContent = '';
  if (!paused) {
    $('frames').append(node('p', 'Execution is ' + state.status + '.', 'empty'));
    $('source').append(node('p', 'The previous stack and variables have been cleared.'));
    $('breakpoints').append(node('p', 'Pause to inspect breakpoints.', 'empty'));
    return;
  }

  (state.goroutines || []).forEach(g => {
    const option = node('option', 'Goroutine ' + g.id);
    option.value = String(g.id);
    option.selected = g.id === state.goroutine;
    $('goroutines').append(option);
  });
  (state.frames || []).forEach((f, index) => {
    const button = node('button', undefined, 'frame' + (index === state.frame ? ' selected' : ''));
    button.append(node('strong', f.function?.name || '(unknown)'), node('small', `#${index}  ${basename(f.file)}:${f.line}`));
    button.onclick = () => { frame = index; void refresh(); };
    $('frames').append(button);
  });
  if (!state.frames?.length) $('frames').append(node('p', 'No Go stack at this location.', 'empty'));
  if (state.source) renderSource(state.source);

  const selected = state.frames?.[state.frame];
  $('frameName').textContent = selected?.function?.name || '';
  const variables = [...(selected?.Arguments || []), ...(selected?.Locals || [])];
  variables.forEach(value => {
    const row = variable(value);
    const inspect = node('button', 'Inspect', 'inspect');
    inspect.disabled = busy || value.name.startsWith('~');
    inspect.onclick = () => { $<HTMLInputElement>('expression').value = value.name; void evaluate(); };
    (row.querySelector('summary') || row).append(inspect); $('locals').append(row);
  });
  if (!variables.length) $('locals').append(node('p', 'No readable locals in this frame.', 'empty'));
  for (const watch of state.watches || []) {
    const row = node('div', undefined, 'watch');
    row.append(node('strong', watch.expression));
    const remove = node('button', 'Remove'); remove.disabled = busy;
    remove.onclick = () => { void act('unwatch', { expression: watch.expression }); };
    row.append(remove);
    row.append(watch.value ? variable(watch.value) : node('p', watch.error || 'Unavailable in this frame.', 'empty'));
    $('watches').append(row);
  }
  if (!state.watches?.length) $('watches').append(node('p', 'Add an expression to follow its value across stops.', 'empty'));

  const breakpoints = (state.breakpoints || []).filter(bp => bp.id > 0);
  breakpoints.forEach(bp => {
    const row = node('div', undefined, 'bp');
    row.append(
      node('span', '●', 'red'),
      node('span', `${basename(bp.file)}:${bp.line} · ${bp.name?.startsWith('codex') ? 'Codex' : 'Editor'}`, 'place'),
      node('span', [bp.Cond, bp.HitCond ? 'hits ' + bp.HitCond : ''].filter(Boolean).join(' · '), 'condition'),
    );
    const remove = node('button', 'Remove');
    remove.disabled = !codex || busy;
    remove.onclick = () => { void act('clear', { breakpoint: bp.id }); };
    row.append(remove);
    $('breakpoints').append(row);
  });
  if (!breakpoints.length) $('breakpoints').append(node('p', 'No user breakpoints.', 'empty'));
}

async function refresh(): Promise<void> {
  if (fetching) return;
  fetching = true;
  try {
    snapshot = await request<Snapshot>(`state?goroutine=${goroutine}&frame=${frame}`);
    if (disconnected) { renderKey = ''; disconnected = false; message('error', ''); }
    render(snapshot);
  } catch (error) {
    disconnected = true; evaluated = undefined; renderKey = '';
    for (const id of ['frames', 'locals', 'evaluation', 'watches', 'source']) $(id).replaceChildren();
    message('error', errorMessage(error));
    $('status').textContent = 'Disconnected';
    document.querySelectorAll('button').forEach(button => { button.disabled = true; });
  } finally {
    fetching = false;
  }
}

async function act(action: Action, extra: ActionOptions = {}): Promise<void> {
  if (busy || !snapshot) return;
  busy = true;
  render(snapshot);
  message('error', '');
  try {
    const result = await request<ActionResult>('action', { action, generation: snapshot.generation, ...extra });
    message('notice', result.instructions || result.message || (action === 'break' ? 'Breakpoint set in Delve.' : ''));
    message('error', result.openError || result.notificationError || result.persistenceError || result.cleanupError);
    if (action === 'eval' && result.value) {
      evaluated = { expression: extra.expression || '', value: result.value, generation: result.generation, goroutine: result.goroutine, frame: result.frame };
      $('evaluation').replaceChildren(node('strong', evaluated.expression), variable(result.value));
    }
    if (action === 'stop') {
      message('notice', 'Session ended. The debuggee and debugger have been stopped.');
      $('status').textContent = 'Ended';
      clearInterval(timer);
      return;
    }
    if (['continue', 'next', 'step', 'stepout', 'reclaim'].includes(action)) { frame = 0; goroutine = 0; }
  } catch (error) {
    message('error', errorMessage(error));
  } finally {
    busy = false;
    renderKey = '';
  }
  await refresh();
}

document.querySelectorAll<HTMLButtonElement>('[data-action]').forEach(button => {
  button.onclick = () => { void act(button.dataset.action as Action); };
});
$('handover').onclick = () => {
  if (snapshot) void act(snapshot.owner === 'codex' ? 'handover' : 'reclaim', { open: true, editor: $<HTMLSelectElement>('editor').value, notify: Boolean(snapshot.thread) });
};
async function evaluate(): Promise<void> {
  if (!snapshot) return;
  await act('eval', { expression: $<HTMLInputElement>('expression').value, goroutine: snapshot.goroutine, frame: snapshot.frame, depth: Number($<HTMLSelectElement>('depth').value), count: 128 });
}
$('evalForm').onsubmit = event => { event.preventDefault(); void evaluate(); };
$('addWatch').onclick = () => { void act('watch', { expression: $<HTMLInputElement>('expression').value }); };
$('retryNotification').onclick = () => {
  if (snapshot?.notification?.status === 'unknown' && !confirm('Delivery is uncertain. Check your Codex task first. Retry anyway?')) return;
  void act('retry-notification');
};
$('goroutines').onchange = () => { goroutine = Number($<HTMLSelectElement>('goroutines').value); frame = 0; void refresh(); };
$('breakForm').onsubmit = event => {
  event.preventDefault();
  void act('break', {
    file: $<HTMLInputElement>('breakFile').value,
    line: Number($<HTMLInputElement>('breakLine').value),
    condition: $<HTMLInputElement>('breakCondition').value,
  });
};
$('stop').onclick = () => {
  if (confirm('End this session and terminate its program?')) void act('stop');
};
const timer = setInterval(() => { void refresh(); }, 800);
void refresh();

$('editor').onchange = () => { $('editor').dataset.chosen = 'true'; if (snapshot) render(snapshot); };

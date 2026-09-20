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
  kind?: number;
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
  owner: 'agent' | 'codex' | 'browser' | 'zed' | 'vscode';
  binding?: {id: string; name: string; revision: number};
  editor?: 'browser' | 'zed' | 'vscode';
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
  notification?: { id: string; kind: string; status: 'pending' | 'sending' | 'queued' | 'acknowledged' | 'failed' | 'unknown'; error?: string };
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
interface ActionRequest extends ActionOptions { action: Action; generation: number; actor?: string }
interface ActionResult extends Partial<Evaluation> { Breakpoint?: {file:string;line:number}; instructions?: string; message?: string; notificationError?: string; persistenceError?: string; cleanupError?: string; openError?: string }

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

const token = location.hash.slice(1); // Only older broker URLs carry a token.
const previewSession = new URLSearchParams(location.search).get('session');
history.replaceState(null, '', location.pathname + location.search);

let snapshot: Snapshot | undefined;
let goroutine = 0;
let frame = 0;
let busy = false;
let fetching = false;
let renderKey = '';
let sourceKey = '';
let displayedSource: Source | undefined;
let lastPid: number | undefined;
const openSources = new Map<string, Source>();
const sourceScroll = new Map<string, {top:number;left:number}>();
let activeFile = '';
let lastLocation = '';
let renderedLocation = '';
let navigationTarget: {file:string;line:number} | undefined;
let fileRequest = 0;
let knownFiles: string[] = [];
let disconnected = false;
let evaluated: Evaluation | undefined;
const basename = (path: string) => path.split('/').pop() || path;
const errorMessage = (error: unknown) => error instanceof Error ? error.message : String(error);

function message(id: string, text?: string): void {
  $(id).textContent = text || '';
  $(id).hidden = !text;
}

async function request<T>(path: string, body?: ActionRequest | {id: string; confirmed: boolean}): Promise<T> {
  const response = await fetch('/api/' + path + (previewSession ? (path.includes('?') ? '&' : '?') + 'session=' + encodeURIComponent(previewSession) : ''), {
    method: body ? 'POST' : 'GET',
    headers: { ...(token ? { Authorization: 'Bearer ' + token } : {}), 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || response.statusText);
  return data as T;
}

function pinIcon(): SVGSVGElement {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  path.setAttribute('d', 'M8 3h8l-1 7 4 4v2H5v-2l4-4-1-7Z M12 16v6');
  svg.append(path);
  return svg;
}

function incomplete(value: Variable): boolean {
  if (value.unreadable) return false;
  const children = value.children || [];
  const count = value.kind === 21 ? children.length / 2 : children.length;
  return ([17, 21, 23, 25].includes(value.kind || 0) && (value.len || 0) > count)
    || (value.kind === 24 && (value.len || 0) > new TextEncoder().encode(value.value || '').length)
    || (value.kind === 22 && !children.length && value.value !== 'nil')
    || children.some(incomplete);
}

function inspectable(value: Variable, expression: string, loaded = false): HTMLElement {
  const root = node('div', undefined, 'inspectable');
  root.append(variable(value));
  if (incomplete(value) && !expression.startsWith('~')) {
    const more = node('button', loaded ? 'Value truncated · use a narrower expression' : 'Load more', 'loadMore');
    if (loaded) { root.append(node('p', more.textContent || '', 'empty')); return root; }
    const scope = snapshot && { generation: snapshot.generation, goroutine: snapshot.goroutine, frame: snapshot.frame };
    more.onclick = async () => {
      if (!scope || busy || snapshot?.status !== 'paused' || snapshot.generation !== scope.generation || snapshot.frame !== scope.frame || snapshot.goroutine !== scope.goroutine) return;
      more.disabled = true;
      more.textContent = 'Loading…';
      try {
        const result = await request<ActionResult>('action', { action: 'eval', actor: snapshot.owner === 'codex' ? 'agent' : 'browser', expression, ...scope, depth: 6, count: 128 });
        if (!root.isConnected || snapshot?.status !== 'paused' || snapshot.generation !== scope.generation || snapshot.frame !== scope.frame || snapshot.goroutine !== scope.goroutine) return;
        if (result.value) {
          const replacement = inspectable({ ...result.value, name: value.name }, expression, true);
          const details = replacement.querySelector('details');
          if (details) details.open = true;
          root.replaceWith(replacement);
        }
      } catch (error) { message('error', errorMessage(error)); }
      finally { more.disabled = false; more.textContent = 'Load more'; }
    };
    root.append(more);
  }
  return root;
}

function variable(value: Variable): HTMLElement {
  const children = value.children || [];
  const root = node(children.length ? 'details' : 'div', undefined, 'variable');
  const row = node(children.length ? 'summary' : 'div', undefined, 'variableRow');
  row.append(
    node('span', value.name || '·', 'name'),
    node('span', value.unreadable ? `(unavailable: ${value.unreadable})` : value.value || (value.len !== undefined && (value.type.startsWith('[]') || value.type.startsWith('map[') || value.type.startsWith('[')) ? `len ${value.len}${value.cap ? ` · cap ${value.cap}` : ''}` : children.length ? '{…}' : '—'), 'value'),
    node('span', value.type || '', 'type'),
  );
  root.append(row);
  if (children.length) {
    const list = node('div', undefined, 'children');
    children.forEach((child, index) => list.append(variable({ ...child, name: child.name || (value.type.startsWith('[') ? `[${index}]` : value.type.startsWith('map[') ? `${index % 2 ? 'value' : 'key'} ${Math.floor(index / 2)}` : '·') })));
    root.append(list);
    if (value.len && value.len > children.length && !value.type.startsWith('map[')) list.append(node('p', `${children.length} of ${value.len} items loaded. Use a slice expression for another range.`, 'empty'));
  }
  return root;
}

function renderSource(source: Source): void {
  $('filename').textContent = source.file;
  $('filename').title = source.file;
  $('line').textContent = source.line ? 'line ' + source.line : 'Browsing';

  const location = source.file + ':' + source.line;
  const revealStop = location !== renderedLocation;
  renderedLocation = location;
  const previous = displayedSource;
  const reusable = previous && previous.file === source.file
    && source.line >= previous.start + 3 && source.line < previous.start + previous.lines.length - 3
    && source.lines.every((line, i) => {
      const oldIndex = source.start + i - previous.start;
      return oldIndex < 0 || oldIndex >= previous.lines.length || previous.lines[oldIndex] === line;
    });
  if (reusable) source = { ...previous, line: source.line };
  const key = JSON.stringify([source.file, source.start, source.lines]);
  const selectLine = () => {
    $('source').querySelectorAll<HTMLElement>('.sourceHighlight, .sourceNumber').forEach(el => {
      el.classList.toggle('current', Number(el.dataset.line) === source.line);
    });
    const current = $('source').querySelector<HTMLElement>('.sourceHighlight.current');
    const pane = $('source');
    if (revealStop && current && (current.offsetTop < pane.scrollTop || current.offsetTop + current.offsetHeight > pane.scrollTop + pane.clientHeight)) {
      pane.scrollTop = Math.max(0, current.offsetTop - pane.clientHeight / 2);
    }
  };
  if (key === sourceKey) { selectLine(); paintBreakpoints(); return; }
  displayedSource = source;
  sourceKey = key;
  const pane = $('source');
  const left = pane.scrollLeft;
  const content = node('div', undefined, 'sourceContent');
  const highlights = node('div', undefined, 'sourceHighlights');
  const numbers = node('div', undefined, 'sourceNumbers');
  highlights.setAttribute('aria-hidden', 'true');

  source.lines.forEach((_, index) => {
    const line = source.start + index;
    const current = line === source.line ? ' current' : '';
    const highlight = node('div', undefined, 'sourceHighlight' + current);
    const number = node('div', undefined, 'sourceNumber' + current);
    const gutter = node('button', '', 'breakpointGutter');
    gutter.dataset.line = String(line);
    number.append(gutter, node('span', String(line), 'lineLabel'));
    gutter.setAttribute('aria-label', `Toggle breakpoint at line ${line}`);
    gutter.onclick = () => { void gutterBreakpoint(source.file,line,false); };
    gutter.oncontextmenu = event => { event.preventDefault(); void gutterBreakpoint(source.file,line,true); };
    highlight.dataset.line = number.dataset.line = String(line);
    highlights.append(highlight);
    numbers.append(number);
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
  pane.replaceChildren(content);
  pane.scrollLeft = left;

  paintBreakpoints();
  // Scroll within the source pane without moving the surrounding inspector.
  const current = highlights.querySelector<HTMLElement>('.current');
  if (current) $('source').scrollTop = Math.max(0, current.offsetTop - $('source').clientHeight / 2);
}

function render(state: Snapshot): void {
  const paused = state.status === 'paused';
  document.querySelector('main')!.dataset.executing = String(busy || state.status === 'running');
  lastPid = state.state.Pid || lastPid;
  const codex = state.owner === 'browser' || state.owner === 'codex';
  const agentName = state.binding?.name || 'Agent';
  $('status').textContent = state.status[0].toUpperCase() + state.status.slice(1);
  $('status').className = 'badge ' + state.status;
  $('session').textContent = `${state.id} · PID ${lastPid || '—'} · ${basename(state.project)}`;
  const editorSelect = $<HTMLSelectElement>('editor');
  if (!editorSelect.dataset.chosen) editorSelect.value = state.editor || 'browser';
  editorSelect.disabled = busy || !paused || (state.owner !== 'agent' && state.owner !== 'codex');
  const editor = state.owner === 'vscode' ? 'VS Code' : state.owner === 'browser' ? 'browser' : 'Zed';
  $('owner').textContent = state.owner === 'agent' || state.owner === 'codex' ? `${agentName} has control` : `You have control in ${editor}`;
  $('connection').textContent = state.owner === 'browser' ? 'Use the controls here, then return control to your agent.'
    : state.owner === 'agent' ? 'Choose browser or an editor to take control at this pause.'
    : state.editorConnected ? `${editor} is connected to the same process.` : `Attach ${editor} to the paused session. Zed: F4 → ${state.label}.`;
  const delivery = state.notification;
  $('delivery').textContent = delivery ? `Handback ${delivery.status}${delivery.error ? ': ' + delivery.error : ''}`
    : `Handback events go to the bound ${agentName} listener when available.`;
  const retry = $<HTMLButtonElement>('retryNotification');
  retry.hidden = !delivery || !['failed', 'unknown'].includes(delivery.status);
  retry.disabled = busy;
  const handover = $<HTMLButtonElement>('handover');
  handover.textContent = state.owner === 'agent' || state.owner === 'codex' ? `Take control in ${editorSelect.value}` : `Return to ${agentName}`;
  handover.disabled = busy || !paused;
  $<HTMLButtonElement>('takeBrowser').hidden = state.owner === 'browser';
  $<HTMLButtonElement>('takeBrowser').disabled = busy || !paused;
  document.querySelectorAll<HTMLButtonElement>('[data-action]').forEach(button => {
    button.disabled = busy || !codex || (button.dataset.action === 'pause' ? state.status !== 'running' : !paused);
  });
  $('breakForm').querySelectorAll<HTMLInputElement | HTMLButtonElement>('input,button').forEach(element => {
    element.disabled = busy || !paused;
  });
  $<HTMLButtonElement>('stop').disabled = busy || !codex;
  $('evalForm').querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement>('input,button,select').forEach(element => { element.disabled = busy || !paused; });
  $<HTMLButtonElement>('addWatch').disabled = busy || !paused || !codex;
  $('stopReason').textContent = paused ? state.state.stopReason || 'Paused at launch' : state.status === 'running' ? 'Running · showing last pause' : 'Program exited';
  $('binary').textContent = 'BINARY  ' + state.binary;
  if (state.error || state.inspectionError) message('error', state.error || state.inspectionError);
  const identity = state.sourceIdentity;
  if (paused) message('sourceWarning', identity?.binaryChanged ? 'The executable on disk changed. This process still runs the original binary.'
    : identity?.changedSinceStart ? 'This source file changed since the session started. Line numbers may no longer match the running code.'
      : identity?.match === 'mismatch' ? 'The executable build revision or working tree differs from the current source.'
        : state.sourceNewerThanBinary ? 'This source is newer than the executable. The source/build match is unverified.' : '');

  if (evaluated && (evaluated.generation !== state.generation || evaluated.frame !== state.frame || evaluated.goroutine !== state.goroutine || !paused)) { evaluated = undefined; $('evaluation').replaceChildren(); }

  const isPinned = Boolean(evaluated && state.watches?.some(watch => watch.expression === evaluated?.expression));
  $('addWatch').hidden = !evaluated || isPinned;
  $('evaluation').hidden = isPinned;
  $('evaluation').querySelectorAll<HTMLButtonElement>('button').forEach(button => { button.disabled = busy || !paused; });

  // Keep the last stop visible during execution. Disable its interactions and
  // label it as stale instead of collapsing and rebuilding every panel.
  const disableInspection = () => {
    $('frames').querySelectorAll<HTMLButtonElement>('button').forEach(b => { b.disabled = busy || !paused; });
    $('locals').querySelectorAll<HTMLButtonElement>('button').forEach(b => { b.disabled = busy || !paused || b.dataset.unavailable === 'true' || (b.classList.contains('pinButton') && !codex); });
    for (const id of ['watches']) $(id).querySelectorAll<HTMLButtonElement>('button').forEach(b => { b.disabled = busy || !paused || !codex; });
    $<HTMLSelectElement>('goroutines').disabled = busy || !paused;
  };
  disableInspection();
  $('breakpoints').querySelectorAll<HTMLButtonElement>('button:not(.breakpointLink)').forEach(b => { b.disabled = busy || !paused; });
  if (!paused) return;
  const key = JSON.stringify([state.goroutine, state.frame, state.frames, state.source, state.breakpoints, state.goroutines, state.watches, agentName]);
  paintBreakpoints();
  if (key === renderKey) return;
  renderKey = key;
  const expanded = new Set(Array.from($('locals').querySelectorAll<HTMLDetailsElement>('details[open]')).map(d => d.dataset.path));
  const positions = new Map(['frames', 'locals', 'watches'].map(id => [id, $(id).scrollTop]));
  for (const id of ['frames', 'locals', 'breakpoints', 'goroutines', 'watches']) $(id).replaceChildren();
  if (!state.source && !activeFile) {
    sourceKey = '';
    displayedSource = undefined;
    $('source').replaceChildren(node('p', 'No source available at this location.'));
    $('filename').textContent = 'No source available';
    $('line').textContent = '—';
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
  if (state.source) {
    const location = [state.source.file,state.source.line,state.frame,state.goroutine].join(':');
    if (!openSources.has(state.source.file)) openSources.set(state.source.file,state.source);
    else if (openSources.get(state.source.file)!.start !== 1) openSources.set(state.source.file,state.source);
    if (location !== lastLocation) { navigationTarget=undefined; rememberScroll(); activeFile=state.source.file; lastLocation=location; }
  }
  showActiveSource();

  const selected = state.frames?.[state.frame];
  $('frameName').textContent = selected?.function?.name || '';
  const variables = [...(selected?.Arguments || []), ...(selected?.Locals || [])];
  variables.forEach(value => {
    const row = node('div', undefined, 'localVariable');
    const pinned = Boolean(state.watches?.some(watch => watch.expression === value.name));
    const pin = node('button', undefined, 'pinButton');
    const unavailable = !value.name || value.name.startsWith('~');
    pin.dataset.unavailable = String(unavailable);
    pin.disabled = busy || !paused || !codex || unavailable;
    pin.setAttribute('aria-pressed', String(pinned));
    pin.setAttribute('aria-label', `${pinned ? 'Unpin' : 'Pin'} ${value.name}`);
    pin.title = unavailable ? 'This compiler-generated value cannot be pinned' : `${pinned ? 'Unpin' : 'Pin'} ${value.name}`;
    pin.append(pinIcon());
    pin.onclick = () => { void act(pinned ? 'unwatch' : 'watch', { expression: value.name }); };
    row.append(pin, inspectable(value, value.name));
    $('locals').append(row);
  });
  if (!variables.length) $('locals').append(node('p', 'No readable locals in this frame.', 'empty'));
  for (const watch of state.watches || []) {
    const row = node('div', undefined, 'watch');
    const remove = node('button', undefined, 'pinButton'); remove.disabled = busy;
    remove.setAttribute('aria-pressed', 'true');
    remove.append(pinIcon());
    remove.setAttribute('aria-label', `Unpin ${watch.expression}`);
    remove.title = `Unpin ${watch.expression}`;
    remove.onclick = () => { void act('unwatch', { expression: watch.expression }); };
    row.append(remove);
    row.append(watch.value ? inspectable({ ...watch.value, name: watch.expression }, watch.expression) : node('p', `${watch.expression}: ${watch.error || 'Unavailable in this frame.'}`, 'empty'));
    $('watches').append(row);
  }


  const breakpoints = (state.breakpoints || []).filter(bp => bp.id > 0).slice().sort((a,b)=>a.file.localeCompare(b.file)||a.line-b.line||a.id-b.id);
  breakpoints.forEach(bp => {
    const row = node('div', undefined, 'bp');
    const link = node('button', `${basename(bp.file)}:${bp.line}`, 'place breakpointLink');
    link.title = bp.file + ':' + bp.line;
    link.onclick = () => { void openSourceFile(bp.file,bp.line); };
    row.append(
      node('span', '●', 'red'),
      link,
      node('span', [bp.Cond, bp.HitCond ? 'hits ' + bp.HitCond : ''].filter(Boolean).join(' · '), 'condition'),
    );
    const remove = node('button', 'Remove');
    remove.disabled = busy || !paused;
    remove.onclick = () => { void act('clear', { breakpoint: bp.id }); };
    row.append(remove);
    $('breakpoints').append(row);
  });
  if (!breakpoints.length) $('breakpoints').append(node('p', 'No user breakpoints.', 'empty'));
  const restoreDetails = (parent: Element, prefix: string) => {
    Array.from(parent.children).forEach((child, index) => {
      const path = prefix + '/' + index;
      if (child instanceof HTMLDetailsElement) { child.dataset.path = path; child.open = expanded.has(path); }
      restoreDetails(child, path);
    });
  };
  restoreDetails($('locals'), `${state.goroutine}:${state.frame}:${selected?.function?.name}`);
  positions.forEach((top, id) => { $(id).scrollTop = top; });
  disableInspection();
}

async function refresh(): Promise<void> {
  if (fetching) return;
  fetching = true;
  try {
    snapshot = await request<Snapshot>(`state?goroutine=${goroutine}&frame=${frame}`);
    if (disconnected) { disconnected = false; message('error', ''); }
    render(snapshot);
  } catch (error) {
    disconnected = true;
    $('stopReason').textContent = 'Disconnected · showing last pause';
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
    const result = await request<ActionResult>('action', { action, actor: snapshot.owner === 'codex' ? 'agent' : 'browser', generation: snapshot.generation, ...extra });
    message('notice', result.instructions || result.message || (result.Breakpoint ? `Breakpoint set at ${basename(result.Breakpoint.file)}:${result.Breakpoint.line}` : action === 'break' ? 'Breakpoint set in Delve.' : ''));
    message('error', result.openError || result.notificationError || result.persistenceError || result.cleanupError);
    if (action === 'eval' && result.value) {
      evaluated = { expression: extra.expression || '', value: result.value, generation: result.generation, goroutine: result.goroutine, frame: result.frame };
      const resultRow = inspectable({ ...result.value, name: evaluated.expression }, evaluated.expression);
      const details = resultRow.querySelector('details');
      if (details) details.open = true;
      $('evaluation').replaceChildren(resultRow);
      $('evaluation').scrollIntoView({ block: 'nearest' });
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
    if (snapshot) render(snapshot);
  }
  await refresh();
}

document.querySelectorAll<HTMLButtonElement>('[data-action]').forEach(button => {
  button.onclick = () => { void act(button.dataset.action as Action); };
});
$('takeBrowser').onclick = () => { void act('handover', {editor:'browser',open:false}); };
$('handover').onclick = () => {
  if (snapshot) void act(snapshot.owner === 'agent' || snapshot.owner === 'codex' ? 'handover' : 'reclaim', { open: true, editor: $<HTMLSelectElement>('editor').value, notify: Boolean(snapshot.thread) });
};
async function evaluate(): Promise<void> {
  if (!snapshot) return;
  await act('eval', { expression: $<HTMLInputElement>('expression').value, goroutine: snapshot.goroutine, frame: snapshot.frame, depth: 3, count: 128 });
}
$('evalForm').onsubmit = event => { event.preventDefault(); void evaluate(); };
$('addWatch').append(pinIcon());
$('addWatch').onclick = () => { if (evaluated) void act('watch', { expression: evaluated.expression }); };
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

interface SessionSummary { id: string; project: string; binary: string; status: string; owner?: string; panel?: string; binding?: { name: string }; recoverable?: boolean }
async function loadSessions(): Promise<void> {
  $<HTMLButtonElement>('refreshSessions').disabled = true;
  try {
    const result = await request<{sessions: SessionSummary[]}>('sessions');
    $('sessionList').replaceChildren();
    $('sessionCount').textContent = `(${result.sessions.length})`;
    for (const item of result.sessions) {
      const row = node('div', undefined, 'sessionRow');
      const title = node('strong', `${basename(item.project)} · ${basename(item.binary)}`);
      title.title = item.project + '\n' + item.binary;
      const owner = item.owner === 'agent' || item.owner === 'codex' ? item.binding?.name || 'Agent' : item.owner;
      row.append(title, node('span', `${item.id} · ${item.status}${owner ? ' · ' + owner : ''}`));
      if (item.id === snapshot?.id) row.append(node('span', 'Current', 'currentSession'));
      else if (item.panel && ['paused', 'running', 'exited'].includes(item.status)) {
        const url = new URL(item.panel);
        if (url.protocol === 'http:' && url.hostname === '127.0.0.1' && !url.username && !url.password) {
          const link = node('a', 'Open session'); link.href = url.href; row.append(link);
        }
      } else row.append(node('small', item.recoverable ? `Offline · recover with: delve-llm-adapter recover ${item.id}` : 'No live broker'));
      if (item.panel && ['paused', 'running', 'exited'].includes(item.status)) {
        const end = node('button', 'End session', 'endSession');
        end.onclick = async () => {
          if (!confirm(`End ${basename(item.binary)} (${item.id})? This terminates its program and debugger, including any editor attachment.`)) return;
          end.disabled = true;
          try {
            await request('sessions/stop', {id:item.id,confirmed:true});
            if (item.id === snapshot?.id) {
              clearInterval(timer);
              $('status').textContent = 'Ended';
              message('notice', 'Session ended. Choose another session from the list.');
              document.querySelectorAll<HTMLButtonElement>('button:not(.endSession):not(#refreshSessions)').forEach(b => { b.disabled = true; });
              // A native inspector loses its broker when ending itself. Existing
              // links remain usable; do not poll the stopped endpoint.
              row.replaceChildren(title, node('span', item.id + ' · ended'));
            } else await loadSessions();
          } catch (error) { message('sessionsError',errorMessage(error)); end.disabled=false; }
        };
        row.append(end);
      }
      $('sessionList').append(row);
    }
    message('sessionsError', result.sessions.length ? '' : 'No saved sessions.');
  } catch (error) { message('sessionsError', errorMessage(error)); }
  finally { $<HTMLButtonElement>('refreshSessions').disabled = false; }
}
$('refreshSessions').onclick = () => { void loadSessions(); };
document.querySelector<HTMLDetailsElement>('.sessionPicker')!.ontoggle = event => {
  if ((event.currentTarget as HTMLDetailsElement).open) void loadSessions();
};

function rememberScroll(): void {
  if (activeFile) sourceScroll.set(activeFile,{top:$('source').scrollTop,left:$('source').scrollLeft});
}
function showActiveSource(): void {
  const source = openSources.get(activeFile);
  if (source) renderSource({...source,line:snapshot?.status==='paused' && snapshot.source?.file===activeFile ? snapshot.source.line : 0});
  $('fileTabs').replaceChildren();
  for (const [file] of openSources) {
    const group=node('div',undefined,'fileTab');
    const button=node('button',basename(file));button.title=file;button.setAttribute('role','tab');button.setAttribute('aria-selected',String(file===activeFile));
    button.onclick=()=>{rememberScroll();activeFile=file;showActiveSource();const pos=sourceScroll.get(file);if(pos){$('source').scrollTop=pos.top;$('source').scrollLeft=pos.left;}};
    const close=node('button','×','closeTab');close.setAttribute('aria-label','Close '+basename(file));
    close.onclick=()=>{openSources.delete(file);sourceScroll.delete(file);if(activeFile===file){activeFile=Array.from(openSources.keys()).pop()||'';sourceKey='';if(!activeFile){$('source').replaceChildren();$('filename').textContent='Open a source file';$('line').textContent='';}}showActiveSource();};
    group.append(button,close);$('fileTabs').append(group);
  }
  paintBreakpoints();
}
function paintBreakpoints(): void {
  $('source').querySelectorAll<HTMLElement>('.sourceHighlight').forEach(el=>el.classList.toggle('navigated',navigationTarget?.file===activeFile && navigationTarget.line===Number(el.dataset.line)));
  $('gutterHint').textContent = snapshot?.status !== 'paused' ? 'Pause execution to edit breakpoints.' : 'Click beside a line number to toggle a breakpoint · right-click for a condition';
  const allowed = !busy && snapshot?.status==='paused';
  $('source').querySelectorAll<HTMLButtonElement>('button.breakpointGutter').forEach(button=>{
    const bp=snapshot?.breakpoints?.find(bp=>bp.id>0 && bp.file===activeFile && bp.line===Number(button.dataset.line));
    button.classList.toggle('hasBreakpoint',Boolean(bp));button.disabled=!allowed;
    button.title=!allowed ? 'Pause execution to edit breakpoints' : bp ? `Breakpoint ${bp.id}${bp.Cond ? ': '+bp.Cond : ''} · click to remove` : 'Click to set breakpoint · right-click for condition';
    button.setAttribute('aria-pressed',String(Boolean(bp)));
  });
}
async function gutterBreakpoint(file: string,line: number,conditional: boolean): Promise<void> {
  if (busy || snapshot?.status!=='paused') return;
  const bp=snapshot.breakpoints?.find(bp=>bp.id>0 && bp.file===file && bp.line===line);
  if (bp && conditional) { message('notice','Remove this breakpoint first to replace its condition.');return; }
  const condition=conditional ? prompt('Breakpoint condition (Go expression):','') : '';
  if (condition===null) return;
  await act(bp?'clear':'break',bp?{breakpoint:bp.id}:{file,line,condition});
  paintBreakpoints();
}
async function openSourceFile(file: string, targetLine?: number): Promise<void> {
  const revision=++fileRequest;
  try {
    const source=await request<Source>('sources?file='+encodeURIComponent(file));
    if(revision!==fileRequest)return;
    rememberScroll();openSources.set(file,source);activeFile=file;navigationTarget=targetLine ? {file,line:targetLine} : undefined;showActiveSource();
    if (targetLine) {
      const target=$('source').querySelector<HTMLElement>(`.sourceHighlight[data-line="${targetLine}"]`);
      if(target) $('source').scrollTop=Math.max(0,target.offsetTop-$('source').clientHeight/2);
      $('line').textContent='line '+targetLine;
    } else $('source').scrollTop=0;
    $<HTMLDialogElement>('filePicker').close();
  } catch(error){$('fileSearchStatus').textContent=errorMessage(error);message('error',errorMessage(error));}
}
function filterFiles(): void {
  const query=$<HTMLInputElement>('fileSearch').value.toLowerCase().replace(/^@/,'');
  const matches=knownFiles.filter(file=>file.toLowerCase().includes(query));
  matches.sort((a,b)=>Number(!a.startsWith((snapshot?.project||'')+'/'))-Number(!b.startsWith((snapshot?.project||'')+'/')) || a.localeCompare(b));
  $('fileResults').replaceChildren();
  for(const file of matches.slice(0,100)){
    const button=node('button',undefined,'fileResult');button.append(node('strong',basename(file)),node('small',file));button.onclick=()=>{void openSourceFile(file);};$('fileResults').append(button);
  }
  $('fileSearchStatus').textContent=`${matches.length} files${matches.length>100?' · type to narrow results':''}`;
}
async function showFilePicker(): Promise<void> {
  const dialog=$<HTMLDialogElement>('filePicker');if(!dialog.open)dialog.showModal();
  $<HTMLInputElement>('fileSearch').focus();$('fileSearchStatus').textContent='Loading source files…';
  try {knownFiles=(await request<{files:string[]}>('sources')).files;filterFiles();}catch(error){$('fileSearchStatus').textContent=errorMessage(error);}
}
$('openFile').onclick=()=>{void showFilePicker();};
$('closePicker').onclick=()=>{$<HTMLDialogElement>('filePicker').close();};
$('fileSearch').oninput=filterFiles;
$('fileSearch').onkeydown=event=>{if(event.key==='ArrowDown'||event.key==='Enter'){event.preventDefault();$('fileResults').querySelector<HTMLButtonElement>('button')?.focus();}};
$('followSource').onclick=()=>{if(snapshot?.source){rememberScroll();activeFile=snapshot.source.file;openSources.set(activeFile,snapshot.source);showActiveSource();}};
document.addEventListener('keydown',event=>{if((event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==='p'){event.preventDefault();void showFilePicker();}});

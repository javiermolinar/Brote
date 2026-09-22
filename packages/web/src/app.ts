import {createWorkspace} from './workspace';
import { createComments } from './comments';
import hljs from 'highlight.js/lib/core';
import go from 'highlight.js/lib/languages/go';

hljs.registerLanguage('go', go);

import type { Variable, Source, Snapshot, Evaluation, Action, ActionRequest, ActionOptions, ActionResult } from '../../client/src/models';
import { createClient } from '../../client/src/index';

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
let historicalID = new URLSearchParams(location.search).get('history');
let historicalLoaded=false;
const landing=!previewSession&&!historicalID&&document.body.dataset.workspace==='true';
let ownHistory: import('../../client/src/models').CommentThread[]=[];
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
const basename = (path: string | null | undefined) => path?.split('/').pop() || path || '?';
const errorMessage = (error: unknown) => error instanceof Error ? error.message : String(error);

function message(id: string, text?: string): void {
  $(id).textContent = text || '';
  $(id).hidden = !text;
}

const api = createClient({baseURL: location.origin, token, session: previewSession || undefined});
const request = <T>(path: string, body?: ActionRequest | Record<string, unknown>) => api.request<T>(path, body);

const comments = createComments(request, (file,line)=>openSourceFile(file,line,true));
const workspace = createWorkspace({request,runs:names=>comments.runs(names),history:items=>comments.history([...items,...ownHistory]),ended:()=>{if(snapshot){historicalID=snapshot.id;historicalLoaded=false;openSources.clear();activeFile='';displayedSource=undefined;sourceKey='';lastLocation='';renderKey='';const url=new URL(location.href);url.searchParams.delete('session');url.searchParams.set('history',historicalID);history.replaceState(null,'',url.pathname+url.search);void refresh();}}});
$('newQuestion').onclick=()=>{const source=displayedSource||snapshot?.source;if(source)comments.start(source.file,navigationTarget?.line||source.line||source.start,undefined,true);};
$('copyPath').onclick=()=>{if(displayedSource)void navigator.clipboard.writeText(displayedSource.file).then(()=>message('notice','Absolute source path copied.')).catch(()=>message('notice',displayedSource!.file));};

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
        const result = await request<ActionResult>('action', { action: 'eval', actor: 'browser', expression, ...scope, depth: 6, count: 128 });
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
  $('filename').textContent = source.file.startsWith((snapshot?.project||'')+'/')?source.file.slice(snapshot!.project.length+1):source.file;
 $('filename').title=source.file;
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
  if (key === sourceKey) { selectLine(); paintBreakpoints(); comments.source(source.file); return; }
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
  // Clone each highlighted line while retaining multiline token ancestors.
  const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
  const texts: Text[] = [];let textNode: Node | null;
  while ((textNode=walker.nextNode())) texts.push(textNode as Text);
  let textIndex=0,offset=0;
  const output=node('code');output.className=code.className;
  source.lines.forEach((line,index)=>{
    const row=node('span',undefined,'sourceCodeLine');row.dataset.line=String(source.start+index);
    if(texts.length){
      const range=document.createRange();range.setStart(texts[textIndex],offset);
      let remaining=line.length;
      while(remaining>0){const available=texts[textIndex].length-offset;if(remaining<=available){offset+=remaining;remaining=0;}else{remaining-=available;textIndex++;offset=0;}}
      range.setEnd(texts[textIndex],offset);row.append(range.cloneContents());
      if(index<source.lines.length-1){if(offset===texts[textIndex].length&&textIndex<texts.length-1){textIndex++;offset=0;}offset++;if(offset===texts[textIndex].length&&textIndex<texts.length-1){textIndex++;offset=0;}}
    }
    output.append(row);
  });
  pre.append(output);
  content.append(highlights, numbers, pre);
  pane.replaceChildren(content);
  pane.scrollLeft = left;
  comments.source(source.file);

  paintBreakpoints();
  // Scroll within the source pane without moving the surrounding inspector.
  const current = highlights.querySelector<HTMLElement>('.current');
  if (current) $('source').scrollTop = Math.max(0, current.offsetTop - $('source').clientHeight / 2);
}

function syncQuestion(state:Snapshot):void{
 const button=$<HTMLButtonElement>('newQuestion');
 button.hidden=!!historicalID||!!state.historical||state.status==='exited';
 button.disabled=busy||disconnected||state.status!=='paused'||!state.capabilities?.comments||!(displayedSource||state.source);
 button.title=button.disabled?'Open a source file at a paused run to ask a question.':'Ask about this source';
}
function setArchiveControls(state:Snapshot):void{
 const archived=!!state.historical;document.querySelector<HTMLElement>('.controls')!.hidden=archived;document.querySelector<HTMLElement>('.fileToolbar')!.hidden=archived&&!state.source;document.querySelector<HTMLElement>('.addBreakpoint')!.hidden=archived;syncQuestion(state);document.querySelector<HTMLElement>('.console')!.hidden=archived;document.querySelector<HTMLElement>('.inspectionDock')!.hidden=archived&&!state.frames?.length;$('openFileLabel').textContent=archived?'Browse saved files…':'Open file…';$('fileShortcut').textContent=/Mac/.test(navigator.platform)?'⌘P':'Ctrl+P';$('openFile').hidden=archived&&!state.source;$<HTMLButtonElement>('openFile').disabled=archived&&!state.source;$('followSource').hidden=!state.source;$('copyPath').hidden=!state.source&&!activeFile;$('gutterHint').hidden=archived&&!state.source;
 if(state.snapshotUnavailable)message('error','Saved snapshot unavailable. Run details and saved discussions remain accessible.');
}
function render(state: Snapshot): void {
  const paused = state.status === 'paused' || !!state.historical;
  const readOnly=!!state.historical;
  document.querySelector('main')!.dataset.executing = String(busy || state.status === 'running');
  lastPid = state.state.Pid || lastPid;
  const agentName = state.binding?.name || 'Agent';
 document.querySelector<HTMLElement>('.agentPanel')!.hidden=!!state.historical;

 setArchiveControls(state);
  $('status').textContent = 'Program · '+(state.historical?(state.runEnded?'Ended':'Historical'):state.status[0].toUpperCase() + state.status.slice(1));
message('historicalNotice',state.historical?(state.snapshotUnavailable||!state.source?'Historical run':'Saved snapshot')+(state.capturedAt?' · '+new Date(state.capturedAt).toLocaleString():'')+(state.runEnded?' · execution has ended':' · live process state is unverified'):'');
  $('status').className = 'badge ' + state.status;
  $('session').textContent = `${state.id} · PID ${lastPid || '—'} · ${basename(state.project)}`;
  const task = state.task;
  const activeTask = task?.status === 'authorized' || task?.status === 'active';
  $('agentStatus').textContent = state.historical?'Agent · Historical discussion':activeTask ? `${agentName} · ${['failed','unknown'].includes(task.delivery||'') ? 'Delivery needs attention' : task.status === 'authorized' && task.delivery !== 'acknowledged' ? 'Awaiting agent' : 'Debugging'}`
    : comments.answering() ? `${agentName} · Answering` : `${agentName} · ${state.agentConnected === true ? 'Connected' : state.agentConnected === false ? 'Offline' : 'Not reported'}`;
  $<HTMLButtonElement>('stopAgent').hidden = !activeTask;
  $<HTMLButtonElement>('stopAgent').disabled = busy;
  $('connection').textContent = state.editorConnected ? 'Editor connected to this process. Debugger controls are shared.' : 'Ask your agent to debug, or start an investigation here. Pause or Stop agent ends agent execution.';
  $<HTMLButtonElement>('authorizeTask').disabled = busy || !paused || readOnly || activeTask || !state.binding || state.agentConnected===false || !state.capabilities?.executionTasks;
  $<HTMLTextAreaElement>('taskInstruction').disabled = busy || activeTask;
  const taskStatus=activeTask ? task.delivery==='acknowledged' ? 'Debugging' : 'Waiting for agent' : task?.status;
  message('taskStatus', state.historical?'':task ? `${task.instruction} · ${taskStatus}${task.reason ? ': ' + task.reason : ''}${task.deliveryError ? ' · '+task.deliveryError : ''}`
    : !state.capabilities?.executionTasks ? 'Execution tasks require an updated broker.' : !state.binding || state.agentConnected===false ? 'Attach your agent conversation before starting an investigation here.' : '');
  document.querySelectorAll<HTMLButtonElement>('[data-action]').forEach(button => {
    button.disabled = readOnly || busy || (button.dataset.action === 'pause' ? state.status !== 'running' : !paused) || (!!state.beforeGoStart && ['next','step','stepout'].includes(button.dataset.action || ''));
  });
  $('breakForm').querySelectorAll<HTMLInputElement | HTMLButtonElement>('input,button').forEach(element => {
    element.disabled = busy || !paused || readOnly;
  });
  $<HTMLButtonElement>('stop').disabled = busy || readOnly;
  $('evalForm').querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement>('input,button,select').forEach(element => { element.disabled = busy || !paused || readOnly || !!state.beforeGoStart; });
  $<HTMLButtonElement>('addWatch').disabled = busy || !paused || readOnly;
  $('stopReason').textContent = state.historical?(state.snapshotUnavailable?'Snapshot unavailable':state.source?'Saved pause':'No recorded pause'):state.beforeGoStart ? 'Paused before Go starts' : paused ? state.state.stopReason || 'Paused at launch' : state.status === 'running' ? 'Running · showing last pause' : 'Program exited';
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
  $('evaluation').querySelectorAll<HTMLButtonElement>('button').forEach(button => { button.disabled = busy || !paused || readOnly; });

  // Keep the last stop visible during execution. Disable its interactions and
  // label it as stale instead of collapsing and rebuilding every panel.
  const disableInspection = () => {
    $('frames').querySelectorAll<HTMLButtonElement>('button').forEach(b => { b.disabled = busy || !paused || readOnly; });
    $('locals').querySelectorAll<HTMLButtonElement>('button').forEach(b => { b.disabled = busy || !paused || readOnly || b.dataset.unavailable === 'true'; });
    for (const id of ['watches']) $(id).querySelectorAll<HTMLButtonElement>('button').forEach(b => { b.disabled = busy || !paused || readOnly; });
    $<HTMLSelectElement>('goroutines').disabled = busy || !paused || readOnly;
  };
  disableInspection();
  $('breakpoints').querySelectorAll<HTMLButtonElement>('button:not(.breakpointLink)').forEach(b => { b.disabled = busy || !paused || readOnly; });
  if(state.status==='exited'&&!readOnly){
    document.querySelector<HTMLElement>('.controls')!.hidden=true;
    document.querySelector<HTMLElement>('.agentPanel')!.hidden=true;
    document.querySelector<HTMLElement>('.addBreakpoint')!.hidden=true;
    $('newQuestion').hidden=true;
    if(!displayedSource){$('filename').textContent='Program exited';$('source').replaceChildren(node('p','This run has finished. Start another run and set a breakpoint before continuing to inspect the program.'));$('goroutines').hidden=true;$('executionContext').hidden=true;$('frames').replaceChildren(node('p','No paused call stack.','empty'));document.querySelector<HTMLElement>('.inspectionDock')!.hidden=true;}
    else {$('gutterHint').textContent='Program exited · showing the last captured source';}
  }
  if (!paused) return;
  const key = JSON.stringify([state.goroutine, state.frame, state.frames, state.source, state.breakpoints, state.goroutines, state.watches, agentName, state.beforeGoStart]);
  paintBreakpoints();
  if (key === renderKey) return;
  renderKey = key;
  const expanded = new Set(Array.from($('locals').querySelectorAll<HTMLDetailsElement>('details[open]')).map(d => d.dataset.path));
  const positions = new Map(['frames', 'locals', 'watches'].map(id => [id, $(id).scrollTop]));
  for (const id of ['frames', 'locals', 'breakpoints', 'goroutines', 'watches']) $(id).replaceChildren();
  if (!state.source && !activeFile) {
    sourceKey = '';
    displayedSource = undefined;
    $('source').replaceChildren(node('p', state.historical?(state.snapshotUnavailable?'Saved snapshot unavailable.':'No snapshot was saved for this run.'):state.beforeGoStart ? 'The program has not entered Go code yet. Open a source file and set a breakpoint, then press Continue.' : 'No source available at this location.'));if(!state.historical){const open=node('button','Open file…');open.onclick=()=>void showFilePicker();$('source').append(open);}
    $('filename').textContent = state.historical?'No recorded source':state.beforeGoStart ? 'Paused before Go starts' : 'No source available';
    $('line').textContent = '—';
  }
  const contexts=state.goroutines||[];$('goroutines').hidden=contexts.length<2;$('executionContext').hidden=contexts.length!==1;$('executionContext').textContent=contexts.length===1?'Goroutine '+contexts[0].id:'';
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
  if (!state.frames?.length) $('frames').append(node('p', state.beforeGoStart ? 'Call stack appears when Go code is reached.' : state.historical?'No call stack recorded.':'No call stack at this location.', 'empty'));
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
    pin.disabled = busy || !paused || readOnly || unavailable;
    pin.setAttribute('aria-pressed', String(pinned));
    pin.setAttribute('aria-label', `${pinned ? 'Unpin' : 'Pin'} ${value.name}`);
    pin.title = unavailable ? 'This compiler-generated value cannot be pinned' : `${pinned ? 'Unpin' : 'Pin'} ${value.name}`;
    pin.append(pinIcon());
    pin.onclick = () => { void act(pinned ? 'unwatch' : 'watch', { expression: value.name }); };
    const valueView=inspectable(value, value.name);
    const name=valueView.querySelector<HTMLElement>('.name');
    if(name&&!unavailable){
      const trigger=node('button',value.name,'name commentName');
      trigger.type='button';trigger.title='Ask agent about '+value.name;
      trigger.onclick=event=>{event.preventDefault();if(state.source)comments.suggest(state.source.file,state.source.line,value.name,trigger.getBoundingClientRect(),value.name);};
      name.replaceWith(trigger);
    }
    row.append(pin, valueView);
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
    remove.disabled = busy || !paused || readOnly;
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
  if (fetching || historicalLoaded || landing) return;
  fetching = true;
  try {
    if(historicalID){snapshot=await request<Snapshot & {discussion:{threads:import('../../client/src/models').CommentThread[]}}>('saved-run?id='+encodeURIComponent(historicalID));ownHistory=(snapshot as Snapshot & {discussion:{threads:import('../../client/src/models').CommentThread[]}}).discussion?.threads||[];historicalLoaded=true;}
    else snapshot = await request<Snapshot>(`state?goroutine=${goroutine}&frame=${frame}`);
    if (disconnected) { disconnected = false; message('error', ''); }
    render(snapshot);
    comments.update(snapshot);
    if(snapshot.historical)comments.history(ownHistory);
    workspace.update(snapshot);
  } catch (error) {
    // Historical evidence does not change on the live polling interval.
    // Keep an unavailable archive stable until the user reloads it.
    if(historicalID){
      historicalLoaded=true;
      snapshot={id:historicalID,historical:true,snapshotUnavailable:true,status:'exited',state:{},frame:0,generation:0,owner:'browser',zedConnected:false,project:'',binary:'',label:''};
      renderKey='';render(snapshot);comments.update(snapshot);workspace.update(snapshot);
      message('error','Saved snapshot unavailable. Run details and saved discussions remain accessible.');
      const retry=node('button','Retry saved evidence');retry.onclick=()=>{historicalLoaded=false;void refresh();};$('source').append(retry);
      return;
    }
    disconnected = true;
    $('stopReason').textContent = historicalID?'Saved snapshot unavailable':'Connection lost · showing last captured pause';
    message('error', historicalID?'This run has no readable saved snapshot. Its investigation and run status are still available.':errorMessage(error));
    $('status').textContent = 'Program · Last state unverified';
    for(const selector of ['[data-action]','#breakForm button','#evalForm button','#frames button','#locals button','.breakpointGutter'])document.querySelectorAll<HTMLButtonElement>(selector).forEach(b=>b.disabled=true);
    if(snapshot){workspace.update(snapshot,true);comments.update({...snapshot,capabilities:{...snapshot.capabilities,comments:false}});}else workspace.empty();
  } finally { fetching = false; }
}

async function act(action: Action, extra: ActionOptions = {}): Promise<void> {
  if (busy || !snapshot || snapshot.historical || disconnected) return;
  busy = true;
  render(snapshot);
  message('error', '');
  try {
    const result = await request<ActionResult>('action', { action, actor: 'browser', generation: snapshot.generation, ...extra });
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
$('stopAgent').onclick = () => { void act('task-cancel', {task: snapshot?.task?.id}); };
$('taskForm').onsubmit = event => {
  event.preventDefault();
  const instruction = $<HTMLTextAreaElement>('taskInstruction').value.trim();
  if (instruction) void act('task-authorize', {instruction});
};
async function evaluate(): Promise<void> {
  if (!snapshot) return;
  await act('eval', { expression: $<HTMLInputElement>('expression').value, goroutine: snapshot.goroutine, frame: snapshot.frame, depth: 3, count: 128 });
}
$('evalForm').onsubmit = event => { event.preventDefault(); void evaluate(); };
$('addWatch').append(pinIcon());
$('addWatch').onclick = () => { if (evaluated) void act('watch', { expression: evaluated.expression }); };
$('goroutines').onchange = () => { goroutine = Number($<HTMLSelectElement>('goroutines').value); frame = 0; void refresh(); };
$('breakForm').onsubmit = event => {
  event.preventDefault();
  void act('break', {
    file: $<HTMLInputElement>('breakFile').value,
    line: Number($<HTMLInputElement>('breakLine').value),
    condition: $<HTMLInputElement>('breakCondition').value,
  });
};
const timer = setInterval(() => { void refresh(); }, 800);
if(landing){workspace.empty();$('status').textContent='No run selected';$('debuggerStatus').textContent='Debugger · No run';$('source').replaceChildren(node('p','Open an investigation or start a new one to debug a precompiled executable.'));document.querySelectorAll<HTMLButtonElement>('[data-action],#stop,#newQuestion,#openFile,#followSource,#copyPath').forEach(b=>b.disabled=true);}else void refresh();

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
  if(snapshot)syncQuestion(snapshot);
}
function paintBreakpoints(): void {
  $('source').querySelectorAll<HTMLElement>('.sourceHighlight').forEach(el=>el.classList.toggle('navigated',navigationTarget?.file===activeFile && navigationTarget.line===Number(el.dataset.line)));
  $('gutterHint').textContent = snapshot?.historical?'Saved source · execution and breakpoints are read only':snapshot?.status !== 'paused' ? 'Pause execution to edit breakpoints.' : 'Click beside a line number to toggle a breakpoint · right-click for a condition';
  const archived=!!historicalID||!!snapshot?.historical;
  const allowed = !archived && !disconnected && !busy && snapshot?.status==='paused';
  $('source').querySelectorAll<HTMLButtonElement>('button.breakpointGutter').forEach(button=>{
    const bp=snapshot?.breakpoints?.find(bp=>bp.id>0 && bp.file===activeFile && bp.line===Number(button.dataset.line));
    button.classList.toggle('hasBreakpoint',Boolean(bp));button.disabled=!allowed;
    button.setAttribute('aria-label',archived?`Saved ${bp?'breakpoint':'source line'} ${button.dataset.line}${bp?.Cond?': '+bp.Cond:''}`:`Toggle breakpoint at line ${button.dataset.line}`);
    button.title=archived ? `Saved ${bp?'breakpoint':'source line'}${bp?.Cond?': '+bp.Cond:''}` : !allowed ? 'Pause execution to edit breakpoints' : bp ? `Breakpoint ${bp.id}${bp.Cond ? ': '+bp.Cond : ''} · click to remove` : 'Click to set breakpoint · right-click for condition';
    button.setAttribute('aria-pressed',String(Boolean(bp)));
  });
}
async function gutterBreakpoint(file: string,line: number,conditional: boolean): Promise<void> {
  if (historicalID || snapshot?.historical || disconnected || busy || snapshot?.status!=='paused') return;
  const bp=snapshot.breakpoints?.find(bp=>bp.id>0 && bp.file===file && bp.line===line);
  if (bp && conditional) { message('notice','Remove this breakpoint first to replace its condition.');return; }
  const condition=conditional ? prompt('Breakpoint condition (Go expression):','') : '';
  if (condition===null) return;
  await act(bp?'clear':'break',bp?{breakpoint:bp.id}:{file,line,condition});
  paintBreakpoints();
}
async function openSourceFile(file: string, targetLine?: number,quiet=false): Promise<void> {
  const revision=++fileRequest;
  try {
    const source=(historicalID||snapshot?.historical)?(snapshot?.source?.file===file?snapshot.source:undefined):await request<Source>('sources?file='+encodeURIComponent(file));
    if(!source)throw Error('This file was not captured in the saved snapshot.');
    if(revision!==fileRequest)return;
    rememberScroll();openSources.set(file,source);activeFile=file;navigationTarget=targetLine ? {file,line:targetLine} : undefined;showActiveSource();
    if (targetLine) {
      const target=$('source').querySelector<HTMLElement>(`.sourceHighlight[data-line="${targetLine}"]`);
      if(target) $('source').scrollTop=Math.max(0,target.offsetTop-$('source').clientHeight/2);
      $('line').textContent='line '+targetLine;
    } else $('source').scrollTop=0;
    $<HTMLDialogElement>('filePicker').close();
  } catch(error){if(quiet)throw error;$('fileSearchStatus').textContent=errorMessage(error);message('error',errorMessage(error));}
}
let fileLoadError='';
function filterFiles(): void {
  if(fileLoadError){$('fileResults').replaceChildren();$('fileSearchStatus').textContent=fileLoadError;return;}
  const query=$<HTMLInputElement>('fileSearch').value.toLowerCase().replace(/^@/,'');
  const matches=knownFiles.filter(file=>file.toLowerCase().includes(query));
  const sourceRoot=snapshot?.source?.file.slice(0,snapshot.source.file.lastIndexOf('/')+1);const rank=(file:string)=>/\/(vendor|node_modules|site-packages)\//.test(file)?2:file.startsWith((snapshot?.project||'')+'/')?0:sourceRoot&&file.startsWith(sourceRoot)?1:3;matches.sort((a,b)=>rank(a)-rank(b)||a.localeCompare(b));
  $('fileResults').replaceChildren();
  for(const file of matches.slice(0,100)){
    const button=node('button',undefined,'fileResult');button.append(node('strong',basename(file)),node('small',file));button.onclick=()=>{void openSourceFile(file);};$('fileResults').append(button);
  }
  $('fileSearchStatus').textContent=matches.length?`${matches.length} files${matches.length>100?' · type to narrow results':''}`:query?'No files match this search.':'The debugger returned no source files for this binary.';
}
async function showFilePicker(): Promise<void> {
  if((historicalID||snapshot?.historical)&&!snapshot?.source)return;
  const dialog=$<HTMLDialogElement>('filePicker');if(!dialog.open)dialog.showModal();
  fileLoadError='';knownFiles=[];$('fileResults').replaceChildren();$<HTMLInputElement>('fileSearch').value='';$<HTMLInputElement>('fileSearch').focus();$('fileSearchStatus').textContent='Loading source files…';
  if(snapshot?.status==='exited'&&!snapshot.historical){fileLoadError='This program has exited. Choose Run again, then open a file and set a breakpoint before Continue.';filterFiles();return;}
  try {knownFiles=snapshot?.historical?[snapshot.source!.file]:(await request<{files:string[]}>('sources')).files;filterFiles();}catch(error){fileLoadError=errorMessage(error);filterFiles();}
}
$('openFile').onclick=()=>{void showFilePicker();};
$('closePicker').onclick=()=>{$<HTMLDialogElement>('filePicker').close();};
$('fileSearch').oninput=filterFiles;
$('fileSearch').onkeydown=event=>{if(event.key==='ArrowDown'||event.key==='Enter'){event.preventDefault();$('fileResults').querySelector<HTMLButtonElement>('button')?.focus();}};
$('followSource').onclick=()=>{if(snapshot?.source){rememberScroll();activeFile=snapshot.source.file;openSources.set(activeFile,snapshot.source);showActiveSource();}};
document.addEventListener('keydown',event=>{if(!document.querySelector('dialog[open]')&&(event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==='p'){event.preventDefault();void showFilePicker();}});

$('toggleNavigation').onclick=()=>{const main=document.querySelector('main')!;const open=main.dataset.navigation!=='open';main.dataset.navigation=open?'open':'closed';$('toggleNavigation').setAttribute('aria-expanded',String(open));};
document.querySelectorAll<HTMLButtonElement>('.paneNav [data-pane]').forEach(button=>{button.onclick=()=>{document.querySelector('main')!.dataset.pane=button.dataset.pane;document.querySelectorAll('.paneNav [data-pane]').forEach(b=>b.setAttribute('aria-pressed',String(b===button)));};});

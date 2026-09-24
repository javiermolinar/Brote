export interface Variable {
  name: string;
  type: string;
  value?: string;
  unreadable?: string;
  len?: number;
  cap?: number;
  kind?: number;
  children?: Variable[];
}

export interface StackFrame {
  file: string;
  line: number;
  function?: { name: string };
  Arguments?: Variable[];
  Locals?: Variable[];
}

export interface Source {
  file: string;
  line: number;
  start: number;
  lines: string[];
}

export interface Breakpoint {
  id: number;
  name?: string;
  file: string;
  line: number;
  Cond?: string;
  HitCond?: string;
}

export interface TraceRecord {session:string;name:string;program:string;debugger:string;local:Record<string,string>;remote?:Record<string,string>;closed:boolean;incomplete?:boolean}
export interface Snapshot {
 run?:string;
 traces?:{record:TraceRecord;error?:string};
 snapshotUnavailable?:boolean;historical?:boolean;runEnded?:boolean;capturedAt?:string;
 debugger?:{adapter:string;protocol:string;pid?:number;status:string;mode:string};
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
  capabilities?: {comments?: boolean; replyContexts?:boolean; executionTasks?: boolean; taskStart?:boolean};
  agentConnected?: boolean;
  beforeGoStart?: boolean;
  task?: {delivery?:string;deliveryError?:string;id:string;instruction:string;status:'authorized'|'active'|'completed'|'cancelled';reason?:string;expires:string};
}

export interface Evaluation { expression: string; value?: Variable; error?: string; generation?: number; goroutine?: number; frame?: number }

export type Action = 'task-start' | 'task-authorize' | 'task-cancel' | 'task-complete' | 'task-heartbeat' | 'continue' | 'next' | 'step' | 'stepout' | 'pause' | 'break' | 'clear' | 'handover' | 'reclaim' | 'stop' | 'eval' | 'watch' | 'unwatch' | 'retry-notification';
export interface ActionOptions {
 binding?: string;
 revision?: number;
 instruction?: string;
 task?: string;
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
export interface ActionRequest extends ActionOptions { action: Action; generation: number; actor?: string }
export interface ActionResult extends Partial<Evaluation> { Breakpoint?: {file:string;line:number}; instructions?: string; message?: string; notificationError?: string; persistenceError?: string; cleanupError?: string; openError?: string }


export interface CommentThread {
 id:string;file:string;line:number;expression?:string;created:string;resolved:boolean;
 context:{run?:string;pauseEpoch?:number;nativeEvidence?:Record<string,unknown>;inspectionError?:string;truncated?:boolean;generation?:number;frame?:number;goroutine?:number;capturedAt?:string;anchorSource?:{lines:string[]};frames?:{function?:{name:string};file?:string;line?:number;Locals?:{name:string;value?:string;type:string}[];Arguments?:{name:string;value?:string;type:string}[]}[]};
 messages:{evidence?:{id:string;session?:string;executionRun?:string;pauseEpoch?:number};id:string;author:string;body:string;created:string;run?:string;context?:CommentThread["context"]}[];
 delivery:{recipient?:{kind:string;id:string;name?:string;revision:number};question:string;status:string;error?:string;binding?:{id:string;name:string}};
}

export interface EvidenceTarget {session:string;traceId:string;spanId:string;captureId?:string}
export interface Annotation {session:string;id:string;revision:number;author:string;body:string;created:string;label?:string;comparisonKey?:string;targets:EvidenceTarget[];conversation?:EvidenceTarget;conversationThread?:string;conversationRun?:string;export?:{local:string;remote:string;error?:string}[]}

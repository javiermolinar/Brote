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

export interface Snapshot {
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
  capabilities?: {comments?: boolean};
}

export interface Evaluation { expression: string; value?: Variable; error?: string; generation?: number; goroutine?: number; frame?: number }

export type Action = 'continue' | 'next' | 'step' | 'stepout' | 'pause' | 'break' | 'clear' | 'handover' | 'reclaim' | 'stop' | 'eval' | 'watch' | 'unwatch' | 'retry-notification';
export interface ActionOptions {
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
 context:{generation?:number;frame?:number;goroutine?:number;capturedAt?:string;anchorSource?:{lines:string[]};frames?:{function?:{name:string};file?:string;line?:number;Locals?:{name:string;value?:string;type:string}[];Arguments?:{name:string;value?:string;type:string}[]}[]};
 messages:{id:string;author:string;body:string;created:string}[];
 delivery:{question:string;status:string;error?:string;binding?:{id:string;name:string}};
}

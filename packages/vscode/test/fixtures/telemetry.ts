import { ROOT_CONTEXT, trace, Span, SpanStatusCode, Attributes } from '@opentelemetry/api';
import { BasicTracerProvider, BatchSpanProcessor, SpanExporter, ReadableSpan } from '@opentelemetry/sdk-trace-base';
import { configuredExporter } from './configuredExporter';
import { resourceFromAttributes } from '@opentelemetry/resources';

// Span names and resource attributes are outside the SDK's attribute-value limit.
// Walk only the retained prefix; do not allocate a copy of an oversized input.
function boundedName(value:string,maxBytes=512):string {
  let result='',bytes=0;
  for(const char of value){
    const size=Buffer.byteLength(char);
    if(bytes+size>maxBytes)break;
    result+=char;bytes+=size;
  }
  return result;
}

export interface ExportConfig {url:string;headers:Record<string,string>;push?:(data:Uint8Array)=>Promise<void>}
export function exportConfig(env:NodeJS.ProcessEnv):ExportConfig|undefined {
  const tracesEndpoint=env.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT?.trim();
  const endpoint=tracesEndpoint || env.OTEL_EXPORTER_OTLP_ENDPOINT?.trim();
  if(!endpoint)return;
  const protocol=env.OTEL_EXPORTER_OTLP_TRACES_PROTOCOL ?? env.OTEL_EXPORTER_OTLP_PROTOCOL;
  if(protocol && protocol!=='http/protobuf')throw new Error('Use OTLP HTTP/protobuf.');
  const url=new URL(endpoint);
  if(!['http:','https:'].includes(url.protocol) || url.username || url.password || url.hash || url.search)throw new Error('Invalid OTLP endpoint.');
  if(!tracesEndpoint)url.pathname=url.pathname.replace(/\/$/,'')+'/v1/traces';
  const headers:Record<string,string>={};
  for(const pair of (env.OTEL_EXPORTER_OTLP_TRACES_HEADERS ?? env.OTEL_EXPORTER_OTLP_HEADERS ?? '').split(',')) {
    if(!pair.trim())continue;
    const separator=pair.indexOf('=');
    if(separator<1)throw new Error('Invalid OTLP headers.');
    const key=pair.slice(0,separator).trim(),value=decodeURIComponent(pair.slice(separator+1).trim());
    if(!/^[!#$%&'*+.^_`|~0-9a-z-]+$/i.test(key) || /[^\t\x20-\x7e]/.test(value))throw new Error('Invalid OTLP headers.');
    headers[key]=value;
  }
  return {url:url.href,headers};
}
// Keep startup outside the SDK's network-export deadline. This queue has the
// same bound as the SDK queue; a slow helper cannot accumulate unbounded spans.
class ReadyBatchProcessor extends BatchSpanProcessor {
  private ready=false;
  private pending:ReadableSpan[]=[];
  private readonly readiness:Promise<void>;
  constructor(exporter:SpanExporter, readiness:Promise<unknown>) {
    super(exporter,{maxQueueSize:256,maxExportBatchSize:64,scheduledDelayMillis:500,exportTimeoutMillis:2000});
    const release=()=>{this.ready=true;const spans=this.pending;this.pending=[];for(const span of spans)super.onEnd(span);};
    // Also drain through the failed exporter so every retained batch reports failure.
    this.readiness=readiness.then(release,release);
  }
  override onEnd(span:ReadableSpan):void {
    if(this.ready)super.onEnd(span);
    else if(this.pending.length<256)this.pending.push(span);
  }
  override async forceFlush():Promise<void>{await this.readiness;await super.forceFlush();}
  override async shutdown():Promise<void>{await this.readiness;await super.shutdown();}
}
// Remote failures are reported by the exporter callback, but must not let a
// provider's aggregate shutdown finish before the local processor has drained.
class RemoteBatchProcessor extends BatchSpanProcessor {
  override async shutdown():Promise<void>{try{await super.shutdown();}catch{/* reported by exporter */}}
  override async forceFlush():Promise<void>{try{await super.forceFlush();}catch{/* reported by exporter */}}
}
async function settleAll(promises:Promise<void>[]):Promise<void>{
  const results=await Promise.allSettled(promises);
  if(results.some(result=>result.status==='rejected'))throw new Error('Local trace export failed.');
}
export interface Frame {name?:string;line?:number;source?:{path?:string}}
export interface Observation {thread:number;frame:Frame;stack:Frame[];scopes:unknown[];capturedAt:string}
interface Pending {span:Span;execution:boolean;thread?:number;ack?:boolean;stop?:{time:Date;outcome:string}}
const executions=new Set(['continue','next','stepIn','stepOut','restart']);
const commands=new Set([...executions,'launch','attach','pause','disconnect','terminate','setBreakpoints','setFunctionBreakpoints','setExceptionBreakpoints','evaluate']);

/** Providers are private: Brote never changes the host's global OpenTelemetry SDK. */
export class SessionTrace {
  private readonly debuggerProvider:BasicTracerProvider;
  private readonly programProvider:BasicTracerProvider;
  private readonly root:Span;
  private readonly run:Span;
  private readonly threads=new Map<number,Span>();
  private readonly requests=new Map<number,Pending>();
  private sequence=0;
  private closed=false;
  private shutdownPromise?:Promise<void>;
  readonly debuggerTraceID:string;
  readonly programTraceID:string;
  constructor(readonly id:string,name:string,type:string,exporter:()=>SpanExporter,additionalExporters:(()=>SpanExporter)[]=[],localReadiness?:Promise<unknown>) {
    name=boundedName(name);type=boundedName(type,128);
    const provider=(service:string)=>new BasicTracerProvider({resource:resourceFromAttributes({'service.name':service,'debugger.adapter.type':type}),spanLimits:{attributeCountLimit:128,attributeValueLengthLimit:65536},spanProcessors:[exporter,...additionalExporters].map((factory,index)=>index===0 && localReadiness?new ReadyBatchProcessor(factory(),localReadiness):new (index===0?BatchSpanProcessor:RemoteBatchProcessor)(factory(),{maxQueueSize:256,maxExportBatchSize:64,scheduledDelayMillis:500,exportTimeoutMillis:2000}))});
    this.debuggerProvider=provider('brote');this.programProvider=provider(name);
    this.root=this.debuggerProvider.getTracer('brote.debugger').startSpan('debugger.session',{attributes:{'debugger.session.id':id}},ROOT_CONTEXT);
    this.run=this.programProvider.getTracer('brote.program').startSpan(boundedName(`run ${name}`),{attributes:{...this.common(),'program.span.type':'run'},links:[{context:this.root.spanContext()}]},ROOT_CONTEXT);
    this.debuggerTraceID=this.root.spanContext().traceId;this.programTraceID=this.run.spanContext().traceId;
  }
  private common():Attributes{return {'debugger.session.id':this.id,'program.run.id':this.id,'program.schema.version':2};}
  request(seq:number,command:string,thread?:number):void {
    if(this.closed || !commands.has(command) || this.requests.size>=1024)return;
    const span=this.debuggerProvider.getTracer('brote.debugger').startSpan(command,{attributes:{'debugger.session.id':this.id,'debugger.channel':'dap','debugger.command':command}},trace.setSpan(ROOT_CONTEXT,this.root));
    this.requests.set(seq,{span,execution:executions.has(command),thread});
  }
  response(seq:number,success:boolean):void {
    const pending=this.requests.get(seq);if(!pending)return;
    if(!success){this.finish(seq,'error');return;}
    pending.ack=true;
    if(!pending.execution)this.finish(seq,'success');
    else if(pending.stop)this.finish(seq,pending.stop.outcome,pending.stop.time);
  }
  stopped(outcome='stopped',thread?:number,allThreads=true):void {
    const time=new Date();
    for(const [seq,pending] of this.requests){
      if(!pending.execution || pending.stop || (!allThreads && pending.thread!==thread))continue;
      pending.stop={time,outcome};
      if(pending.ack)this.finish(seq,outcome,time);
    }
  }
  private finish(seq:number,outcome:string,time?:Date):void {
    const pending=this.requests.get(seq);if(!pending)return;
    pending.span.setAttribute('debugger.outcome',outcome);
    if(outcome==='error')pending.span.setStatus({code:SpanStatusCode.ERROR});
    pending.span.end(time);this.requests.delete(seq);
  }
  snapshot(observation:Observation,label?:string,selections:Record<string,string>={}):void {
    if(this.closed || this.sequence>=1000)return;
    const instant=new Date(observation.capturedAt);
    if(!Number.isFinite(instant.getTime()))return;
    let parent=this.threads.get(observation.thread);
    if(!parent) {
      if(this.threads.size>=256)return;
      // Generic DAP exposes observed frames, not goroutine creation sites.
      const entry=boundedName(observation.stack.at(-1)?.name || observation.frame.name || 'unknown');
      parent=this.programProvider.getTracer('brote.program').startSpan(boundedName(`thread observed at ${entry}`),{startTime:instant,attributes:{...this.common(),'program.span.type':'thread','program.thread.id':observation.thread,'program.observation.boundary':'first-observed'}},trace.setSpan(ROOT_CONTEXT,this.run));
      this.threads.set(observation.thread,parent);
    }
    const location=(f:Frame):Frame=>({name:f.name?.slice(0,512),line:f.line,source:f.source?.path?{path:f.source.path.slice(0,2048)}:undefined});
    const snapshot={thread:observation.thread,capturedAt:observation.capturedAt,frame:location(observation.frame),stack:observation.stack.slice(0,30).map(location),scopes:[...observation.scopes]};
    let payload=JSON.stringify(snapshot),partial=snapshot.scopes.some(scope=>{const s=scope as {omitted?:string;truncated?:boolean;variables?:{truncated?:boolean}[]};return Boolean(s.omitted || s.truncated || s.variables?.some(v=>v.truncated));});
    while(Buffer.byteLength(payload)>32768 && snapshot.scopes.length){snapshot.scopes.pop();partial=true;payload=JSON.stringify(snapshot);}
    if(Buffer.byteLength(payload)>32768){snapshot.stack=[];partial=true;payload=JSON.stringify(snapshot);}
    if(Buffer.byteLength(payload)>32768)return;
    const attrs:Attributes={...this.common(),'program.span.type':'snapshot','program.thread.id':observation.thread,'program.snapshot.sequence':++this.sequence,'program.capture.status':partial?'partial':'complete','program.snapshot.json':payload};
    for(const [alias,selected] of Object.entries(selections).slice(0,16)){
      if(!/^[a-z][a-z0-9_]{0,63}$/.test(alias) || typeof selected!=='string')continue;
      const variables=snapshot.scopes.flatMap(scope=>(scope as {variables?:{name:string;value:string;type?:string;truncated?:boolean;variablesReference?:number}[]}).variables || []);
      const matches=variables.filter(v=>v.name===selected);
      const value=matches[0];
      const status=matches.length>1?'ambiguous':!value?'not_captured':value.truncated?'truncated':value.variablesReference?'non_scalar':'available';
      attrs['program.value_status.'+alias]=status;
      attrs['program.value_expression.'+alias]=selected;
      if(value?.type)attrs['program.value_type.'+alias]=value.type;
      if(status==='available')attrs['program.value.'+alias]=value.value;
    }
    if(label)attrs['program.capture.name']=label;
    if(observation.frame.name)attrs['code.function.name']=observation.frame.name;
    if(observation.frame.source?.path)attrs['code.file.path']=observation.frame.source.path;
    if(observation.frame.line)attrs['code.line.number']=observation.frame.line;
    this.programProvider.getTracer('brote.program').startSpan(boundedName(label || observation.frame.name || 'capture'),{startTime:instant,attributes:attrs},trace.setSpan(ROOT_CONTEXT,parent)).end(instant);
  }
  async flush():Promise<void>{await settleAll([this.debuggerProvider.forceFlush(),this.programProvider.forceFlush()]);}
  close():Promise<void>{
    if(this.shutdownPromise)return this.shutdownPromise;
    this.closed=true;
    for(const seq of this.requests.keys())this.finish(seq,'interrupted');
    for(const span of this.threads.values()){span.setAttribute('program.observation.boundary','session-ended');span.end();}
    this.run.end();this.root.end();
    this.shutdownPromise=settleAll([this.debuggerProvider.shutdown(),this.programProvider.shutdown()]);
    return this.shutdownPromise;
  }
}
export async function preflight(config:ExportConfig):Promise<void> {
  const response=await fetch(config.url,{method:'POST',headers:{...config.headers,'Content-Type':'application/x-protobuf'},body:new Uint8Array(),signal:AbortSignal.timeout(2000),redirect:'error'});
  await response.body?.cancel();
  if(!response.ok)throw new Error('Remote OTLP connection or authentication failed.');
}
export function createSessionTrace(id:string,name:string,type:string,config:ExportConfig|Promise<ExportConfig>,remote?:ExportConfig,onExport?:(destination:'local'|'remote',success:boolean,traceID:string)=>void):SessionTrace {
  const factory=(settings:ExportConfig|Promise<ExportConfig>,destination:'local'|'remote')=>():SpanExporter=>{
    let exporter:SpanExporter|undefined,closed=false;
    const ready=Promise.resolve(settings).then(config=>{
      if(closed)throw new Error('Trace exporter is closed.');
      return exporter=configuredExporter(config,destination==='local');
    });
    // Readiness may fail before the first batch is exported.
    void ready.catch(()=>{});
    return {
      export(spans,done){void ready.then(target=>target.export(spans,result=>{onExport?.(destination,result.code===0,spans[0]?.spanContext().traceId || '');done(result);})).catch(()=>{onExport?.(destination,false,spans[0]?.spanContext().traceId || '');done({code:1,error:new Error('Trace exporter is unavailable.')});});},
      async shutdown(){closed=true;await exporter?.shutdown();},
    };
  };
  return new SessionTrace(id,name,type,factory(config,'local'),remote?[factory(remote,'remote')]:[],Promise.resolve(config));
}

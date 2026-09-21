export * from './models';
export const PROTOCOL_VERSION = 2;
export interface Binding {id:string;name:string;revision:number}
export interface SessionEvent {id:number;kind:string;owner:string;binding?:Binding;note?:string;created:string}
export interface ClientOptions {baseURL:string;token?:string;session?:string;binding?:string;timeoutMs?:number;fetch?:typeof fetch}

/** Shared local HTTP transport. Legacy bearer tokens are supported for migration. */
export function createClient(options:ClientOptions){
 const base=new URL(options.baseURL);
 if(base.protocol!=='http:'||base.hostname!=='127.0.0.1'||base.username||base.password||base.pathname!=='/'||base.search||base.hash)throw new Error('Expected a local broker URL');
 const url=(route:string)=>{
  if(!/^[a-z][a-z-]*(?:\/[a-z][a-z-]*)*(?:\?.*)?$/.test(route)||route.includes('#'))throw new Error('Invalid API route');
  const target=new URL('/api/'+route,base);
  if(options.session)target.searchParams.set('session',options.session);
  return target;
 };
 async function request<T>(route:string,body?:unknown,signal?:AbortSignal):Promise<T>{
  const response=await (options.fetch||fetch)(url(route).href,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json',...(options.token?{Authorization:'Bearer '+options.token}:{})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:signal?AbortSignal.any([signal,AbortSignal.timeout(options.timeoutMs||8000)]):AbortSignal.timeout(options.timeoutMs||8000)});
  const value=await response.json() as T&{error?:string;version?:number};
  if(!response.ok)throw new Error(value.error||`Broker returned ${response.status}`);
  if(value.version!==undefined&&value.version>PROTOCOL_VERSION)throw new Error('Unsupported broker protocol version');
  return value;
 }
 // EventSource owns reconnects and Last-Event-ID; callers reconcile after reset.
 function subscribe(cursor:number,onEvent:(event:SessionEvent)=>void,onReset:()=>void,onOpen?:()=>void):()=>void{
  if(options.token)throw new Error('Live events require protocol v2');
  const target=url('events');target.searchParams.set('cursor',String(cursor));
  const stream=new EventSource(target.href);
  stream.onopen=()=>onOpen?.();
  stream.onmessage=e=>{let event:SessionEvent;try{event=JSON.parse(e.data);}catch{return;}onEvent(event);};
  stream.addEventListener('reset',()=>{stream.close();onReset();});
  return ()=>stream.close();
 }
 return {request,subscribe};
}

/** Node/browser streaming reader; consumers reconnect using the last event ID.
 * A reset means the cursor expired and the caller must reconcile a fresh snapshot.
 */
export async function* readEvents(options:ClientOptions,cursor=0,signal?:AbortSignal):AsyncGenerator<SessionEvent|{kind:'reset'}>{
 createClient(options); // Apply the same loopback validation.
 const endpoint=new URL('/api/events',options.baseURL);endpoint.searchParams.set('cursor',String(cursor));if(options.session)endpoint.searchParams.set('session',options.session);if(options.binding)endpoint.searchParams.set('binding',options.binding);
 const response=await (options.fetch||fetch)(endpoint.href,{headers:options.token?{Authorization:'Bearer '+options.token}:{},redirect:'error',signal});
 if(!response.ok||!response.body)throw new Error(`Event stream returned ${response.status}`);
 const reader=response.body.getReader(),decoder=new TextDecoder();let pending='';
 try{while(true){const chunk=await reader.read();if(chunk.done)break;pending+=decoder.decode(chunk.value,{stream:true});if(pending.length>1048576)throw new Error('Event frame too large');let end:number;
  while((end=pending.indexOf('\n\n'))>=0){const block=pending.slice(0,end);pending=pending.slice(end+2);let kind='',data='';for(const line of block.split('\n')){if(line.startsWith('event:'))kind=line.slice(6).trim();if(line.startsWith('data:'))data+=line.slice(5).trimStart()+'\n';}if(kind==='reset'){yield {kind:'reset'};return;}if(data){const event=JSON.parse(data) as SessionEvent;if(event.id>cursor){cursor=event.id;yield event;}}}
 }}finally{await reader.cancel();reader.releaseLock();}
}

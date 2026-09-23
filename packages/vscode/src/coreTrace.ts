import {execFile} from 'node:child_process';
import {createClient} from '../../client/src/index';

export interface Frame {name?:string;line?:number;source?:{path?:string}}
export interface Observation {thread:number;frame:Frame;stack:Frame[];scopes:unknown[];capturedAt:string}
export interface TraceRecord {session:string;name:string;program:string;debugger:string;local?:Record<string,string>;remote?:Record<string,string>;closed?:boolean;incomplete?:boolean}
/** Thin Brote API transport. Tempo, span IDs, span generation and exporters live in Go. */
export class CoreTracing {
 private endpoint?:Promise<ReturnType<typeof createClient>>;
 constructor(private binary:string){}
 private connect(){
  return this.endpoint??=new Promise<ReturnType<typeof createClient>>((resolve,reject)=>{
   execFile(this.binary,['trace-service'],{timeout:35000,maxBuffer:65536},(error,stdout)=>{
    if(error){this.endpoint=undefined;reject(new Error('Brote tracing service could not start.'));return;}
    try{const {endpoint}=JSON.parse(stdout);resolve(createClient({baseURL:endpoint,timeoutMs:15000}));}catch{this.endpoint=undefined;reject(new Error('Invalid Brote tracing endpoint.'));}
   });
  });
 }
 async request<T>(route:string,body?:unknown):Promise<T>{
  try{return await (await this.connect()).request<T>(route,body);}catch(error){this.endpoint=undefined;throw error;}
 }
 records():Promise<TraceRecord[]>{return this.request('trace-sessions');}
 async traceJSON(id:string):Promise<string>{if(!/^[a-f0-9]{32}$/.test(id))throw new Error('Invalid trace ID.');return JSON.stringify(await this.request('traces?id='+id),null,2);}
 session(id:string,name:string,adapter:string,onRecord:(record:TraceRecord)=>void,report:(message:string)=>void){return new CoreSessionTrace(this,id,name,adapter,onRecord,report);}
}
export class CoreSessionTrace {
 private chain:Promise<void>;
 private pending=0;
 private closed=false;
 private failed=false;
 private unavailable=false;
 private started=false;
 private heartbeat:ReturnType<typeof setInterval>;
 constructor(private core:CoreTracing,private id:string,name:string,adapter:string,private onRecord:(record:TraceRecord)=>void,private report:(message:string)=>void){
  this.chain=Promise.resolve();this.send({kind:'start',name,adapter});
  this.heartbeat=setInterval(()=>this.send({kind:'heartbeat'}),30000);this.heartbeat.unref();
 }
 private send(event:Record<string,unknown>):void {
  if(this.closed)return;
  if(this.pending>=256 && event.kind!=='close'){this.fail();return;}
  this.pending++;
  const message={session:this.id,at:new Date().toISOString(),...event};
  this.chain=this.chain.then(async()=>{
   if(this.unavailable && (!this.started || (event.kind!=='close' && event.kind!=='heartbeat')))return;
   const record=await this.core.request<TraceRecord>('trace-events',{...message,incomplete:this.failed||undefined});
   this.started=true;this.unavailable=false;
   if(this.failed){record.incomplete=true;record.local??={};record.local[record.program]='Export failed or incomplete';record.local[record.debugger]='Export failed or incomplete';}
   this.onRecord(record);
  }).catch(()=>{this.unavailable=true;this.fail();}).finally(()=>{this.pending--;});
 }
 private fail(){this.failed=true;this.report('Trace capture failed or incomplete.');}
 request(seq:number,command:string,thread?:number){this.send({kind:'request',seq,command,thread});}
 response(seq:number,success:boolean){this.send({kind:'response',seq,success});}
 stopped(kind='stopped',thread?:number,allThreads=true){this.send({kind,thread,allThreads});}
 snapshot(observation:Observation,label?:string,selections:Record<string,string>={}){this.send({kind:'snapshot',observation,label,selections:Object.fromEntries(Object.entries(selections).filter(([alias,value])=>/^[a-z][a-z0-9_]{0,63}$/.test(alias)&&typeof value==='string').slice(0,16))});}
 async flush(){this.send({kind:'flush'});await this.chain;}
 async close(){if(!this.closed){clearInterval(this.heartbeat);this.send({kind:'close'});this.closed=true;}
  let timer:ReturnType<typeof setTimeout>|undefined;
  await Promise.race([this.chain,new Promise<void>(resolve=>{timer=setTimeout(()=>{this.unavailable=true;this.fail();resolve();},50000);})]);
  if(timer)clearTimeout(timer);
 }
}

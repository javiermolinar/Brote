import {spawn,ChildProcessByStdio} from 'node:child_process';
import type {Readable,Writable} from 'node:stream';
import {createInterface} from 'node:readline';
import * as path from 'node:path';
import {ExportConfig} from './telemetry';

type Child=ChildProcessByStdio<Writable,Readable,null>;
type Pending={child:Child;resolve:(value:unknown)=>void;reject:(error:Error)=>void;timer:ReturnType<typeof setTimeout>};

export class TraceRuntime {
  private child?:Child;
  private ready?:Promise<void>;
  private exited?:Promise<void>;
  private stopping=false;
  private sequence=0;
  private pending=new Map<number,Pending>();
  constructor(private binary:string,private dataDir:string,private report:(message:string)=>void){}
  async start():Promise<ExportConfig> {
    if(this.stopping)throw new Error('Trace runtime is stopping.');
    if(this.child && (this.child.exitCode!==null || this.child.signalCode!==null))await this.exited;
    if(this.stopping)throw new Error('Trace runtime is stopping.');
    this.ready??=this.launch();
    const attempt=this.ready,exited=this.exited;
    try{await attempt;}catch(error){await exited;throw error;}
    const child=this.child;
    if(!child)throw new Error('Local trace storage stopped.');
    return {url:'',headers:{},push:async data=>{await this.request(child,{method:'push',data:Buffer.from(data).toString('base64')},3000);}};
  }
  private launch():Promise<void> {
    const env={...process.env};
    for(const key of Object.keys(env))if(key.startsWith('OTEL_EXPORTER_'))delete env[key];
    const child=spawn(this.binary,['embedded-tempo',path.resolve(this.dataDir)],{env,stdio:['pipe','pipe','ignore']});
    this.child=child;
    child.stdin.on('error',()=>{/* Completion rejects outstanding requests below. */});
    this.exited=new Promise(resolve=>child.once('close',()=>resolve()));
    return new Promise((resolve,reject)=>{
      let settled=false,connected=false;
      const fail=()=>{
        if(!settled){settled=true;clearTimeout(timer);reject(new Error('Local trace storage could not start. Check that its directory is writable and not open in another window.'));}
        else if(connected){connected=false;if(!this.stopping)this.report('Local trace storage stopped unexpectedly.');}
      };
      const timer=setTimeout(()=>{child.kill('SIGKILL');fail();},30000);
      child.once('error',fail);
      child.once('close',()=>{
        fail();
        for(const [id,request] of this.pending)if(request.child===child){clearTimeout(request.timer);request.reject(new Error('Local trace storage stopped.'));this.pending.delete(id);}
        // Wait for close and lock release, and never clear a newer child's state.
        if(this.child===child){this.ready=undefined;this.child=undefined;}
      });
      createInterface({input:child.stdout}).on('line',line=>{
        try{
          const value=JSON.parse(line);
          if(!settled && value.ready===true){settled=true;connected=true;clearTimeout(timer);resolve();return;}
          const request=this.pending.get(value.id);
          if(!request || request.child!==child)return;
          clearTimeout(request.timer);this.pending.delete(value.id);
          if(typeof value.error==='string')request.reject(new Error(value.error));else request.resolve(value.result);
        }catch{/* Ignore non-protocol output. */}
      });
    });
  }
  private request(child:Child,message:Record<string,unknown>,timeout:number):Promise<unknown> {
    if(child!==this.child || child.exitCode!==null || child.signalCode!==null || child.stdin.writableEnded)return Promise.reject(new Error('Local trace storage stopped.'));
    // Bound pipe backlog across sessions as well as each producer's span queue.
    if(this.pending.size>=64 || child.stdin.writableLength>16*1024*1024)return Promise.reject(new Error('Local trace storage is busy.'));
    const id=++this.sequence;
    return new Promise((resolve,reject)=>{
      const fail=(error:Error)=>{const request=this.pending.get(id);if(!request)return;clearTimeout(request.timer);this.pending.delete(id);reject(error);};
      const timer=setTimeout(()=>fail(new Error('Local trace request timed out.')),timeout);
      this.pending.set(id,{child,resolve,reject,timer});
      child.stdin.write(JSON.stringify({id,...message})+'\n',error=>{if(error)fail(new Error('Local trace storage stopped.'));});
    });
  }
  async traceJSON(id:string):Promise<string> {
    if(!/^[a-f0-9]{32}$/.test(id))throw new Error('Invalid trace ID.');
    await this.start();
    for(let attempt=0;;attempt++)try{return JSON.stringify(await this.request(this.child!,{method:'query',traceID:id},15000),null,2);}catch(error){if(attempt>=50||!String(error).includes('not queryable'))throw error;await new Promise(resolve=>setTimeout(resolve,500));}
  }
  async stop():Promise<void> {
    this.stopping=true;
    const child=this.child,exited=this.exited;
    if(!child || !exited)return;
    child.stdin.end();
    let timer:ReturnType<typeof setTimeout>|undefined;
    await Promise.race([exited,new Promise<void>(resolve=>{timer=setTimeout(()=>{this.report('Trace runtime exceeded its shutdown deadline; recovery will run on next start.');child.kill('SIGKILL');resolve();},15000);})]);
    if(timer)clearTimeout(timer);
    await exited;
  }
}

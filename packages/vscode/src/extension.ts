import * as vscode from 'vscode';
import * as path from 'node:path';
import {registerNativeTracing} from './nativeTracing';
import { registerService } from './service';
import { nativeDiscussions } from './native';
import { migrateDiscussions } from './discussionMigration';
import { serviceTracepoints } from './serviceTracepoints';

let shutdown:(()=>Promise<void>)|undefined;
export async function deactivate():Promise<void> {await shutdown?.();shutdown=undefined;}
export function activate(context:vscode.ExtensionContext) {
  const service=registerService(context);
 const migration=migrateDiscussions(context,service);
 void migration.catch(error=>vscode.window.showErrorMessage(`Brote history migration: ${String(error)}`));
  const native=nativeDiscussions(context,async(session,thread,index)=>{
    const state=await service.state(String(session.configuration.sessionId),thread,index);
    if(state.status!=='paused'||!state.frames?.length)throw Error('Pause the Brote session before capturing evidence.');
    const stack=state.frames.map(f=>({name:f.function?.name,line:f.line,source:{path:f.file}}));
    const selected=state.frames[state.frame||0];
    return {session:session.id,serviceSession:state.id,name:session.name,type:'brote',capturedAt:new Date().toISOString(),thread:state.goroutine||thread,frame:stack[state.frame||0],stack,scopes:[{name:'Locals',variables:selected.Locals||[],error:selected.localsError,truncated:selected.localsTruncated},{name:'Arguments',variables:selected.Arguments||[],error:selected.argumentsError,truncated:selected.argumentsTruncated}],run:state.run,pauseEpoch:state.pauseEpoch,generation:state.generation,frameIndex:index,inspectionError:state.inspectionError,stackError:state.stackError,truncated:state.truncated||selected.truncated,frameOffset:state.frameOffset,exception:state.exception,exceptionError:state.exceptionError};
  },service,migration);
  const closeTracing=registerNativeTracing(context,service.executable);
  shutdown=async()=>{await Promise.allSettled([native.shutdown(),closeTracing()]);};
  serviceTracepoints(context,service,migration);
  context.subscriptions.push(vscode.commands.registerCommand('brote.ask',async()=>{try{await native.ask();}catch(error){void vscode.window.showErrorMessage(String(error));}}));
  const participant=vscode.chat.createChatParticipant('brote.chat',async(req,history,stream,token)=>{
    try{
      await native.ready;
      if(req.command==='discuss'){
        const record=native.discussion(req.prompt.trim());if(!record)throw new Error('Saved discussion not found.');
        for(const turn of [...(record.turns||[]),record]){stream.markdown(`**You:** ${turn.question}\n\n`);if(turn.contextNote)stream.markdown(`${turn.contextNote}\n\n`);if(turn.answer)stream.markdown(`${turn.answer}\n\n`);}
        stream.markdown(`Saved outcome: ${record.status}\n\n`);
        return {metadata:{nativeDiscussion:record.id}};
      }
      const previous=[...history.history].reverse().find(turn=>turn instanceof vscode.ChatResponseTurn && turn.result.metadata?.nativeDiscussion);
      const id=previous instanceof vscode.ChatResponseTurn?String(previous.result.metadata?.nativeDiscussion):undefined;
      const saved=await native.chat(req.prompt,req.model,stream,token,id);
      return {metadata:{nativeDiscussion:saved}};
    }catch(error){stream.markdown(String(error));return;}
  });
  participant.iconPath=vscode.Uri.file(path.join(context.extensionPath,'assets','brote-plant.png'));
  context.subscriptions.push(participant,vscode.lm.registerTool('brote_inspect',{
    async invoke(){const target=vscode.debug.activeDebugSession;const evidence=await (target&&target.type!=='brote'?closeTracing.capture():native.capture());return new vscode.LanguageModelToolResult([new vscode.LanguageModelTextPart(JSON.stringify(evidence))]);},
  }));
  return {traces:closeTracing.traces,traceJSON:closeTracing.traceJSON};
}

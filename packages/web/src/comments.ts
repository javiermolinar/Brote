import {createClient} from '../../client/src/index';
import type {CommentThread as Thread} from '../../client/src/models';
interface State {cursor?:number;generation:number;status:string;frame:number;goroutine?:number;capabilities?:{comments?:boolean};binding?:{id:string;name:string};source?:{file:string;line:number}}
type Request = <T>(path:string,body?:Record<string,unknown>)=>Promise<T>;
const el=(tag:string,text?:string,cls?:string)=>{const e=document.createElement(tag);if(text)e.textContent=text;if(cls)e.className=cls;return e;};
function progress(thread:Thread):{text:string;busy:boolean} {
 const n=thread.delivery,name=n.binding?.name||'Agent';
 if(thread.resolved)return {text:'Resolved',busy:false};
 if(n.status==='answered')return {text:`${name} replied`,busy:false};
 if(!n.binding)return {text:'No agent connected · connect an agent, then retry',busy:false};
 const labels:Record<string,string>={pending:`Waiting to send to ${name}…`,sending:`Sending to ${name}…`,queued:`Delivered to ${name} · waiting for acknowledgement…`,thinking:`${name} is thinking…`,failed:`Could not deliver to ${name}`,unknown:'Delivery uncertain · check the agent conversation'};
 return {text:(labels[n.status]||n.status)+(n.error?' · '+n.error:''),busy:['pending','sending','queued','thinking'].includes(n.status)};
}
export function createComments(request:Request,navigate:(file:string,line:number)=>Promise<void>){
 const list=document.getElementById('commentList')!,pane=document.getElementById('source')!;
 const panel=el('section',undefined,'commentThread');panel.hidden=true;
 const heading=el('div',undefined,'commentHeading'),title=el('strong'),close=el('button','×');close.setAttribute('aria-label','Close comment');heading.append(title,close);
 const context=el('div',undefined,'commentContext'),messages=el('div',undefined,'commentMessages'),delivery=el('div',undefined,'commentDelivery');delivery.setAttribute('role','status');
 const captured=document.createElement('details');captured.className='commentCaptured';const capturedTitle=el('summary','Captured pause'),capturedValues=el('pre');captured.append(capturedTitle,capturedValues);
 const form=document.createElement('form'),input=document.createElement('textarea');input.placeholder='Ask the agent about this line…';input.setAttribute('aria-label','Comment for agent');input.required=true;input.maxLength=16000;
 const actions=el('div',undefined,'commentActions'),send=document.createElement('button'),resolve=document.createElement('button'),retry=document.createElement('button');send.type='submit';send.textContent='Ask agent';resolve.type=retry.type='button';retry.textContent='Retry delivery';actions.append(send,resolve,retry);form.append(input,actions);panel.append(heading,context,captured,messages,delivery,form);
 let state:State|undefined,threads:Thread[]=[],selected:Thread|undefined,anchor:{file:string;line:number;expression?:string}|undefined,capture:State|undefined,sourceFile='',key='',listKey='',pending=false,initialized=false,stopStream:(()=>void)|undefined;
 let available=false,errorText='',loadSequence=0,collapsed=false;
 const suggestion=document.createElement('button');suggestion.className='commentSuggestion';suggestion.hidden=true;suggestion.type='button';document.body.append(suggestion);
 let proposed:{file:string;line:number;expression?:string;label:string}|undefined;
 function suggest(file:string,line:number,label:string,rect:DOMRect,expression?:string){
  if(!available||state?.status!=='paused')return;
  proposed={file,line,expression,label};suggestion.textContent='Ask agent about “'+label.slice(0,60)+'”';suggestion.hidden=false;
  suggestion.style.left=Math.max(8,Math.min(rect.left,window.innerWidth-suggestion.offsetWidth-8))+'px';
  suggestion.style.top=Math.max(8,Math.min(rect.bottom+6,window.innerHeight-suggestion.offsetHeight-8))+'px';
 }
 suggestion.onclick=()=>{if(!proposed)return;const p=proposed;suggestion.hidden=true;void navigate(p.file,p.line).then(()=>{start(p.file,p.line,p.expression);if(!selected&&!input.value)input.value='About `'+p.label+'`: ';});};
 document.addEventListener('pointerdown',event=>{
  const target=event.target as Node;
  if(!panel.contains(target)&&!suggestion.contains(target)){collapsed=true;position();}
  if(!suggestion.contains(target))suggestion.hidden=true;
 });
 document.addEventListener('keydown',event=>{if(event.key==='Escape'){collapsed=true;suggestion.hidden=true;position();}});
 pane.addEventListener('mouseup',event=>{
  const target=event.target as Element;if(!target.closest('.sourceCodeLine'))return;
  const selection=window.getSelection();let range:Range|undefined,label='';
  if(selection&&!selection.isCollapsed&&selection.rangeCount){range=selection.getRangeAt(0);if(!pane.querySelector('.sourceCode')?.contains(range.commonAncestorContainer))return;label=selection.toString().trim();}
  else{
   const doc=document as Document&{caretRangeFromPoint?:(x:number,y:number)=>Range|null};
   range=doc.caretRangeFromPoint?.(event.clientX,event.clientY)||undefined;
   if(!range||range.startContainer.nodeType!==Node.TEXT_NODE)return;
   const text=range.startContainer.textContent||'',offset=range.startOffset;
   const left=text.slice(0,offset).match(/[\p{L}\p{N}_]+$/u)?.[0]||'',right=text.slice(offset).match(/^[\p{L}\p{N}_]+/u)?.[0]||'';
   label=left+right;if(!/^[\p{L}_]/u.test(label))return;
   range.setStart(range.startContainer,offset-left.length);range.setEnd(range.startContainer,offset+right.length);
  }
  const element=range?.startContainer.nodeType===Node.ELEMENT_NODE?range.startContainer as Element:range?.startContainer.parentElement;
  const row=element?.closest<HTMLElement>('.sourceCodeLine');
  if(label&&row&&range)suggest(sourceFile,Number(row.dataset.line),label,range.getBoundingClientRect());
 });
 function bubbles(){
  pane.querySelectorAll('.sourceNumber').forEach(row=>{
   const line=Number((row as HTMLElement).dataset.line),matches=threads.filter(t=>t.file===sourceFile&&t.line===line);
   let bubble=row.querySelector<HTMLButtonElement>('.commentGutter');
   if(!matches.length){bubble?.remove();return;}
   if(!bubble){bubble=document.createElement('button');bubble.className='commentGutter';bubble.innerHTML='<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H9l-6 4V6a2 2 0 0 1 2-2Z"/></svg>';row.append(bubble);}
   const active=matches.find(t=>!t.resolved)||matches[0],status=progress(active);
   bubble.title=bubble.ariaLabel=`Open comments on line ${line} · ${status.text}`;bubble.classList.toggle('awaitingAgent',status.busy);
   bubble.onclick=()=>void open(matches.find(t=>!t.resolved)||matches[0]);
  });
 }
 function start(file:string,line:number,expression?:string){if(!available)return;const wasCollapsed=collapsed;collapsed=false;const existing=threads.find(t=>!t.resolved&&t.file===file&&t.line===line&&t.expression===expression);if(existing){void open(existing);return;}if(wasCollapsed&&anchor&&!selected&&anchor.file===file&&anchor.line===line&&anchor.expression===expression){draw();input.focus({preventScroll:true});return;}errorText='';selected=undefined;anchor={file,line,expression};capture=state?{...state}:undefined;input.value='';draw();input.focus({preventScroll:true});panel.scrollIntoView({block:'nearest'});}

 const fail=(error:unknown)=>{delivery.classList.remove('awaitingAgent');errorText=error instanceof Error?error.message:String(error);delivery.textContent=errorText;};
 function position(){
  panel.hidden=collapsed||!anchor||anchor.file!==sourceFile;
  if(panel.hidden)return;
  const row=pane.querySelector<HTMLElement>(`.sourceNumber[data-line="${anchor!.line}"]`),content=pane.querySelector<HTMLElement>('.sourceContent');
  if(!row||!content){panel.hidden=true;return;}
  // Re-appending even to the same parent detaches the focused textarea.
  if(panel.parentElement!==content)content.append(panel);
  panel.style.width=Math.max(190,pane.clientWidth-90)+'px';panel.style.left=(pane.scrollLeft+76)+'px';panel.style.top=(row.offsetTop+row.offsetHeight)+'px';

 }
 if(typeof ResizeObserver!=='undefined'){new ResizeObserver(position).observe(panel);new ResizeObserver(position).observe(pane);}
 pane.addEventListener('scroll',()=>{if(!panel.hidden)panel.style.left=(pane.scrollLeft+76)+'px';});
 function draw(){
  bubbles();
  const nextListKey=JSON.stringify(threads.map(t=>[t.id,t.resolved,t.messages.length,t.delivery.status]));
  if(nextListKey!==listKey){listKey=nextListKey;list.replaceChildren();
  for(const t of threads){const button=el('button',`${t.resolved?'✓':'◌'} ${t.file.split('/').pop()}:${t.line} · ${t.messages[0]?.body.slice(0,70)}`,'commentListItem');const status=progress(t),hint=el('span',status.text,'commentProgress');hint.classList.toggle('awaitingAgent',status.busy);button.append(hint);button.onclick=()=>void open(t);list.append(button);}
  if(!threads.length)list.append(el('p','Select code or click a variable to ask the agent.','empty'));
  }
  if(!anchor){position();return;}
  selected=selected?threads.find(t=>t.id===selected!.id):undefined;
  title.textContent=`${anchor.file.split('/').pop()}:${anchor.line}${anchor.expression?' · '+anchor.expression:''}`;
  if(selected){
   context.textContent=`Captured ${new Date(selected.created).toLocaleTimeString()} · ${selected.context.frames?.[selected.context.frame||0]?.function?.name||'selected frame'} · historical values`;
   captured.hidden=false;
   const frame=selected.context.frames?.[selected.context.frame||0];
   capturedValues.textContent=[...(selected.context.frames||[]).map((f,i)=>`#${i} ${f.function?.name||'?'} ${f.file?.split('/').pop()||''}:${f.line||''}`),'',...[...(frame?.Arguments||[]),...(frame?.Locals||[])].map(v=>`${v.name} = ${v.value??'…'} (${v.type})`),'',...(selected.context.anchorSource?.lines||[])].join('\n');
   const nextKey=JSON.stringify(selected.messages);
   if(nextKey!==key){key=nextKey;messages.replaceChildren();for(const m of selected.messages){const item=el('div',undefined,'commentMessage');item.append(el('strong',m.author==='human'?'You':m.author),el('p',m.body));messages.append(item);}}
   const n=selected.delivery,status=progress(selected);if(delivery.textContent!==status.text)delivery.textContent=status.text;delivery.classList.toggle('awaitingAgent',status.busy);

   send.textContent='Ask follow-up';send.disabled=pending||!available||(!selected.resolved&&n.status!=='answered');
   resolve.hidden=false;resolve.textContent=selected.resolved?'Reopen':'Resolve';resolve.disabled=pending||!available;
   retry.hidden=selected.resolved||!['pending','failed','unknown'].includes(n.status);retry.disabled=pending||!available;
  }else{
   delivery.classList.remove('awaitingAgent');captured.hidden=true;messages.replaceChildren();key='';context.textContent=`Capture selected frame · ${capture?.source?.file.split('/').pop()||''}:${capture?.source?.line||''}`;
   delivery.textContent='Read-only question · execution stays with its current owner';send.textContent='Ask agent';send.disabled=pending||!available||state?.status!=='paused';resolve.hidden=retry.hidden=true;
  }
  if(errorText){delivery.textContent=errorText;delivery.classList.remove('awaitingAgent');}
  input.disabled=pending||send.disabled;position();
 }
 async function load(){const sequence=++loadSequence;try{const result=await request<{discussion:{threads:Thread[]}}>('comments');if(sequence!==loadSequence)return;threads=result.discussion.threads||[];draw();}catch(error){fail(error);}}
 async function open(t:Thread){collapsed=false;if(selected?.id!==t.id)input.value='';selected=t;anchor={file:t.file,line:t.line,expression:t.expression};key='';await navigate(t.file,t.line);draw();panel.scrollIntoView({block:'nearest'});}
 async function mutate(body:Record<string,unknown>){pending=true;errorText='';draw();try{const result=await request<{thread:Thread;eventError?:string}>('comments',body);selected=result.thread;input.value='';await load();if(result.eventError)fail('Saved, but event delivery failed. Reconnect the agent to reconcile pending questions.');}catch(error){fail(error);}finally{pending=false;draw();}}
 form.onsubmit=e=>{e.preventDefault();if(!anchor||!input.value.trim())return;void mutate(selected?{action:'ask',thread:selected.id,body:input.value}:{action:'create',...anchor,body:input.value,generation:capture?.generation,goroutine:capture?.goroutine,frame:capture?.frame});};
 close.onclick=()=>{collapsed=true;draw();};resolve.onclick=()=>{if(selected)void mutate({action:selected.resolved?'reopen':'resolve',thread:selected.id});};retry.onclick=()=>{if(selected&&confirm('Retry this question? If delivery was uncertain, check the agent conversation first to avoid a duplicate.'))void mutate({action:'retry',thread:selected.id});};
 return {
  update(next:State){state=next;available=!!next.capabilities?.comments;document.getElementById('commentsPanel')!.hidden=!available&&!threads.length;
   if(available&&!initialized){initialized=true;void load();if(typeof EventSource!=='undefined'){
    const api=createClient({baseURL:location.origin,session:new URLSearchParams(location.search).get('session')||undefined});
    stopStream=api.subscribe(next.cursor||0,event=>{if(['question.created','reply.added','thread.updated','thread.resolved'].includes(event.kind))void load();},()=>{stopStream?.();initialized=false;},()=>void load());

   }}
   pane.querySelectorAll<HTMLButtonElement>('.commentGutter').forEach(b=>{b.disabled=!available;});draw();
  },
  source(file:string){if(sourceFile!==file)suggestion.hidden=true;sourceFile=file;bubbles();position();},
  start, suggest,

 };
}

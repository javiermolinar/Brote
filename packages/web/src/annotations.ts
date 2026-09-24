import type {Snapshot, Annotation, EvidenceTarget} from '../../client/src/models';
type Evidence=EvidenceTarget & {file?:string;line?:number;created?:string};
type Request=<T>(path:string,body?:Record<string,unknown>)=>Promise<T>;
const element=<K extends keyof HTMLElementTagNameMap>(tag:K,text?:string)=>{const e=document.createElement(tag);if(text)e.textContent=text;return e;};
const same=(a:EvidenceTarget,b:EvidenceTarget)=>a.session===b.session&&a.traceId===b.traceId&&a.spanId===b.spanId;
export function createAnnotations(request:Request){
 const host=document.getElementById('annotationPanel')!,tabs=document.getElementById('evidenceTabs')!;
 const discussion=document.getElementById('discussionPane')!,discussionTab=document.getElementById('discussionTab')!,annotationTab=document.getElementById('annotationTab')!;
 const context=element('div'),picker=element('select'),add=element('button','Add annotation'),refresh=element('button','Refresh'),list=element('div'),status=element('p');
 context.className='annotationTarget';picker.setAttribute('aria-label','Annotation evidence');status.setAttribute('role','status');
 const actions=element('div');actions.className='annotationActions';actions.append(add,refresh);
 const editor=element('div'),fixed=element('strong'),label=element('label','Note'),body=element('textarea'),details=element('details'),summary=element('summary','Optional details'),tag=element('input'),key=element('input'),save=element('button','Save annotation'),cancel=element('button','Cancel');
 body.id='annotationBody';label.htmlFor=body.id;body.maxLength=16000;body.placeholder='What is worth remembering about this evidence?';
 tag.id='annotationLabel';key.id='annotationKey';tag.maxLength=key.maxLength=128;
 const tagLabel=element('label','Label'),keyLabel=element('label','Comparison key');tagLabel.htmlFor=tag.id;keyLabel.htmlFor=key.id;
 details.append(summary,tagLabel,tag,keyLabel,key);const buttons=element('div');buttons.className='annotationActions';buttons.append(cancel,save);
 editor.className='annotationEditor';editor.hidden=true;editor.append(fixed,label,body,details,buttons);host.append(context,picker,actions,editor,status,list);
 let targets:Evidence[]=[],selected:Evidence|undefined,records:Annotation[]=[],generation=0,scope='',visible=false,pending=false;
 let draft: {id:string;target:EvidenceTarget;context:string;body?:string;label?:string;key?:string}|undefined;
 function tab(show:boolean){visible=show;annotationTab.setAttribute('aria-selected',String(show));discussionTab.setAttribute('aria-selected',String(!show));annotationTab.tabIndex=show?0:-1;discussionTab.tabIndex=show?-1:0;host.hidden=!show;discussion.hidden=show; if(show)void load();}
 annotationTab.onclick=()=>tab(true);discussionTab.onclick=()=>tab(false);
 tabs.onkeydown=e=>{if(e.key==='ArrowLeft'||e.key==='ArrowRight'){e.preventDefault();tab(!visible);(visible?annotationTab:discussionTab).focus();}};
 document.addEventListener('brote:discussion',()=>{tab(false);const main=document.querySelector('main');if(main)main.dataset.pane='discussion';document.querySelectorAll<HTMLElement>('.paneNav [data-pane]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.pane==='discussion')));});
 function targetText(t:EvidenceTarget){return `Run ${t.session.split(':').at(-1)!.slice(0,8)} · ${t.captureId?'Capture '+t.captureId.slice(0,8):'Program root (no capture link)'}`;}
 function contextText(){return selected?`${selected.file?selected.file.split('/').at(-1)+':'+selected.line+' · ':''}${targetText(selected)}${selected.created&&!selected.created.startsWith('0001')?' · '+new Date(selected.created).toLocaleString():''}`:'No verified trace identity';}
 function controls(){save.disabled=pending||!body.value.trim();cancel.disabled=pending;add.disabled=pending||!selected||!!draft;picker.disabled=pending||!!draft;refresh.disabled=pending||!!draft;body.disabled=tag.disabled=key.disabled=pending;}
 function draw(){
  context.textContent=contextText();context.title=selected?JSON.stringify(selected):'';annotationTab.textContent=`Annotations (${records.filter(a=>selected&&a.targets.some(t=>same(t,selected!))).length})`;list.replaceChildren();
  for(const a of records.filter(a=>selected&&a.targets.some(t=>same(t,selected!)))){
   const article=element('article');article.className='annotationNote';const heading=element('div'),author=element('strong',a.author),time=element('time',new Date(a.created).toLocaleString());time.dateTime=a.created;heading.append(author,time);
   article.append(heading,element('p',a.body),element('small',a.targets.map(targetText).join(' · ')));
   if(a.label)article.append(element('div',a.label));if(a.comparisonKey)article.append(element('small','Comparison · '+a.comparisonKey));
   if(a.conversationThread&&a.conversationRun){const link=element('a','View conversation');link.href='/?history='+encodeURIComponent(a.conversationRun)+'&thread='+encodeURIComponent(a.conversationThread);article.append(link);}
   article.append(element('small','Saved locally · '+(a.export?.map(s=>`Trace export: local ${s.local}, remote ${s.remote}${s.error?' · '+s.error:''}`).join('; ')||'Trace export pending')));list.append(article);
  }
  controls();
 }
 async function load(){const g=++generation;try{
  const next=await request<Evidence[]>('annotation-evidence');if(g!==generation)return;
  targets=next;selected=targets.find(t=>selected&&same(t,selected))||targets[0];picker.replaceChildren();
  targets.forEach((t,i)=>{const option=element('option',targetText(t));option.value=String(i);option.selected=!!selected&&same(t,selected);picker.append(option);});
  const loaded=selected?await request<Annotation[]>('annotations?traceSession='+encodeURIComponent(selected.session)):[];if(g!==generation)return;records=loaded;status.textContent=selected?'':'This saved evidence has no verified trace identity.';draw();
 }catch(e){if(g===generation){status.textContent=String(e);controls();}}}
 picker.onchange=()=>{selected=targets[Number(picker.value)];void load();};refresh.onclick=()=>void load();
 add.onclick=()=>{if(!selected||draft)return;draft={id:crypto.randomUUID(),target:{session:selected.session,traceId:selected.traceId,spanId:selected.spanId,...(selected.captureId?{captureId:selected.captureId}:{})},context:contextText()};fixed.textContent=draft.context;editor.hidden=false;body.value=tag.value=key.value='';details.open=false;controls();body.focus();};
 cancel.onclick=()=>{if(pending)return;draft=undefined;editor.hidden=true;controls();};body.oninput=controls;
 save.onclick=async()=>{
  if(pending||!draft||!body.value.trim())return;
  const d=draft;pending=true;controls();status.textContent='Saving…';
  // Retain the ID across retries. Edited content after an uncertain save conflicts
  // at the service rather than creating a duplicate note.
  d.body=body.value;d.label=tag.value;d.key=key.value;
  try {await request<Annotation>('annotations',{session:d.target.session,id:d.id,revision:1,author:'human',body:d.body,label:d.label,comparisonKey:d.key,targets:[d.target]});draft=undefined;editor.hidden=true;status.textContent='Saved locally';await load();}
  catch(e){status.textContent=String(e)+' · Your text is retained. Retry unchanged after uncertain delivery.';}
  finally{pending=false;controls();}
 };
 return {update(next:Snapshot){const k=next.id+':'+(next.run||'');if(k!==scope){scope=k;selected=undefined;records=[];generation++;if(!pending){draft=undefined;editor.hidden=true;}if(visible)void load();}if(visible){discussion.hidden=true;draw();}},open:()=>tab(true)};
}

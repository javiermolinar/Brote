/** Presentation-only notes from the saved payload; never infer live state. */
export function evidenceNotes(value:unknown):string[]{
 const notes:string[]=[];
 const walk=(v:unknown,path:string)=>{
  if(!v||typeof v!=='object')return;
  for(const [key,item] of Object.entries(v)){
   const at=path?`${path}.${key}`:key;
   if(/error|truncated|partial|unreadable/i.test(key)&&item!==false&&item!==''&&item!==null&&item!==undefined&&item!==0){
    const text=typeof item==='string'?item:JSON.stringify(item);notes.push(`${at}: ${text}`);
   }else walk(item,at);
  }
 };walk(value,'');return notes;
}

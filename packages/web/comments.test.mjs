import {test} from 'node:test';
import assert from 'node:assert/strict';
import {JSDOM} from 'jsdom';
import {build} from 'esbuild';

test('comments capture the original draft scope, preserve errors, and receive replies through events',async t=>{
 const js=await build({entryPoints:['packages/web/src/comments.ts'],bundle:true,write:false,format:'iife',globalName:'Comments',logLevel:'silent'});
 const dom=new JSDOM('<div id="source"><div class="sourceContent"><div class="sourceNumber" data-line="18"></div></div></div><section id="commentsPanel"><div id="commentList"></div></section>',{url:'http://127.0.0.1',runScripts:'outside-only'});t.after(()=>dom.window.close());
 const w=dom.window,d=w.document;w.HTMLElement.prototype.scrollIntoView=function(){};
 let stream,threads=[],posts=[],fail=false;
 w.EventSource=class{constructor(){stream=this;}addEventListener(){}close(){}};
 w.eval(js.outputFiles[0].text+';window.CommentUI=Comments;');
 const flush=()=>new Promise(r=>setTimeout(r,0));
 const context={frame:0,frames:[{function:{name:'main.process'},Locals:[{name:'total',value:'21',type:'int'}]}]};
 const api=async(path,body)=>{if(!body)return {discussion:{threads}};posts.push(body);if(fail)throw Error('pause changed');const thread={id:'thread',file:'main.go',line:18,created:new Date().toISOString(),context,messages:[{id:'question',author:'human',body:body.body}],delivery:{question:'question',status:'pending',binding:{name:'Pi'}}};threads=[thread];return {thread};};
 const ui=w.CommentUI.createComments(api,async()=>{});
 const state={generation:1,frame:0,goroutine:1,status:'paused',capabilities:{comments:true},cursor:7,source:{file:'main.go',line:18}};
 ui.update(state);ui.source('main.go');await flush();ui.start('main.go',18);
 assert.equal(d.querySelector('.commentGutter'),null,'empty lines have no comment button');
 const input=d.querySelector('textarea');input.value='<script>unsafe</script>';
 input.focus();input.setSelectionRange(3,8);
 for(let i=0;i<3;i++){ui.source('main.go');ui.update({...state});}
 assert.equal(d.activeElement,input,'refresh must not detach the focused comment editor');
 assert.equal(input.value,'<script>unsafe</script>');
 assert.equal(input.selectionStart,3);assert.equal(input.selectionEnd,8);
 assert.equal(d.querySelector('[data-comment-space]'),null,'comment overlay must not reserve space in the code');
 assert.equal(d.querySelector('.sourceNumber').style.marginBottom,'');

 ui.update({...state,generation:2});fail=true;d.querySelector('form').dispatchEvent(new w.Event('submit',{cancelable:true}));await flush();
 assert.equal(posts[0].generation,1,'draft must reject a changed pause rather than silently changing its captured scope');
 ui.update({...state,generation:2});assert.match(d.querySelector('.commentDelivery').textContent,/pause changed/);
 fail=false;ui.start('main.go',18);input.value='Why total?';d.querySelector('form').dispatchEvent(new w.Event('submit',{cancelable:true}));await flush();
 assert.equal(posts.at(-1).generation,2);assert.match(d.querySelector('.commentMessages').textContent,/Why total/);
 assert.match(d.querySelector('.commentCaptured').textContent,/total = 21/);
 for(const [status,label] of [['queued','Delivered to Pi'],['thinking','Pi is thinking']]){
  threads[0].delivery.status=status;stream.onmessage({data:JSON.stringify({kind:'thread.updated'})});await flush();
  assert.match(d.querySelector('.commentDelivery').textContent,new RegExp(label));
  assert.equal(d.querySelector('.commentDelivery').classList.contains('awaitingAgent'),true);
  assert.match(d.querySelector('.commentListItem').textContent,new RegExp(label));
 }
 threads[0].messages.push({id:'reply',author:'Pi',body:'<img src=x onerror=alert(1)> 7 + 14 = 21'});threads[0].delivery.status='answered';
 stream.onmessage({data:JSON.stringify({kind:'reply.added'})});await flush();
 assert.match(d.querySelector('.commentMessages').textContent,/7 \+ 14 = 21/);assert.equal(d.querySelector('.commentMessages img'),null);
 assert.equal(d.querySelector('textarea').disabled,false);
 assert.equal(d.querySelector('.commentDelivery').classList.contains('awaitingAgent'),false);
 input.value='Follow-up draft';
 d.body.dispatchEvent(new w.Event('pointerdown',{bubbles:true}));
 assert.equal(d.querySelector('.commentThread').hidden,true);
 ui.update(state);assert.equal(d.querySelector('.commentThread').hidden,true,'refresh keeps the overlay collapsed');
 const bubble=d.querySelector('.commentGutter');assert.ok(bubble);bubble.click();await flush();
 assert.equal(d.querySelector('.commentThread').hidden,false);
 assert.equal(input.value,'Follow-up draft','collapse and reopen preserve the draft');
 ui.suggest('main.go',18,'total',{left:10,bottom:20},'total');
 assert.equal(d.querySelector('.commentSuggestion').hidden,false);
 assert.match(d.querySelector('.commentSuggestion').textContent,/total/);
 d.dispatchEvent(new w.KeyboardEvent('keydown',{key:'Escape'}));
 assert.equal(d.querySelector('.commentSuggestion').hidden,true);

});

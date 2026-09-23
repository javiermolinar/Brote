import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {JSDOM} from 'jsdom';
import {build} from 'esbuild';

test('stepping retains last pause, expansions and stable source; stale controls are disabled', async t => {
 const html = await readFile('packages/web/public/index.html','utf8');
 const js = await build({entryPoints:['packages/web/src/app.ts'],bundle:true,write:false,format:'iife',logLevel:'silent'});
 const dom = new JSDOM(html,{url:'http://127.0.0.1:1234',runScripts:'outside-only'});
 t.after(()=>dom.window.close());
 const w=dom.window,d=w.document;
 let tick, fail=false;
 let state={id:'test',owner:'browser',generation:1,status:'paused',state:{Pid:1},project:'/demo',binary:'/demo/bin',sourceIdentity:{match:'mismatch'},frame:0,goroutine:1,goroutines:[{id:1}],frames:[{file:'main.go',line:18,function:{name:'main.process'},Locals:[{name:'value',type:'S',children:[{name:'n',type:'int',value:'21'}]}]}],source:{file:'main.go',start:18,line:18,lines:['total += delta']}};
 w.setInterval=fn=>{tick=fn;return 1;};
 const calls=[];
 w.fetch=async(url,options)=>{url=new URL(url,w.location.href).pathname+new URL(url,w.location.href).search;calls.push({url,options});if(url==='/api/action')return {ok:true,json:async()=>JSON.parse(options.body).action==='eval'?{value:{name:'value',type:'S',children:[{name:'n',type:'int',value:'42'}]},generation:state.generation,goroutine:state.goroutine,frame:state.frame}:{Breakpoint:{file:'main.go',line:23}}};if(url.startsWith('/api/sources?'))return {ok:true,json:async()=>({file:'/demo/other.go',start:1,line:0,lines:['package main','func other() {}']})};if(url==='/api/sources')return {ok:true,json:async()=>({files:['/demo/other.go']})};if(url==='/api/sessions')return {ok:true,json:async()=>({sessions:[{id:'test',project:'/demo',binary:'/demo/bin',status:'paused',owner:'browser',panel:'http://127.0.0.1:1234/'},{id:'other',project:'/other',binary:'/other/app',status:'paused',owner:'agent',binding:{name:'Pi'},panel:'http://127.0.0.1:5678/'},{id:'offline',project:'/old',binary:'/old/app',status:'offline'}]})};if(fail)throw Error('offline');return {ok:true,json:async()=>structuredClone(state)};};
 const flush=()=>new Promise(r=>setTimeout(r,0));
 w.eval(js.outputFiles[0].text);await flush();
 d.querySelector('#refreshSessions').click();await flush();
 assert.equal(d.querySelectorAll('.sessionRow').length,3);
 assert.equal(d.querySelectorAll('#runList .runItem').length,1);
 assert.match(d.querySelector('#sessionList').textContent,/Current/);
 assert.ok(calls.every(c=>c.options.method==='GET'),'listing must not mutate session');
 w.HTMLDialogElement.prototype.showModal=function(){this.open=true;};
 w.HTMLDialogElement.prototype.close=function(){this.open=false;};
 d.querySelector('#openFile').click();await flush();
 d.querySelector('.fileResult').click();await flush();
 assert.equal(d.querySelectorAll('[role=tab]').length,2);
 assert.match(d.querySelector('#filename').textContent,/other.go/);
 assert.match(d.querySelector('#frameName').textContent,/main.process/,'browsing must preserve locals scope');
 tick();await flush();assert.match(d.querySelector('#filename').textContent,/other.go/,'polling must not steal active tab');
 d.querySelector('#followSource').click();
 const source=d.querySelector('.sourceContent'),details=d.querySelector('#locals details'),frame=d.querySelector('#frames button');
 details.open=true;
 state={...state,status:'running',generation:2,frames:undefined,source:undefined,sourceIdentity:undefined,state:{}};tick();await flush();
 assert.equal(d.querySelector('.sourceContent'),source);
 assert.equal(d.querySelector('#locals details'),details);
 assert.equal(frame.disabled,true);
 assert.equal(d.querySelector('#sourceWarning').hidden,false,'warning must keep its space during a step');
 assert.match(d.querySelector('#session').textContent,/PID 1/);
 assert.equal(d.querySelector('main').dataset.executing,'true');
 assert.match(d.querySelector('#stopReason').textContent,/last pause/);
 state={...state,status:'paused',frames:[{file:'main.go',line:18,function:{name:'main.process'},Locals:[{name:'value',type:'S',children:[{name:'n',type:'int',value:'42'}]}]}],source:{file:'main.go',start:18,line:18,lines:['total += delta']}};tick();await flush();
 assert.equal(d.querySelector('.sourceContent'),source);
 assert.equal(d.querySelector('#locals details').open,true);
 assert.match(d.querySelector('#locals').textContent,/42/);
 assert.equal(d.querySelector('#frames button').disabled,false);
 const lines=Array.from({length:27},(_,i)=>'// line '+(i+10));
 state={...state,source:{file:'main.go',start:10,line:22,lines}};tick();await flush();
 const stableCode=d.querySelector('.sourceContent');
 state={...state,generation:3,source:{file:'main.go',start:11,line:23,lines:[...lines.slice(1),'// line 37']}};tick();await flush();
 assert.equal(d.querySelector('.sourceContent'),stableCode,'overlapping step should move marker without replacing code');
 assert.equal(d.querySelector('.sourceHighlight.current').dataset.line,'23');
 fail=true;tick();await flush();assert.equal(d.querySelector('.sourceContent'),stableCode);assert.equal(d.querySelector('#frames button').disabled,true);
 fail=false;tick();await flush();assert.equal(d.querySelector('#frames button').disabled,false);
 assert.equal(d.querySelector('.sourceNumber[data-line="23"]').tagName,'DIV');
 assert.equal(d.querySelector('.sourceNumber[data-line="23"] .lineLabel').textContent,'23');
 state={...state,owner:'agent'};tick();await flush();
 assert.equal(d.querySelector('.breakpointGutter[data-line="23"]').disabled,false);
 assert.equal(d.querySelector('[data-action=step]').disabled,false);
 assert.equal(d.querySelector('#takeBrowser'),null);
 assert.match(d.querySelector('#agentStatus').textContent,/Not reported/);
 d.querySelector('.breakpointGutter[data-line="23"]').click();await flush();
 const action=JSON.parse(calls.find(c=>c.url==='/api/action').options.body);
 assert.equal(action.action,'break');assert.equal(action.file,'main.go');assert.equal(action.line,23);
 assert.equal(action.generation,3);assert.equal(action.actor,'browser');
 assert.match(d.querySelector('#notice').textContent,/main.go:23/);
 d.querySelector('#source').scrollTop=200;
 state={...state,breakpoints:[{id:2,file:'/demo/other.go',line:2},{id:1,file:'main.go',line:23}]};tick();await flush();
 assert.equal(d.querySelector('#source').scrollTop,200,'breakpoint refresh must not recenter the paused line');
 d.querySelector('.breakpointLink').click();await flush();
 assert.match(d.querySelector('#filename').textContent,/other.go/);
 assert.equal(d.querySelector('.sourceHighlight.navigated').dataset.line,'2');
 assert.match(d.querySelector('#frameName').textContent,/main.process/);
 const revealed=[];
 w.HTMLElement.prototype.scrollIntoView=function(){revealed.push(this.id);};
 assert.equal(d.querySelector('#locals .inspect'),null);
 d.querySelector('#expression').value='value';
 d.querySelector('#evaluate').click();await flush();
 assert.match(d.querySelector('#evaluation').textContent,/42/);
 assert.equal(d.querySelector('#evaluation details').open,true);
 assert.ok(revealed.includes('evaluation'));
 assert.equal(d.querySelector('#addWatch').hidden,false);
 const evaluationCall=JSON.parse(calls.at(-2).options.body || '{}');
 assert.equal(evaluationCall.action,'eval');
 assert.equal(evaluationCall.frame,state.frame);
 state={...state,owner:'browser',frames:[{file:'main.go',line:18,function:{name:'main.process'},Locals:[{name:'value',type:'S',kind:25,len:2,children:[{name:'n',type:'int',value:'21'}]}]}]};tick();await flush();
 assert.equal(d.querySelector('#locals .pinButton').getAttribute('aria-pressed'),'false');
 const localOpen=d.querySelector('#locals details').open;
 d.querySelector('#locals .pinButton').click();await flush();
 assert.equal(d.querySelector('#locals details').open,localOpen,'pinning must not toggle expansion');
 const pinAction=JSON.parse(calls.filter(c=>c.url==='/api/action').at(-1).options.body);
 assert.equal(pinAction.action,'watch');assert.equal(pinAction.expression,'value');
 d.querySelector('#locals .loadMore').click();await flush();
 assert.match(d.querySelector('#locals').textContent,/42/,'load more replaces the inline value');
 assert.equal(d.querySelector('#locals details').open,true);
 d.querySelector('#expression').value='edited input';
 d.querySelector('#addWatch').click();await flush();
 assert.equal(JSON.parse(calls.filter(c=>c.url==='/api/action').at(-1).options.body).expression,'value','save uses the evaluated expression, not edited input');
 state={...state,watches:[{expression:'value',value:{name:'value',type:'int',value:'42'}}]};tick();await flush();
 assert.equal(d.querySelector('#evaluation').hidden,true,'pinned results are not duplicated');
 assert.equal(d.querySelector('#addWatch').hidden,true);
 assert.match(d.querySelector('#watches').textContent,/42/);
 assert.equal(d.querySelector('#locals .pinButton').getAttribute('aria-pressed'),'true');
 d.querySelector('#locals .pinButton').click();await flush();
 assert.equal(JSON.parse(calls.filter(c=>c.url==='/api/action').at(-1).options.body).action,'unwatch');
 d.querySelector('#watches button').click();await flush();
 const unpin=JSON.parse(calls.filter(c=>c.url==='/api/action').at(-1).options.body);
 assert.equal(unpin.action,'unwatch');
 assert.equal(unpin.expression,'value');


});

test('inspector and chat investigations share cancellation while comments stay read-only', async t => {
 const html=await readFile('packages/web/public/index.html','utf8');
 const js=await build({entryPoints:['packages/web/src/app.ts'],bundle:true,write:false,format:'iife',logLevel:'silent'});
 const dom=new JSDOM(html,{url:'http://127.0.0.1:1234',runScripts:'outside-only'});t.after(()=>dom.window.close());
 const w=dom.window,d=w.document;let tick;const calls=[];
 let state={id:'test',owner:'agent',binding:{id:'pi',revision:1,name:'Pi'},agentConnected:true,capabilities:{executionTasks:true},generation:1,status:'paused',state:{Pid:1},project:'/demo',binary:'/demo/bin',frame:0,frames:[{file:null,line:0}]};
 w.setInterval=fn=>{tick=fn;return 1};
 w.fetch=async(url,options)=>{
  const path=new URL(url,w.location.href).pathname;
  if(path==='/api/action'){
   const body=JSON.parse(options.body);calls.push(body);
   if(body.action==='task-authorize')state={...state,generation:2,task:{id:'task1',instruction:body.instruction,status:'authorized'}};
   if(body.action==='task-cancel')state={...state,generation:3,task:{...state.task,status:'cancelled',reason:'cancelled by browser'}};
   return {ok:true,json:async()=>({task:state.task})};
  }
  if(path==='/api/sessions')return {ok:true,json:async()=>({sessions:[]})};
  return {ok:true,json:async()=>structuredClone(state)};
 };
 const flush=()=>new Promise(r=>setTimeout(r,0));w.eval(js.outputFiles[0].text);await flush();
 assert.equal(d.querySelector('#status').textContent,'Paused');
 assert.equal(d.querySelector('#handover'),null);assert.equal(d.querySelector('#editor'),null);
 assert.equal(d.querySelector('[data-action=next]').disabled,false);
 assert.match(d.querySelector('#agentStatus').textContent,/Pi.*Connected/);
 assert.equal(calls.length,0);
 const input=d.querySelector('#taskInstruction');input.value='Find why retry repeats';
 d.querySelector('#taskForm').dispatchEvent(new w.Event('submit',{cancelable:true}));await flush();
 assert.equal(calls[0].action,'task-authorize');assert.equal(calls[0].actor,'browser');assert.equal(calls[0].instruction,input.value);
 assert.equal(d.querySelector('#stopAgent').hidden,false);
 assert.equal(d.querySelector('#authorizeTask').disabled,true);
 assert.equal(input.value,'Find why retry repeats');tick();await flush();assert.equal(input.value,'Find why retry repeats');
 d.querySelector('#stopAgent').click();await flush();
 assert.equal(calls[1].action,'task-cancel');assert.equal(calls[1].task,'task1');assert.equal(d.querySelector('#stopAgent').hidden,true);
 assert.equal(d.querySelector('#authorizeTask').disabled,false);assert.match(d.querySelector('#taskStatus').textContent,/cancelled/);
 state={...state,task:{id:'chat-task',instruction:'Debug the selected test',status:'authorized',delivery:'acknowledged'}};tick();await flush();
 assert.match(d.querySelector('#agentStatus').textContent,/Pi.*Debugging/);
 assert.equal(d.querySelector('#stopAgent').hidden,false);
 assert.equal(calls.length,2,'rendering a chat task never starts or authorizes another task');
 state={...state,task:undefined,agentConnected:false};tick();await flush();
 assert.equal(d.querySelector('#authorizeTask').disabled,true);
 assert.match(d.querySelector('#taskStatus').textContent,/Attach your agent/);
 state={...state,beforeGoStart:true,frames:[]};tick();await flush();
 assert.match(d.querySelector('#filename').textContent,/Paused before Go starts/);
 assert.equal(d.querySelector('[data-action=step]').disabled,true);
 assert.equal(d.querySelector('[data-action=continue]').disabled,false);
 assert.equal(d.querySelectorAll('#frames button').length,0);
 assert.match(d.querySelector('#source').textContent,/set a breakpoint/);
});

test('unavailable archived run keeps its label and does not poll the missing snapshot', async t=>{
 const html=await readFile('packages/web/public/index.html','utf8');
 const js=await build({entryPoints:['packages/web/src/app.ts'],bundle:true,write:false,format:'iife',logLevel:'silent'});
 const dom=new JSDOM(html,{url:'http://127.0.0.1:1234/?history=f188abf6a0',runScripts:'outside-only'});t.after(()=>dom.window.close());
 const w=dom.window,d=w.document;let tick;let archiveRequests=0;
 w.setInterval=fn=>{tick=fn;return 1};
 w.fetch=async url=>{if(String(url).includes('saved-run')){archiveRequests++;return {ok:false,status:404,json:async()=>({error:'file does not exist'})};}return {ok:true,json:async()=>({investigations:[{id:'f188abf6a0',title:'Demo',project:'/demo',runs:[{id:'f188abf6a0',status:'ended'}]}]})};};
 w.eval(js.outputFiles[0].text);const flush=()=>new Promise(r=>setTimeout(r,0));await flush();await flush();
 const label=d.querySelector('#session'),before=label.textContent,count=archiveRequests;assert.match(before,/f188abf6a0/);
 let changes=0;const observer=new w.MutationObserver(()=>changes++);observer.observe(label,{childList:true,characterData:true,subtree:true});
 for(let i=0;i<4;i++){tick();await flush();}
 assert.equal(archiveRequests,count);assert.equal(label.textContent,before);assert.equal(changes,0);assert.equal(d.querySelector('#status').textContent,'Ended');observer.disconnect();assert.equal(d.querySelector('.controls').hidden,true);assert.equal(d.querySelector('.inspectionDock').hidden,true);assert.match(d.querySelector('#filename').textContent,/recorded source|unavailable/i);assert.equal(d.querySelector('#openFile').hidden,true);
});

test('archived missing snapshot hides live controls and keeps saved discussions available',async t=>{
 const html=await readFile('packages/web/public/index.html','utf8');const js=await build({entryPoints:['packages/web/src/app.ts'],bundle:true,write:false,format:'iife',logLevel:'silent'});
 const dom=new JSDOM(html,{url:'http://127.0.0.1:1234/?history=1111111111',runScripts:'outside-only'});t.after(()=>dom.window.close());const w=dom.window,d=w.document;w.setInterval=()=>1;
 w.fetch=async url=>({ok:true,json:async()=>String(url).includes('saved-run')?{id:'1111111111',historical:true,runEnded:true,snapshotUnavailable:true,status:'exited',state:{},project:'/demo',binary:'/demo/bin',frames:[],discussion:{threads:[]}}:{investigations:[{id:'1111111111',title:'Demo',project:'/demo',created:'2026-09-20T10:00:00Z',runs:[{id:'1111111111',ordinal:1,status:'ended',created:'2026-09-20T10:00:00Z'}]}]}});
 w.eval(js.outputFiles[0].text);await new Promise(r=>setTimeout(r,0));await new Promise(r=>setTimeout(r,0));
 assert.equal(d.querySelector('.controls').hidden,true);assert.equal(d.querySelector('#goroutines').hidden,true);assert.equal(d.querySelector('#reconnectAgent').hidden,true);assert.equal(d.querySelector('.inspectionDock').hidden,true);assert.equal(d.querySelector('#openFile').hidden,true);assert.match(d.querySelector('#frames').textContent,/No call stack recorded/);assert.match(d.querySelector('#commentList').textContent,/No saved discussions/);assert.match(d.querySelector('#investigationDate').textContent,/2026/);assert.equal(d.querySelector('#runTitle').textContent,'Run 1');
});

test('entry pause followed by exit clears stale launch instructions and offers rerun',async t=>{
 const html=await readFile('packages/web/public/index.html','utf8');const js=await build({entryPoints:['packages/web/src/app.ts'],bundle:true,write:false,format:'iife',logLevel:'silent'});const dom=new JSDOM(html,{url:'http://127.0.0.1:1234/?session=1111111111',runScripts:'outside-only'});t.after(()=>dom.window.close());const w=dom.window,d=w.document;let tick;w.setInterval=fn=>{tick=fn;return 1};let state={id:'1111111111',project:'/demo',binary:'/demo/bin',status:'paused',state:{Pid:42},beforeGoStart:true,frames:[]};
 w.fetch=async url=>({ok:true,json:async()=>String(url).includes('workspace')?{investigations:[{id:'1111111111',title:'Demo',project:'/demo',runs:[{id:'1111111111',status:'paused',binary:'/demo/bin'}]}]}:String(url).includes('comments')?{threads:[]}:structuredClone(state)});
 w.eval(js.outputFiles[0].text);await new Promise(r=>setTimeout(r,0));assert.match(d.querySelector('#filename').textContent,/Paused before/);state={...state,status:'exited',beforeGoStart:false};tick();await new Promise(r=>setTimeout(r,0));assert.equal(d.querySelector('#filename').textContent,'Program exited');assert.match(d.querySelector('#runList').textContent,/exited/);assert.equal(d.querySelector('#headerRunAgain').hidden,false);assert.equal(d.querySelector('.controls').hidden,true);assert.equal(d.querySelector('#newQuestion').hidden,true);
});

test('file picker preserves source-load failure when search text changes',async t=>{
 const html=await readFile('packages/web/public/index.html','utf8');const js=await build({entryPoints:['packages/web/src/app.ts'],bundle:true,write:false,format:'iife',logLevel:'silent'});const dom=new JSDOM(html,{url:'http://127.0.0.1:1234/?session=1111111111',runScripts:'outside-only'});t.after(()=>dom.window.close());const w=dom.window,d=w.document;w.setInterval=()=>1;w.HTMLDialogElement.prototype.showModal=function(){this.open=true;};
 w.fetch=async url=>String(url).includes('sources')?{ok:false,status:409,json:async()=>({error:'Process has exited'})}:{ok:true,json:async()=>String(url).includes('workspace')?{investigations:[]}:String(url).includes('comments')?{threads:[]}:{id:'1111111111',status:'paused',project:'/demo',binary:'/demo/bin',state:{},frames:[]}};
 w.eval(js.outputFiles[0].text);await new Promise(r=>setTimeout(r,0));d.querySelector('#openFile').click();await new Promise(r=>setTimeout(r,0));const field=d.querySelector('#fileSearch');field.value='main';field.dispatchEvent(new w.Event('input'));assert.match(d.querySelector('#fileSearchStatus').textContent,/Process has exited/);assert.doesNotMatch(d.querySelector('#fileSearchStatus').textContent,/0 files/);
});

test('saved breakpoint gutters cannot prompt or mutate',async t=>{
 const html=await readFile('packages/web/public/index.html','utf8'),js=await build({entryPoints:['packages/web/src/app.ts'],bundle:true,write:false,format:'iife'});
 const dom=new JSDOM(html,{url:'http://127.0.0.1/?history=1111111111',runScripts:'outside-only'});t.after(()=>dom.window.close());const w=dom.window,d=w.document;w.setInterval=()=>1;let prompts=0;w.prompt=()=>{prompts++;return 'true'};const calls=[];
 w.fetch=async(url,options)=>{calls.push({url:String(url),options});return {ok:true,json:async()=>String(url).includes('saved-run')?{id:'1111111111',historical:true,status:'paused',state:{},frame:0,source:{file:'main.go',start:1,line:1,lines:['func main() {}']},breakpoints:[{id:1,file:'main.go',line:1,Cond:'x == 1'}],discussion:{threads:[]}}:{investigations:[]}}};
 w.eval(js.outputFiles[0].text);await new Promise(r=>setTimeout(r,0));const gutter=d.querySelector('.breakpointGutter');assert.equal(gutter.disabled,true);assert.match(gutter.ariaLabel,/Saved breakpoint/);gutter.dispatchEvent(new w.MouseEvent('contextmenu',{bubbles:true}));assert.equal(prompts,0);assert.equal(calls.some(c=>c.options.method==='POST'),false);
});

test('paused excerpts expand to the full file and polling preserves browsing position', async t => {
 const html=await readFile('packages/web/public/index.html','utf8');
 const js=await build({entryPoints:['packages/web/src/app.ts'],bundle:true,write:false,format:'iife',logLevel:'silent'});
 const dom=new JSDOM(html,{url:'http://127.0.0.1:1234',runScripts:'outside-only'});
 t.after(()=>dom.window.close());
 const w=dom.window,d=w.document;
 let tick,resolveSource,loads=0;
 const lines=Array.from({length:200},(_,i)=>'// line '+(i+1));
 let state={id:'test',generation:1,status:'paused',state:{Pid:1},project:'/demo',binary:'/demo/bin',frame:0,goroutine:1,frames:[],source:{file:'/demo/main.go',start:90,line:100,lines:lines.slice(89,110)}};
 w.setInterval=fn=>{tick=fn;return 1;};
 w.HTMLElement.prototype.getBoundingClientRect=function(){return {height:this.classList.contains('sourceNumber')?22:0,width:0};};
 w.fetch=async url=>{
  const path=new URL(url,w.location.href).pathname;
  if(path==='/api/sources'){loads++;return new Promise(resolve=>{resolveSource=()=>resolve({ok:true,json:async()=>({file:'/demo/main.go',start:1,line:0,lines})});});}
  return {ok:true,json:async()=>path==='/api/workspace'?{investigations:[]}:path==='/api/comments'?{threads:[]}:structuredClone(state)};
 };
 const flush=()=>new Promise(r=>setTimeout(r,0));
 w.eval(js.outputFiles[0].text);await flush();
 const pane=d.querySelector('#source');pane.scrollTop=44;pane.scrollLeft=30;
 resolveSource();await flush();
 assert.equal(d.querySelectorAll('.sourceCodeLine').length,200);
 assert.equal(pane.scrollTop,44+89*22,'loading earlier lines preserves the visible source position');
 assert.equal(pane.scrollLeft,30);
 pane.scrollTop=3000;
 state={...state,breakpoints:[{id:1,file:'/demo/main.go',line:120}]};tick();await flush();
 assert.equal(pane.scrollTop,3000);
 assert.equal(loads,1);
 d.querySelector('#followSource').click();
 assert.equal(d.querySelectorAll('.sourceCodeLine').length,200,'Current frame retains the complete file');
});

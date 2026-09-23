const {test}=require('node:test');
const assert=require('node:assert/strict');
const {build}=require('esbuild');
test('shared transport rejects foreign endpoints and preserves version, routing and errors',async()=>{
 const bundle=await build({entryPoints:['packages/client/src/index.ts'],bundle:true,write:false,platform:'node',format:'cjs'});
 const m={exports:{}};new Function('module','exports',bundle.outputFiles[0].text)(m,m.exports);const {createClient}=m.exports;
 assert.throws(()=>createClient({baseURL:'https://example.com'}),/local/);
 let call;const client=createClient({baseURL:'http://127.0.0.1:1234',session:'abc',fetch:async(url,options)=>{call={url,options};return {ok:true,json:async()=>({version:2})}}});
 await client.request('sessions/stop',{confirmed:true});assert.equal(new URL(call.url).searchParams.get('session'),'abc');assert.equal(call.options.redirect,'error');assert.equal(call.options.method,'POST');
 await assert.rejects(client.request('../escape'),/route/);
 await assert.rejects(createClient({baseURL:'http://127.0.0.1',fetch:async()=>({ok:true,json:async()=>({version:99})})}).request('state'),/version/);
});

test('authenticated event subscription sends bearer header, never credential URL or cookie',async()=>{
 const bundle=await build({entryPoints:['packages/client/src/index.ts'],bundle:true,write:false,platform:'node',format:'cjs'});
 const m={exports:{}};new Function('module','exports',bundle.outputFiles[0].text)(m,m.exports);
 let call,stop;let received;
 const ready=new Promise(resolve=>{received=resolve;});
 const client=m.exports.createClient({baseURL:'http://127.0.0.1:1234',token:'private-secret',fetch:async(url,options)=>{
  call={url,options};
  return new Response(new ReadableStream({start(controller){controller.enqueue(new TextEncoder().encode('data: {"id":4,"kind":"stopped"}\n\n'));}}),{status:200});
 }});
 stop=client.subscribe(3,event=>{assert.equal(event.id,4);stop();received();},()=>assert.fail('unexpected reset'));
 await ready;
 assert.equal(call.options.headers.Authorization,'Bearer private-secret');
 assert.equal(call.url.includes('private-secret'),false);
 assert.equal(new URL(call.url).searchParams.get('cursor'),'3');
 assert.equal(call.options.signal.aborted,true);
});

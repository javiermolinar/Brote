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

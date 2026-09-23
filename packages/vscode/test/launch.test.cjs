const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const {buildSync}=require('esbuild');
const code=buildSync({entryPoints:['packages/vscode/src/launch.ts'],bundle:false,platform:'node',format:'cjs',write:false}).outputFiles[0].text;
const moduleUnderTest={exports:{}};vm.runInNewContext(code,{module:moduleUnderTest,exports:moduleUnderTest.exports,require,process});
const {launchService}=moduleUnderTest.exports;
const folder={uri:{fsPath:'/workspace'}};
test('source, selected test and executable profiles use CLI creation with private temporary config',async()=>{
 for(const mode of ['debug','test','exec']){
  const calls=[];let file;
  const client={run:async args=>{calls.push(args);if(args[0]==='start'){file=args[args.indexOf('--launch-file')+1];const fs=require('node:fs');const config=JSON.parse(fs.readFileSync(file)).configurations[0];assert.equal(config.mode,mode);assert.deepEqual(config.args,['-test.run','^TestChosen$']);assert.equal(fs.statSync(file).mode&0o777,0o600);return{id:'new'};}},verify:async id=>assert.equal(id,'new')};
  assert.equal(await launchService(client,folder,{type:'brote',request:'launch',program:'/workspace',mode,args:['-test.run','^TestChosen$'],tracepoints:[{file:'main.go',line:4,values:{value:'value'}}]}),'new');
  assert.equal(calls[0].includes('--build'),mode!=='exec');assert.ok(calls[0].includes('--editor-start'));assert.equal(calls[1][0],'tracepoint');assert.equal(require('node:fs').existsSync(file),false);
 }
});
test('startup failures and cancellation clean up only newly created sessions',async()=>{
 for(const reason of ['verify','capture','cancel']){
  const calls=[];const token={isCancellationRequested:false};
  const client={run:async args=>{calls.push(args);if(args[0]==='start'){if(reason==='cancel')token.isCancellationRequested=true;return{id:'new'};}if(args[0]==='tracepoint')throw Error('capture rejected');},verify:async()=>{if(reason==='verify')throw Error('incompatible');}};
  await assert.rejects(launchService(client,folder,{program:'/workspace',tracepoints:[{file:'main.go',line:4}]},token));
  assert.deepEqual(Array.from(calls.at(-1)),['end-session','new','--confirmed']);
 }
 const client={run:async()=>{throw Error('build failed');}};
 await assert.rejects(launchService(client,folder,{program:'/workspace'}),/build failed/);
 await assert.rejects(launchService(client,folder,{program:'/workspace',remotePath:'/remote'}),/Unsupported/);
});

const {test}=require('node:test');
const assert=require('node:assert/strict');
const {mkdtemp,writeFile,rm,symlink,realpath}=require('node:fs/promises');
const {tmpdir}=require('node:os');
const {join}=require('node:path');
const {build}=require('esbuild');

test('runtime discovery uses bundled compatible core and rejects an explicit stale override',async t=>{
 const dir=await mkdtemp(join(tmpdir(),'agentdebugger-runtime-'));t.after(()=>rm(dir,{recursive:true,force:true}));
 const module=join(dir,'runtime.cjs');
 await build({entryPoints:['packages/client/src/runtime.ts'],bundle:true,platform:'node',format:'cjs',outfile:module,logLevel:'silent'});
 const {resolveRuntime}=require(module);
 const good=join(dir,'core-v1'),bad=join(dir,'old'),launcher=join(dir,'current');
 const binary=(value)=>'#!'+process.execPath+'\nconsole.log('+JSON.stringify(JSON.stringify(value))+');\n';
 await writeFile(good,binary({protocol:2,capabilities:['executionTasks','taskDelivery','taskExecute','embeddedWebUI']}),{mode:0o755});
 await writeFile(bad,binary({protocol:2}),{mode:0o755});
 await symlink(good,launcher);
 assert.equal(await resolveRuntime({bundled:[launcher],installed:[]}),await realpath(good),'pin the resolved executable rather than a mutable launcher');
 assert.equal(await resolveRuntime({bundled:[bad,good],installed:[]}),await realpath(good));
 const names=['BROTE_BIN','AGENTDEBUGGER_BIN','DELVE_LLM_ADAPTER_BIN'];
 const saved=Object.fromEntries(names.map(name=>[name,process.env[name]]));
 try {
  // New overrides take precedence, and existing installs retain their old alias.
  process.env.BROTE_BIN=good;process.env.AGENTDEBUGGER_BIN=bad;
  assert.equal(await resolveRuntime({bundled:[],installed:[]}),await realpath(good));
  process.env.BROTE_BIN=bad;process.env.AGENTDEBUGGER_BIN=good;
  await assert.rejects(resolveRuntime({bundled:[good],installed:[]}),/incompatible/);
  delete process.env.BROTE_BIN;
  assert.equal(await resolveRuntime({bundled:[],installed:[]}),await realpath(good));
  delete process.env.AGENTDEBUGGER_BIN;process.env.DELVE_LLM_ADAPTER_BIN=good;
  assert.equal(await resolveRuntime({bundled:[],installed:[]}),await realpath(good));
 } finally {
  for(const name of names) if(saved[name]===undefined)delete process.env[name];else process.env[name]=saved[name];
 }
 await assert.rejects(resolveRuntime({explicit:bad,bundled:[good],installed:[]}),/incompatible/);
 await assert.rejects(resolveRuntime({bundled:[],installed:[]}),/core unavailable/);
});

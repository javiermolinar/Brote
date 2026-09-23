const {test}=require('node:test');
const assert=require('node:assert/strict');
const {createServer}=require('node:https');
const {execFileSync,spawn}=require('node:child_process');
const fs=require('node:fs/promises');
const os=require('node:os');
const path=require('node:path');
const {buildSync}=require('esbuild');

test('OTLP verifies TLS and Cloud-style Basic auth without a collector',{timeout:30000},async()=>{
 const dir=await fs.mkdtemp(path.join(os.tmpdir(),'brote-tls-'));
 const key=path.join(dir,'key.pem'),cert=path.join(dir,'cert.pem');
 execFileSync('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',key,'-out',cert,'-days','1','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost'],{stdio:'ignore'});
 const calls=[];const server=createServer({key:await fs.readFile(key),cert:await fs.readFile(cert)},(req,res)=>{
  const chunks=[];req.on('data',c=>chunks.push(c));req.on('end',()=>{calls.push({auth:req.headers.authorization,bytes:Buffer.concat(chunks).length});res.writeHead(req.headers.authorization==='Basic aWQ6dG9rZW4='?200:401,{'Content-Type':'application/x-protobuf'});res.end();});
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const bundle=path.join(dir,'telemetry.cjs');buildSync({entryPoints:['packages/vscode/test/fixtures/telemetry.ts'],bundle:true,platform:'node',format:'cjs',outfile:bundle});
 async function probe(trust,auth,skipPreflight=false){const script=`const {preflight,createSessionTrace}=require(process.argv[1]);(async()=>{const c={url:process.argv[2],headers:{Authorization:process.argv[3]}};if(process.argv[4]!=='skip')await preflight(c);await createSessionTrace('tls','program','go',{url:'',headers:{},push:async()=>{}},c,(destination,success)=>{if(destination==='remote'&&!success)process.exitCode=1;}).close();})().catch(()=>{process.exitCode=1})`;
  const child=spawn(process.execPath,['-e',script,bundle,`https://localhost:${server.address().port}/v1/traces`,auth,skipPreflight?'skip':'preflight'],{env:{...process.env,NODE_EXTRA_CA_CERTS:trust?cert:''},stdio:'ignore'});return await new Promise(resolve=>child.once('exit',resolve));}
 try{
  assert.notEqual(await probe(false,'Basic aWQ6dG9rZW4='),0,'untrusted certificate rejected');
  assert.notEqual(await probe(false,'Basic aWQ6dG9rZW4=',true),0,'SDK transport also rejects untrusted certificates');
  assert.notEqual(await probe(true,'Basic wrong'),0,'invalid credentials rejected');
  assert.equal(await probe(true,'Basic aWQ6dG9rZW4='),0,'trusted authenticated export works');
  assert.ok(calls.some(r=>r.auth==='Basic aWQ6dG9rZW4='&&r.bytes>0),'spans sent after preflight');
 }finally{await new Promise(resolve=>server.close(resolve));await fs.rm(dir,{recursive:true,force:true});}
});

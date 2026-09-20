import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, request } from 'node:http';
import { spawn } from 'node:child_process';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createInterface } from 'node:readline';
import { once } from 'node:events';

for (const version of [1, 2]) test(`UI preview v${version} preserves API authorization and isolates origins`, async t => {
  const token = version === 1 ? 'local-test-token' : undefined;
  const upstream = createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ authorization: req.headers.authorization, origin: req.headers.origin, body, url: req.url }));
  });
  upstream.listen(0, '127.0.0.1');
  await once(upstream, 'listening');
  const broker = `http://127.0.0.1:${upstream.address().port}`;
  t.after(() => { upstream.close(); upstream.closeAllConnections(); });
  const dir = await mkdtemp(join(tmpdir(), 'debug-handover-ui-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const descriptor = join(dir, 'session.json');
  await writeFile(descriptor, JSON.stringify({ id: 'test', http: broker, token, version }));
  const child = spawn(process.execPath, [fileURLToPath(new URL('./preview-ui.mjs', import.meta.url)), descriptor]);
  t.after(() => child.kill());
  const lines = createInterface({ input: child.stdout });
  const [line] = await once(lines, 'line', { signal: AbortSignal.timeout(5000) });
  const origin = new URL(JSON.parse(line).panel).origin;
  const get = async (path, options) => {
    const response = await fetch(origin + path, options);
    return { status: response.status, body: await response.text() };
  };
  assert.equal((await get('/api/state')).status, token ? 401 : 200);
  assert.equal((await get('/api/state', { headers: { Authorization: 'Bearer wrong' } })).status, token ? 401 : 200);
  const headers = { ...(token ? {Authorization: 'Bearer ' + token} : {}), Origin: origin, 'Content-Type':'application/json' };
  assert.equal((await get('/api/state', { headers: { ...headers, Origin: 'http://untrusted.example' } })).status, 403);
  const read = await get('/api/state?frame=2', { headers });
  assert.equal(read.status, 200);
  assert.deepEqual(JSON.parse(read.body), { ...(token ? {authorization:'Bearer ' + token} : {}), origin: broker, body: '', url: '/api/state?frame=2' });
  const body = JSON.stringify({ action: 'next', generation: 12 });
  const write = await get('/api/action', { method: 'POST', headers, body });
  assert.equal(write.status, 200);
  assert.equal(JSON.parse(write.body).body, body);
  assert.equal((await get('/api/action', { method: 'POST', headers, body: 'x'.repeat(65537) })).status, 413);
  assert.equal((await get('/api/unknown', { headers })).status, 404);
  const page = await get('/');
  assert.equal(page.status, 200);
  if (token) assert.ok(!page.body.includes(token));
  assert.equal((await get('/app.ts')).status, 404);
  const badHost = await new Promise((resolve, reject) => {
    const req = request(origin + '/', { headers: { Host: 'untrusted.example' } }, response => { response.resume(); resolve(response.statusCode); });
    req.on('error', reject); req.end();
  });
  assert.equal(badHost, 403);
});

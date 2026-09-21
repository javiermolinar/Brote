// Serve current UI assets against an existing broker without restarting Delve.
import { createServer, request } from 'node:http';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { dirname, join } from 'node:path';
import { readFile } from 'node:fs/promises';
import { timingSafeEqual } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const run = promisify(execFile);
const descriptor = process.argv[2];
if (!descriptor) {
  console.error('Usage: npm run preview -- /absolute/path/to/session.json');
  process.exit(1);
}
const session = JSON.parse(await readFile(descriptor, 'utf8'));
const upstream = new URL(session.http);
if (upstream.protocol !== 'http:' || upstream.hostname !== '127.0.0.1' || (!session.token && session.version !== 2)) {
  throw new Error('Expected a local Brote session descriptor');
}
const expectedToken = session.token ? Buffer.from('Bearer ' + session.token) : null;
const assets = new Map([
  ['/', ['index.html', 'text/html']],
  ['/index.html', ['index.html', 'text/html']],
  ['/app.js', ['app.js', 'text/javascript']],
  ['/style.css', ['style.css', 'text/css']],
  ['/brote-plant.png', ['brote-plant.png', 'image/png']],
]);
let origin;

const server = createServer(async (req, res) => {
  const reject = (status, error) => {
    res.writeHead(status, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error }));
  };
  res.setHeader('Cache-Control', 'no-store');
  res.setHeader('X-Content-Type-Options', 'nosniff');
  res.setHeader('Referrer-Policy', 'no-referrer');
  res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'");
  if ('http://' + req.headers.host !== origin) return reject(403, 'unexpected host');
  if (req.headers.origin && req.headers.origin !== origin) return reject(403, 'foreign origin rejected');

  const url = new URL(req.url, origin);
  if (url.pathname.startsWith('/api/')) {
    const token = Buffer.from(req.headers.authorization || '');
    if (expectedToken && (token.length !== expectedToken.length || !timingSafeEqual(token, expectedToken))) {
      return reject(401, 'session token required');
    }
    if (req.method === 'POST' && url.pathname === '/api/sessions/stop') {
      if (!req.headers['content-type']?.startsWith('application/json')) return reject(415, 'JSON required');
      try {
        let data = ''; for await (const chunk of req) { data += chunk; if (data.length > 4096) return reject(413, 'request too large'); }
        const input = JSON.parse(data);
        if (!/^[a-f0-9]+$/.test(input.id) || input.confirmed !== true) return reject(400, 'session ID and confirmation required');
        const {stdout} = await run(process.env.DELVE_LLM_ADAPTER_BIN || 'delve-llm-adapter', ['end-session',input.id,'--confirmed'], {timeout:20000});
        res.end(stdout);
      } catch (error) { reject(409, 'Could not end session: ' + (error.stdout || error.message)); }
      return;
    }
    if (req.method === 'GET' && url.pathname === '/api/sessions') {
      try {
        const {stdout} = await run(process.env.DELVE_LLM_ADAPTER_BIN || 'delve-llm-adapter', ['sessions'], {timeout:15000});
        const sessions = JSON.parse(stdout).map(s => ({...s, panel: s.panel ? origin + '/?session=' + encodeURIComponent(s.id) : undefined}));
        res.end(JSON.stringify({sessions}));
      } catch { reject(502, 'Could not list sessions'); }
      return;
    }
    let destination = upstream, upstreamToken = session.token;
    const selected = url.searchParams.get('session');
    if (selected) {
      if (!/^[a-f0-9]+$/.test(selected)) return reject(400, 'invalid session');
      try {
        const other = JSON.parse(await readFile(join(dirname(dirname(descriptor)), selected, 'session.json'), 'utf8'));
        destination = new URL(other.http); upstreamToken = other.token;
        if (other.id !== selected || destination.protocol !== 'http:' || destination.hostname !== '127.0.0.1' || destination.username || destination.password) return reject(400, 'invalid session endpoint');
      } catch { return reject(404, 'session unavailable'); }
      url.searchParams.delete('session');
    }
    if (req.method === 'GET' && url.pathname === '/api/sources') {
      try {
        const args = ['sources', selected || session.id];
        if (url.searchParams.has('file')) args.push(url.searchParams.get('file'));
        const {stdout} = await run(process.env.DELVE_LLM_ADAPTER_BIN || 'delve-llm-adapter', args, {timeout:10000,maxBuffer:8*1024*1024});
        res.end(stdout);
      } catch (error) { reject(409, 'Source unavailable: ' + (error.stdout || error.message)); }
      return;
    }
    const eventStream=req.method==='GET' && url.pathname==='/api/events';
    if (!(req.method === 'GET' && url.pathname === '/api/state') &&
        !(eventStream || (['GET','POST'].includes(req.method) && url.pathname === '/api/comments')) &&
        !(req.method === 'POST' && url.pathname === '/api/action')) {
      return reject(404, 'unknown endpoint');
    }
    if (req.method === 'POST' && !req.headers['content-type']?.startsWith('application/json')) return reject(415, 'JSON required');
    try {
      const chunks = [];
      let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 65536) return reject(413, 'request too large');
        chunks.push(chunk);
      }
      const proxy = request(new URL(url.pathname + url.search, destination), {
        method: req.method,
        timeout: eventStream ? 0 : 15000,
        headers: {
          ...(upstreamToken ? { Authorization: 'Bearer ' + upstreamToken } : {}),
          'Content-Type': 'application/json',
          // Only our own validated origin is translated for the upstream broker.
          Origin: destination.origin,
        },
      }, response => {
        res.writeHead(response.statusCode, { 'Content-Type': response.headers['content-type'] || 'application/json' });
        response.pipe(res);
      });
      proxy.on('timeout', () => proxy.destroy(new Error('broker request timed out')));
      proxy.on('error', () => {
        if (!res.headersSent) reject(502, 'debugger broker is unavailable');
        else res.destroy();
      });
      req.on('error', () => proxy.destroy());
      res.on('close',()=>proxy.destroy());
      proxy.end(Buffer.concat(chunks));
    } catch {
      if (!res.headersSent) reject(400, 'invalid request');
    }
    return;
  }

  const asset = assets.get(url.pathname);
  if (!asset || req.method !== 'GET') return reject(404, 'not found');
  try {
    const data = await readFile(fileURLToPath(new URL('./public/' + asset[0], import.meta.url)));
    res.writeHead(200, { 'Content-Type': asset[1] });
    res.end(data);
  } catch {
    reject(500, 'UI asset unavailable; run npm run build');
  }
});
server.requestTimeout = 15000;
server.headersTimeout = 5000;
server.listen(Number(process.env.PORT || 0), '127.0.0.1', () => {
  origin = `http://127.0.0.1:${server.address().port}`;
  console.log(JSON.stringify({ panel: origin + '/' + (session.token ? '#' + session.token : ''), session: session.id, broker: upstream.origin }));
});
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => { server.close(); server.closeAllConnections(); });
}

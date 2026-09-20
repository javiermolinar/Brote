// Serve current UI assets against an existing broker without restarting Delve.
import { createServer, request } from 'node:http';
import { readFile } from 'node:fs/promises';
import { timingSafeEqual } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const descriptor = process.argv[2];
if (!descriptor) {
  console.error('Usage: npm run preview -- /absolute/path/to/session.json');
  process.exit(1);
}
const session = JSON.parse(await readFile(descriptor, 'utf8'));
const upstream = new URL(session.http);
if (upstream.protocol !== 'http:' || upstream.hostname !== '127.0.0.1' || !session.token) {
  throw new Error('Expected a local Debug Handover session descriptor');
}
const expectedToken = Buffer.from('Bearer ' + session.token);
const assets = new Map([
  ['/', ['index.html', 'text/html']],
  ['/index.html', ['index.html', 'text/html']],
  ['/app.js', ['app.js', 'text/javascript']],
  ['/style.css', ['style.css', 'text/css']],
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
    if (token.length !== expectedToken.length || !timingSafeEqual(token, expectedToken)) {
      return reject(401, 'session token required');
    }
    if (!(req.method === 'GET' && url.pathname === '/api/state') &&
        !(req.method === 'POST' && url.pathname === '/api/action')) {
      return reject(404, 'unknown endpoint');
    }
    try {
      const chunks = [];
      let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 65536) return reject(413, 'request too large');
        chunks.push(chunk);
      }
      const proxy = request(new URL(url.pathname + url.search, upstream), {
        method: req.method,
        timeout: 15000,
        headers: {
          Authorization: req.headers.authorization,
          'Content-Type': 'application/json',
          // Only our own validated origin is translated for the upstream broker.
          Origin: upstream.origin,
        },
      }, response => {
        res.writeHead(response.statusCode, { 'Content-Type': 'application/json' });
        response.pipe(res);
      });
      proxy.on('timeout', () => proxy.destroy(new Error('broker request timed out')));
      proxy.on('error', () => {
        if (!res.headersSent) reject(502, 'debugger broker is unavailable');
        else res.destroy();
      });
      req.on('error', () => proxy.destroy());
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
server.listen(0, '127.0.0.1', () => {
  origin = `http://127.0.0.1:${server.address().port}`;
  console.log(JSON.stringify({ panel: origin + '/#' + session.token, session: session.id, broker: upstream.origin }));
});
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => { server.close(); server.closeAllConnections(); });
}

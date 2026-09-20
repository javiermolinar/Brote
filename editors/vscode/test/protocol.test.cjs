const { test } = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { validateSession, pending, loopbackPort, request } = require('../dist/protocol.cjs');
const descriptor = { id: '0123456789', project: '/tmp/project', http: 'http://127.0.0.1:1234', token: 'a'.repeat(64) };
const state = { ...descriptor, owner: 'vscode', status: 'paused', state: {}, handoverId: 'b'.repeat(16), editorConnected: false };

test('only a fresh explicit handover can attach', () => {
  assert.equal(pending(descriptor, state), true);
  assert.equal(pending(descriptor, state, state.handoverId), false);
  for (const change of [{owner:'codex'}, {owner:'zed'}, {status:'running'}, {state:{NextInProgress:true}},
    {editorConnected:true}, {handoverId:''}, {id:'different'}, {project:'/another/project'}]) {
    assert.equal(pending(descriptor, {...state, ...change}), false, JSON.stringify(change));
  }
  assert.equal(pending({...descriptor, stopped:true}, state), false);
});

test('descriptors and DAP endpoints cannot send the token off machine', () => {
  assert.equal(validateSession(descriptor, descriptor.id), descriptor);
  for (const http of ['https://example.com', 'http://127.0.0.1:1234@evil.test', 'http://localhost:1234',
    'http://127.0.0.1:1234/path', 'http://127.0.0.1:65536', 'http://127.0.0.1:0']) {
    assert.throws(() => validateSession({...descriptor, http}, descriptor.id));
  }
  assert.throws(() => validateSession(descriptor, '../0123456789'));
  assert.throws(() => loopbackPort('remote.example:1234'));
  assert.equal(loopbackPort('127.0.0.1:57521'), 57521);
});

test('authenticated API surfaces errors and refuses redirects', async t => {
  let behavior = 'ok';
  const server = http.createServer((req, res) => {
    assert.equal(req.headers.authorization, `Bearer ${descriptor.token}`);
    if (behavior === 'redirect') { res.writeHead(302, {Location:'/elsewhere'}); res.end(); return; }
    res.writeHead(behavior === 'error' ? 409 : 200, {'Content-Type':'application/json'});
    res.end(JSON.stringify(behavior === 'error' ? {error:'session changed'} : state));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const s = {...descriptor, http:`http://127.0.0.1:${server.address().port}`};
  assert.equal((await request(s, '/api/state?brief=1')).owner, 'vscode');
  behavior = 'error';
  await assert.rejects(request(s, '/api/action', {}), /session changed/);
  behavior = 'redirect';
  await assert.rejects(request(s, '/api/state?brief=1'));
});


test('v2 descriptors omit tokens and reject unsupported protocol versions', () => {
  const v2 = {...descriptor, version:2, token:undefined};
  assert.equal(validateSession(v2,v2.id),v2);
  assert.throws(()=>validateSession({...v2,version:3},v2.id));
});

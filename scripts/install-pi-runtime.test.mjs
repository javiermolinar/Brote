import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {mkdtemp, mkdir, writeFile, readFile, realpath, rm, access} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {installPiRuntime, runtimeAsset} from './install-pi-runtime.mjs';

const execute = promisify(execFile);
const capabilities = ['executionTasks', 'taskDelivery', 'taskExecute', 'embeddedWebUI'];
const binary = version => Buffer.from(`#!/bin/sh\necho '${JSON.stringify({version, protocol:2, capabilities})}'\n`);
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'brote pi runtime '));
  t.after(() => rm(root, {recursive:true, force:true}));
  await writeFile(join(root, 'package.json'), JSON.stringify({version:'0.4.0'}));
  const requests = [];
  const fetcher = async url => {
    requests.push(String(url));
    const version = /\/v([^/]+)\//.exec(String(url))[1];
    const body = binary(version);
    return new Response(String(url).endsWith('/SHA256SUMS')
      ? `${sha256(body)}  ${runtimeAsset(version).name}\n` : body);
  };
  return {root, cacheRoot:join(root, 'cache'), requests, fetcher};
}

test('Git install verifies exact version, caches outside the package runtime and relinks offline', async t => {
  const f = await fixture(t);
  const installed = await installPiRuntime(f);
  assert.equal(JSON.parse((await execute(installed, ['version'])).stdout).version, '0.4.0');
  assert.ok(f.requests.every(url => url.startsWith('https://github.com/javiermolinar/Brote/releases/download/v0.4.0/')));
  const link = join(f.root, 'adapters/pi/runtime', runtimeAsset('0.4.0').target, 'brote');
  assert.equal(await realpath(link), await realpath(installed));
  await rm(join(f.root, 'adapters/pi/runtime'), {recursive:true});
  await installPiRuntime({...f, fetcher:() => {throw Error('offline');}});
  assert.equal(await realpath(link), await realpath(installed));
});

test('updates retain the old executable and failed updates never replace it', async t => {
  const f = await fixture(t), old = await installPiRuntime(f);
  await writeFile(join(f.root, 'package.json'), JSON.stringify({version:'0.4.1'}));
  await assert.rejects(installPiRuntime({...f, fetcher:async () => new Response('', {status:404})}), /matching GitHub release may not be published/);
  const link = join(f.root, 'adapters/pi/runtime', runtimeAsset('0.4.0').target, 'brote');
  assert.equal(await realpath(link), await realpath(old));
  const next = await installPiRuntime(f);
  assert.notEqual(next, old);
  assert.equal(JSON.parse((await execute(old, ['version'])).stdout).version, '0.4.0');
  assert.equal(JSON.parse((await execute(next, ['version'])).stdout).version, '0.4.1');
});

test('checksum mismatch is rejected before the downloaded executable runs', async t => {
  const f = await fixture(t), marker = join(f.root, 'executed');
  const payload = Buffer.from(`#!/bin/sh\ntouch '${marker}'\n`);
  await assert.rejects(installPiRuntime({...f, fetcher:async url => new Response(String(url).endsWith('/SHA256SUMS')
    ? `${'0'.repeat(64)}  ${runtimeAsset('0.4.0').name}\n` : payload)}), /checksum mismatch/);
  await assert.rejects(access(marker));
});

test('a checksum-valid core with the wrong version never becomes active', async t => {
  const f = await fixture(t), body = binary('0.3.0');
  await assert.rejects(installPiRuntime({...f, fetcher:async url => new Response(String(url).endsWith('/SHA256SUMS')
    ? `${sha256(body)}  ${runtimeAsset('0.4.0').name}\n` : body)}), /incompatible/);
  await assert.rejects(access(join(f.cacheRoot, '0.4.0', runtimeAsset('0.4.0').target, 'brote')));
});

test('ambiguous checksums and insecure mirrors fail before installing', async t => {
  const f = await fixture(t), line = `${sha256(binary('0.4.0'))}  ${runtimeAsset('0.4.0').name}\n`;
  await assert.rejects(installPiRuntime({...f, fetcher:async () => new Response(line + line)}), /ambiguous/);
  await assert.rejects(installPiRuntime({...f, baseURL:'http://example.com/releases/'}), /must use HTTPS/);
});

test('damaged cached bytes are replaced with verified release bytes', async t => {
  const f = await fixture(t), installed = await installPiRuntime(f);
  await writeFile(installed, 'broken');
  await installPiRuntime(f);
  assert.deepEqual(await readFile(installed), binary('0.4.0'));
  assert.equal(f.requests.length, 4);
});

test('asset names match the four release platforms and reject unsupported hosts', () => {
  for (const platform of ['darwin', 'linux']) for (const arch of ['arm64', 'x64']) {
    assert.equal(runtimeAsset('0.4.0', platform, arch).name, `brote-v0.4.0-${platform}-${arch === 'x64' ? 'amd64' : arch}`);
  }
  assert.throws(() => runtimeAsset('0.4.0', 'win32', 'x64'), /supports macOS and Linux/);
  assert.throws(() => runtimeAsset('../latest'), /Invalid/);
});

test('development install skips downloads; production hook failures propagate', async t => {
  const f = await fixture(t), scripts = join(f.root, 'scripts');
  await mkdir(scripts);
  const script = join(scripts, 'install-pi-runtime.mjs');
  await writeFile(script, await readFile(new URL('./install-pi-runtime.mjs', import.meta.url)));
  const env = {...process.env, npm_config_omit:'', NODE_ENV:'development', BROTE_RELEASE_BASE_URL:'http://example.com/', BROTE_RUNTIME_CACHE:f.cacheRoot};
  await execute(process.execPath, [script, '--if-production'], {env});
  await assert.rejects(execute(process.execPath, [script, '--if-production'], {env:{...env, npm_config_omit:'dev'}}), /must use HTTPS/);
  await assert.rejects(execute(process.execPath, [script], {env}), /must use HTTPS/);
});

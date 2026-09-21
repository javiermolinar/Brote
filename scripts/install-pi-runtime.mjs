// Pi's Git installer runs npm install --omit=dev. Keep its core outside the
// checkout: Pi cleans that directory on update, including untracked files.
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {readFile, writeFile, mkdir, mkdtemp, chmod, rename, symlink, rm, realpath} from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import {fileURLToPath} from 'node:url';

const execute = promisify(execFile);
const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const releaseRoot = 'https://github.com/javiermolinar/Brote/releases/download/';
const capabilities = ['executionTasks', 'taskDelivery', 'taskExecute', 'embeddedWebUI'];
const digest = data => createHash('sha256').update(data).digest('hex');

export function runtimeAsset(version, platform = process.platform, arch = process.arch) {
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) throw new Error('Invalid Brote package version');
  if (!['darwin', 'linux'].includes(platform) || !['arm64', 'x64'].includes(arch)) {
    throw new Error(`Brote supports macOS and Linux on arm64/x64; this host is ${platform}/${arch}`);
  }
  return {target: `${platform}-${arch}`, name: `brote-v${version}-${platform}-${arch === 'x64' ? 'amd64' : arch}`};
}

async function validate(binary, version) {
  const result = JSON.parse((await execute(binary, ['version'], {timeout: 5000, maxBuffer: 65536})).stdout);
  if (result.version !== version || result.protocol !== 2 || !capabilities.every(c => result.capabilities?.includes(c))) {
    throw new Error(`Downloaded core is incompatible with Brote ${version}`);
  }
}

async function download(url, fetcher, maxBytes) {
  const response = await fetcher(url, {signal: AbortSignal.timeout(60000)});
  if (!response.ok) {
    const hint = response.status === 404 ? ' The matching GitHub release may not be published yet.' : '';
    throw new Error(`Cannot download ${url}: HTTP ${response.status}.${hint}`);
  }
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > maxBytes) throw new Error(`Download exceeds the size limit: ${url}`);
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

export async function installPiRuntime({root = projectRoot,
  cacheRoot = process.env.BROTE_RUNTIME_CACHE || path.join(process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'), 'brote', 'runtimes'),
  baseURL = process.env.BROTE_RELEASE_BASE_URL || releaseRoot,
  platform = process.platform, arch = process.arch, fetcher = fetch} = {}) {
  const {version} = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8'));
  const {target, name} = runtimeAsset(version, platform, arch);
  const base = new URL(baseURL.endsWith('/') ? baseURL : baseURL + '/');
  // A loopback mirror supports local release validation. Public downloads use HTTPS.
  if (base.protocol !== 'https:' && !(base.protocol === 'http:' && ['127.0.0.1', '[::1]', 'localhost'].includes(base.hostname))) {
    throw new Error('Brote release URL must use HTTPS');
  }
  const source = new URL(`v${version}/${name}`, base).href;
  const cache = path.resolve(cacheRoot, version, target);
  const binary = path.join(cache, 'brote');
  const receipt = path.join(cache, 'receipt.json');
  let cached = false;
  try {
    const saved = JSON.parse(await readFile(receipt, 'utf8'));
    cached = saved.source === source && saved.sha256 === digest(await readFile(binary));
    if (cached) await validate(binary, version);
  } catch { cached = false; }
  if (!cached) {
    const sums = (await download(new URL(`v${version}/SHA256SUMS`, base), fetcher, 1024 * 1024)).toString('utf8');
    const matches = sums.split(/\r?\n/).map(line => /^([a-fA-F0-9]{64})  (.+)$/.exec(line)).filter(m => m?.[2] === name);
    if (matches.length !== 1) throw new Error(`Release checksum missing or ambiguous for ${name}`);
    const bytes = await download(source, fetcher, 128 * 1024 * 1024);
    const sha256 = digest(bytes);
    if (sha256 !== matches[0][1].toLowerCase()) throw new Error(`Release checksum mismatch for ${name}`);
    await mkdir(cache, {recursive: true});
    const staging = await mkdtemp(path.join(cache, '.install-'));
    try {
      const candidate = path.join(staging, 'brote');
      await writeFile(candidate, bytes);
      await chmod(candidate, 0o755);
      await validate(candidate, version);
      await writeFile(path.join(staging, 'receipt.json'), JSON.stringify({source, sha256}) + '\n');
      await rename(candidate, binary);
      await rename(path.join(staging, 'receipt.json'), receipt);
    } finally { await rm(staging, {recursive: true, force: true}); }
  }
  // resolveRuntime realpaths this link, so running brokers retain the versioned
  // cache path even if a later pi update removes the whole package checkout.
  const directory = path.join(root, 'adapters', 'pi', 'runtime', target);
  await mkdir(directory, {recursive: true});
  const link = path.join(directory, 'brote');
  const staging = await mkdtemp(path.join(directory, '.link-'));
  try {
    await symlink(binary, path.join(staging, 'brote'));
    await rename(path.join(staging, 'brote'), link);
  } finally { await rm(staging, {recursive: true, force: true}); }
  return binary;
}

if (process.argv[1] && await realpath(process.argv[1]).catch(() => '') === fileURLToPath(import.meta.url)) {
  const production = process.env.npm_config_omit?.split(/[\s,]+/).includes('dev') || process.env.NODE_ENV === 'production';
  if (!process.argv.includes('--if-production') || production) {
    try {
      const binary = await installPiRuntime();
      console.log(`Brote core and browser inspector ready: ${binary}`);
    } catch (error) {
      console.error(`Brote installation failed: ${error.message}`);
      process.exitCode = 1;
    }
  }
}

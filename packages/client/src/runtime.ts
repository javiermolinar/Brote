// Node-only runtime discovery, shared by Pi and the desktop extension.
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {access, realpath} from 'node:fs/promises';
import {constants} from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

const run = promisify(execFile);
export interface RuntimeOptions { explicit?:string; bundled?:string[]; installed?:string[] }
export async function resolveRuntime(options:RuntimeOptions = {}):Promise<string> {
  const explicit = options.explicit || process.env.BROTE_BIN || process.env.AGENTDEBUGGER_BIN || process.env.DELVE_LLM_ADAPTER_BIN;
  const candidates = explicit ? [explicit] : [
    ...(options.bundled || []),
    ...(options.installed || [path.join(os.homedir(),'.local','bin','brote'),path.join(os.homedir(),'.local','bin','agentdebugger'),path.join(os.homedir(),'.local','bin','delve-llm-adapter')]),
  ];
  const failures:string[]=[];
  for (const candidate of candidates) {
    try {
      if (!path.isAbsolute(candidate)) throw new Error('runtime path must be absolute');
      await access(candidate,constants.X_OK);
      const executable = await realpath(candidate);
      const info = JSON.parse((await run(executable,['version'],{timeout:5000,maxBuffer:65536})).stdout);
      if (info.protocol !== 2 || !['executionTasks','taskDelivery','taskExecute','embeddedWebUI'].every(c=>info.capabilities?.includes(c))) throw new Error('incompatible Brote core; update this integration or configured executable');
      return executable;
    } catch(error) { failures.push(`${candidate}: ${error instanceof Error ? error.message : error}`); }
  }
  throw new Error(`Brote core unavailable on ${process.platform}/${process.arch}. Install the matching release or set BROTE_BIN.\n${failures.join('\n')}`);
}

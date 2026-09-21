// Git packages load TypeScript directly, just like other native Pi extensions.
import type {ExtensionAPI} from '@earendil-works/pi-coding-agent';
import extension from './index.js';
import {resolveRuntime} from '../../packages/client/src/runtime.js';
import {installPiRuntime} from '../../scripts/install-pi-runtime.mjs';

export default function (pi: ExtensionAPI) {
  extension(pi, async () => {
    // Explicit overrides remain authoritative, including visible incompatibility errors.
    const explicit = process.env.BROTE_BIN || process.env.AGENTDEBUGGER_BIN || process.env.DELVE_LLM_ADAPTER_BIN;
    if (explicit) return resolveRuntime({explicit});
    const binary = await installPiRuntime();
    return resolveRuntime({bundled:[binary], installed:[]});
  });
}

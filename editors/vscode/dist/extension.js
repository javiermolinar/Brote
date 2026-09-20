"use strict";
var __create = Object.create;
var __defProp = Object.defineProperty;
var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
var __getOwnPropNames = Object.getOwnPropertyNames;
var __getProtoOf = Object.getPrototypeOf;
var __hasOwnProp = Object.prototype.hasOwnProperty;
var __export = (target, all) => {
  for (var name in all)
    __defProp(target, name, { get: all[name], enumerable: true });
};
var __copyProps = (to, from, except, desc) => {
  if (from && typeof from === "object" || typeof from === "function") {
    for (let key of __getOwnPropNames(from))
      if (!__hasOwnProp.call(to, key) && key !== except)
        __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
  }
  return to;
};
var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(
  // If the importer is in node compatibility mode or this is not an ESM
  // file that has been converted to a CommonJS file using a Babel-
  // compatible transform (i.e. "__esModule" has not been set), then set
  // "default" to the CommonJS "module.exports" for node compatibility.
  isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", { value: mod, enumerable: true }) : target,
  mod
));
var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);

// editors/vscode/src/extension.ts
var extension_exports = {};
__export(extension_exports, {
  activate: () => activate
});
module.exports = __toCommonJS(extension_exports);
var vscode = __toESM(require("vscode"));
var fs = __toESM(require("node:fs/promises"));
var path2 = __toESM(require("node:path"));

// editors/vscode/src/protocol.ts
var path = __toESM(require("node:path"));
var os = __toESM(require("node:os"));
function sessionDirectory() {
  if (process.env.DEBUG_HANDOVER_HOME) return process.env.DEBUG_HANDOVER_HOME;
  const cache = process.platform === "darwin" ? path.join(os.homedir(), "Library", "Caches") : process.platform === "win32" ? process.env.LocalAppData || path.join(os.homedir(), "AppData", "Local") : process.env.XDG_CACHE_HOME || path.join(os.homedir(), ".cache");
  return path.join(cache, "debug-handover", "sessions");
}
function validID(id) {
  return /^[a-f0-9]{10}$/.test(id);
}
function loopbackPort(endpoint) {
  const match = /^127\.0\.0\.1:(\d+)$/.exec(endpoint);
  const port = match ? Number(match[1]) : 0;
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("Expected a local debugger endpoint");
  return port;
}
function validateSession(value, id) {
  const s = value;
  if (!s || !validID(id) || s.id !== id || typeof s.project !== "string" || !path.isAbsolute(s.project) || !/^http:\/\/127\.0\.0\.1:\d+$/.test(s.http) || !/^[a-f0-9]{64}$/.test(s.token)) {
    throw new Error("Invalid local session descriptor");
  }
  loopbackPort(s.http.slice(7));
  return s;
}
function pending(s, v, attempted) {
  return !s.stopped && v.id === s.id && v.project === s.project && v.owner === "vscode" && v.status === "paused" && !v.state.NextInProgress && !v.editorConnected && /^[a-f0-9]{16}$/.test(v.handoverId) && v.handoverId !== attempted;
}
async function request(s, route, body) {
  validateSession(s, s.id);
  const response = await fetch(s.http + route, {
    method: body === void 0 ? "GET" : "POST",
    headers: { Authorization: `Bearer ${s.token}`, "Content-Type": "application/json" },
    body: body === void 0 ? void 0 : JSON.stringify(body),
    redirect: "error",
    signal: AbortSignal.timeout(8e3)
  });
  const value = await response.json();
  if (!response.ok) throw new Error(value.error || `Broker returned ${response.status}`);
  return value;
}

// editors/vscode/src/extension.ts
function activate(context) {
  const log = vscode.window.createOutputChannel("Debug Handover");
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 20);
  status.command = "debugHandover.reclaim";
  const descriptors = /* @__PURE__ */ new Map();
  const states = /* @__PURE__ */ new Map();
  const attempts = /* @__PURE__ */ new Map();
  const live = /* @__PURE__ */ new Map();
  let busy = false;
  let disposed = false;
  function message(error) {
    return error instanceof Error ? error.message : String(error);
  }
  async function folderFor(s) {
    if (!vscode.workspace.isTrusted) return void 0;
    const project = await fs.realpath(s.project);
    for (const folder of vscode.workspace.workspaceFolders || []) {
      if (folder.uri.scheme === "file" && await fs.realpath(folder.uri.fsPath) === project) return folder;
    }
    return void 0;
  }
  function updateStatus() {
    const owned = [...states.values()].filter((s) => s.owner === "vscode" && s.status !== "exited");
    if (!owned.length) {
      status.hide();
      return;
    }
    status.text = "$(debug-disconnect) Give control to Codex";
    status.tooltip = "Return the paused Go process to Codex. Pause in the debugger first.";
    status.show();
  }
  async function attach(s, state, folder) {
    const key = `attempt.${s.id}`;
    attempts.set(s.id, state.handoverId);
    await context.workspaceState.update(key, state.handoverId);
    try {
      const started = await vscode.debug.startDebugging(folder, {
        type: "debug-handover",
        name: `Debug Handover \xB7 ${s.id}`,
        request: "attach",
        handoverSession: s.id,
        handoverId: state.handoverId,
        mode: "remote",
        stopOnEntry: true,
        showGlobalVariables: false,
        suppressMultipleSessionWarning: true
      });
      if (!started) throw new Error("VS Code could not start the attach session. Run Attach Pending Session to retry.");
      log.appendLine(`Attached to session ${s.id}`);
    } catch (error) {
      log.appendLine(`Attach ${s.id}: ${message(error)}`);
      void vscode.window.showErrorMessage(`Debug Handover: ${message(error)}`);
      try {
        const fresh = await request(s, "/api/state?brief=1");
        await request(s, "/api/action", {
          action: "editor-error",
          generation: fresh.generation,
          handoverId: state.handoverId,
          error: message(error)
        });
      } catch {
      }
    }
  }
  async function scan(retry = false) {
    if (busy || disposed || !vscode.workspace.isTrusted) return;
    busy = true;
    try {
      const root = vscode.workspace.getConfiguration("debugHandover").get("sessionDirectory") || sessionDirectory();
      const entries = await fs.readdir(root).catch(() => []);
      const present = /* @__PURE__ */ new Set();
      for (const id of entries.filter(validID)) {
        if (disposed) return;
        try {
          const s = validateSession(JSON.parse(await fs.readFile(path2.join(root, id, "session.json"), "utf8")), id);
          if (s.stopped) continue;
          const folder = await folderFor(s);
          if (!folder) continue;
          const state = await request(s, "/api/state?brief=1");
          present.add(id);
          descriptors.set(id, s);
          states.set(id, state);
          const attempted = retry ? void 0 : attempts.get(id) || context.workspaceState.get(`attempt.${id}`);
          if (!live.has(id) && pending(s, state, attempted)) await attach(s, state, folder);
        } catch {
        }
      }
      for (const id of states.keys()) if (!present.has(id)) {
        states.delete(id);
        descriptors.delete(id);
      }
      updateStatus();
    } finally {
      busy = false;
    }
  }
  async function selected() {
    const active = vscode.debug.activeDebugSession?.configuration.handoverSession;
    if (active && descriptors.has(active)) return descriptors.get(active);
    const candidates = [...descriptors.values()].filter((s) => states.get(s.id)?.owner === "vscode");
    if (candidates.length === 1) return candidates[0];
    if (!candidates.length) {
      void vscode.window.showInformationMessage("No VS Code handover session is active for this project.");
      return;
    }
    const choice = await vscode.window.showQuickPick(candidates.map((s) => ({ label: s.id, description: s.project, session: s })));
    return choice?.session;
  }
  context.subscriptions.push(
    log,
    status,
    vscode.debug.registerDebugAdapterDescriptorFactory("debug-handover", {
      async createDebugAdapterDescriptor(session) {
        const id = session.configuration.handoverSession;
        const s = descriptors.get(id);
        if (!s || !await folderFor(s)) throw new Error("Session does not belong to this trusted project.");
        const state = await request(s, "/api/state?brief=1");
        if (!pending(s, state) || state.handoverId !== session.configuration.handoverId) {
          throw new Error("Handover changed or another editor is attached. Request a fresh handover.");
        }
        return new vscode.DebugAdapterServer(loopbackPort(state.dap), "127.0.0.1");
      }
    }),
    vscode.debug.onDidStartDebugSession((session) => {
      if (session.type === "debug-handover") live.set(session.configuration.handoverSession, session);
    }),
    vscode.debug.onDidTerminateDebugSession((session) => {
      const id = session.configuration.handoverSession;
      if (live.get(id)?.id === session.id) live.delete(id);
      void scan();
    }),
    vscode.commands.registerCommand("debugHandover.attach", () => scan(true)),
    vscode.commands.registerCommand("debugHandover.reclaim", async () => {
      try {
        const s = await selected();
        if (!s) return;
        const state = await request(s, "/api/state?brief=1");
        if (state.owner !== "vscode") throw new Error("VS Code no longer owns this session.");
        const result = await request(s, "/api/action", {
          action: "reclaim",
          generation: state.generation,
          notify: Boolean(state.thread)
        });
        if (result.notificationError) throw new Error(`Control returned, but notification failed: ${result.notificationError}`);
        void vscode.window.showInformationMessage(state.thread ? "Control returned to Codex; task notification requested." : "Control returned. No Codex task is bound.");
        await scan();
      } catch (error) {
        void vscode.window.showErrorMessage(`Debug Handover: ${message(error)}`);
      }
    }),
    vscode.commands.registerCommand("debugHandover.inspector", async () => {
      const s = await selected();
      if (s) await vscode.env.openExternal(vscode.Uri.parse(`${s.http}/#${s.token}`));
    }),
    vscode.workspace.onDidChangeWorkspaceFolders(() => void scan()),
    vscode.workspace.onDidGrantWorkspaceTrust(() => void scan())
  );
  const timer = setInterval(() => void scan(), 1e3);
  context.subscriptions.push({ dispose() {
    disposed = true;
    clearInterval(timer);
  } });
  void scan();
}
// Annotate the CommonJS export names for ESM import in node:
0 && (module.exports = {
  activate
});

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

// packages/vscode/src/protocol.ts
var protocol_exports = {};
__export(protocol_exports, {
  loopbackPort: () => loopbackPort,
  pending: () => pending,
  request: () => request,
  sessionDirectory: () => sessionDirectory,
  validID: () => validID,
  validateSession: () => validateSession
});
module.exports = __toCommonJS(protocol_exports);

// packages/client/src/index.ts
var PROTOCOL_VERSION = 2;
function createClient(options) {
  const base = new URL(options.baseURL);
  if (base.protocol !== "http:" || base.hostname !== "127.0.0.1" || base.username || base.password || base.pathname !== "/" || base.search || base.hash) throw new Error("Expected a local broker URL");
  const url = (route) => {
    if (!/^[a-z][a-z-]*(?:\/[a-z][a-z-]*)*(?:\?.*)?$/.test(route) || route.includes("#")) throw new Error("Invalid API route");
    const target = new URL("/api/" + route, base);
    if (options.session) target.searchParams.set("session", options.session);
    return target;
  };
  async function request2(route, body, signal) {
    const response = await (options.fetch || fetch)(url(route).href, { method: body === void 0 ? "GET" : "POST", headers: { "Content-Type": "application/json", ...options.token ? { Authorization: "Bearer " + options.token } : {} }, body: body === void 0 ? void 0 : JSON.stringify(body), redirect: "error", signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(options.timeoutMs || 8e3)]) : AbortSignal.timeout(options.timeoutMs || 8e3) });
    const value = await response.json();
    if (!response.ok) throw new Error(value.error || `Broker returned ${response.status}`);
    if (value.version !== void 0 && value.version > PROTOCOL_VERSION) throw new Error("Unsupported broker protocol version");
    return value;
  }
  function subscribe(cursor, onEvent, onReset, onOpen) {
    if (options.token) throw new Error("Live events require protocol v2");
    const target = url("events");
    target.searchParams.set("cursor", String(cursor));
    const stream = new EventSource(target.href);
    stream.onopen = () => onOpen?.();
    stream.onmessage = (e) => {
      let event;
      try {
        event = JSON.parse(e.data);
      } catch {
        return;
      }
      onEvent(event);
    };
    stream.addEventListener("reset", () => {
      stream.close();
      onReset();
    });
    return () => stream.close();
  }
  return { request: request2, subscribe };
}

// packages/vscode/src/protocol.ts
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
  if (!s || s.version !== void 0 && s.version > 2 || !validID(id) || s.id !== id || typeof s.project !== "string" || !path.isAbsolute(s.project) || !/^http:\/\/127\.0\.0\.1:\d+$/.test(s.http) || s.version !== 2 && !/^[a-f0-9]{64}$/.test(s.token || "")) {
    throw new Error("Invalid local session descriptor");
  }
  loopbackPort(s.http.slice(7));
  return s;
}
function pending(s, v, attempted) {
  return !s.stopped && v.id === s.id && v.project === s.project && v.owner === "vscode" && v.status === "paused" && !v.state.NextInProgress && !v.editorConnected && /^[a-f0-9]{16}$/.test(v.handoverId) && v.handoverId !== attempted;
}
async function request(s, route, body, signal) {
  validateSession(s, s.id);
  return createClient({ baseURL: s.http, token: s.token }).request(route.slice(5), body, signal);
}
// Annotate the CommonJS export names for ESM import in node:
0 && (module.exports = {
  loopbackPort,
  pending,
  request,
  sessionDirectory,
  validID,
  validateSession
});

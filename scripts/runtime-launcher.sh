#!/bin/sh
# Release-only launcher: the target and the helper are never compiled on install.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
case "$(uname -s)" in Darwin) platform=darwin;; Linux) platform=linux;; *) echo 'Shared Brote sessions support macOS and Linux.' >&2; exit 1;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=x64;; *) echo 'Unsupported processor architecture.' >&2; exit 1;; esac
binary=${BROTE_BIN:-${AGENTDEBUGGER_BIN:-${DELVE_LLM_ADAPTER_BIN:-"$root/runtime/$platform-$arch/brote"}}}
[ -x "$binary" ] || { echo 'Brote runtime missing; reinstall the matching release package.' >&2; exit 1; }
exec "$binary" "$@"

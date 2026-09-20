#!/bin/sh
# Download a verified release and delegate all installation to its setup command.
set -eu
repository=${DELVE_LLM_ADAPTER_REPOSITORY:-@REPOSITORY@}
version=latest
agent=
editor=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --repository|--version|--agent|--editor)
      [ "$#" -ge 2 ] || { echo "Missing value for $1" >&2; exit 1; }
      case "$1" in
        --repository) repository=$2;;
        --version) version=$2;;
        --agent) agent=$2;;
        --editor) editor=$2;;
      esac
      shift 2;;
    *) echo "Unknown argument: $1" >&2; exit 1;;
  esac
done
case "$repository" in ''|@REPOSITORY@|*[!A-Za-z0-9_./-]*) echo 'Supply --repository OWNER/REPO (release installers embed this value).' >&2; exit 1;; esac
case "$version" in ''|*[!A-Za-z0-9._+-]*) echo 'Invalid version' >&2; exit 1;; esac
case "$agent" in ''|codex|pi) ;; *) echo 'Agent must be codex or pi' >&2; exit 1;; esac
case "$editor" in ''|vscode) ;; *) echo 'Editor must be vscode' >&2; exit 1;; esac
case "$(uname -s)" in Darwin) os=darwin;; Linux) os=linux;; *) echo 'Only macOS and Linux are supported' >&2; exit 1;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=amd64;; *) echo 'Unsupported architecture' >&2; exit 1;; esac
command -v curl >/dev/null || { echo 'curl is required' >&2; exit 1; }
command -v tar >/dev/null || { echo 'tar is required' >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
if [ "$version" = latest ]; then
  base="https://github.com/$repository/releases/latest/download"
else
  base="https://github.com/$repository/releases/download/$version"
fi
curl --proto '=https' --tlsv1.2 -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"
# The checksum manifest identifies the version, avoiding a separate API request.
asset=$(awk -v suffix="-$os-$arch.tar.gz" '$2 ~ /^delve-llm-adapter-/ && substr($2,length($2)-length(suffix)+1)==suffix {print $2}' "$tmp/SHA256SUMS")
case "$asset" in ''|*/*|*..*|*[!A-Za-z0-9._+-]*) echo 'Missing or invalid release asset' >&2; exit 1;; esac
curl --proto '=https' --tlsv1.2 -fsSL "$base/$asset" -o "$tmp/$asset"
expected=$(awk -v name="$asset" '$2==name {print $1}' "$tmp/SHA256SUMS")
if command -v sha256sum >/dev/null; then actual=$(sha256sum "$tmp/$asset" | awk '{print $1}'); else actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}'); fi
[ "$actual" = "$expected" ] || { echo 'Release checksum mismatch' >&2; exit 1; }
tar -xzf "$tmp/$asset" -C "$tmp"
set -- setup --bundle "$tmp/delve-llm-adapter"
[ -z "$agent" ] || set -- "$@" --agent "$agent"
[ -z "$editor" ] || set -- "$@" --editor "$editor"
"$tmp/delve-llm-adapter/bin/delve-llm-adapter" "$@"

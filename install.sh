#!/bin/sh
# Verify a downloaded or local release and delegate installation to setup.
set -eu
repository=${BROTE_REPOSITORY:-${DELVE_LLM_ADAPTER_REPOSITORY:-@REPOSITORY@}}
version=latest
agent=
editor=
release_dir=
usage() {
  cat <<'HELP'
Install Brote and its browser inspector, with an optional agent or editor.

Usage: sh install.sh [--agent codex|pi] [--editor vscode] [options]

  --from DIR               Install from a local release directory with SHA256SUMS
  --repository OWNER/REPO   GitHub repository (embedded in release installers)
  --version TAG            Download a particular release; default: latest
  --help                   Show this help

Examples:
  sh install.sh --from dist/releases --agent pi
  sh install.sh --from dist/releases --agent codex --editor vscode

Without --from, downloads the matching macOS/Linux release and verifies its
checksum. The selected host command (codex, pi, or code) must be installed.
HELP
}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --help|-h) usage; exit 0;;
    --repository|--version|--agent|--editor|--from)
      [ "$#" -ge 2 ] || { echo "Missing value for $1" >&2; exit 1; }
      [ -n "$2" ] || { echo "Missing value for $1" >&2; exit 1; }
      case "$1" in
        --repository) repository=$2;;
        --version) version=$2;;
        --agent) agent=$2;;
        --editor) editor=$2;;
        --from) release_dir=$2;;
      esac
      shift 2;;
    *) echo "Unknown argument: $1" >&2; exit 1;;
  esac
done
case "$version" in ''|*[!A-Za-z0-9._+-]*) echo 'Invalid version' >&2; exit 1;; esac
case "$agent" in ''|codex|pi) ;; *) echo 'Agent must be codex or pi' >&2; exit 1;; esac
case "$editor" in ''|vscode) ;; *) echo 'Editor must be vscode' >&2; exit 1;; esac
case "$(uname -s)" in Darwin) os=darwin;; Linux) os=linux;; *) echo 'Only macOS and Linux are supported' >&2; exit 1;; esac
case "$(uname -m)" in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=amd64;; *) echo 'Unsupported architecture' >&2; exit 1;; esac
command -v tar >/dev/null || { echo 'tar is required' >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
if [ -n "$release_dir" ]; then
  [ "$version" = latest ] || { echo '--version only applies to downloaded releases; choose the local directory with --from.' >&2; exit 1; }
  [ -d "$release_dir" ] || { echo "Release directory not found: $release_dir" >&2; exit 1; }
  release_dir=$(cd "$release_dir" && pwd)
  [ -f "$release_dir/SHA256SUMS" ] || { echo 'Release directory must contain SHA256SUMS; build packages first.' >&2; exit 1; }
  cp "$release_dir/SHA256SUMS" "$tmp/SHA256SUMS"
else
  case "$repository" in ''|*[!A-Za-z0-9_./-]*) echo 'Supply --repository OWNER/REPO, or --from DIR for local releases.' >&2; exit 1;; esac
  command -v curl >/dev/null || { echo 'curl is required' >&2; exit 1; }
  if [ "$version" = latest ]; then
    base="https://github.com/$repository/releases/latest/download"
  else
    base="https://github.com/$repository/releases/download/$version"
  fi
  curl --proto '=https' --tlsv1.2 -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"
fi
# The checksum manifest identifies the version, avoiding a separate API request.
asset=$(awk -v suffix="-$os-$arch.tar.gz" '$2 ~ /^(brote|delve-llm-adapter)-/ && substr($2,length($2)-length(suffix)+1)==suffix {print $2}' "$tmp/SHA256SUMS")
case "$asset" in ''|*/*|*..*|*[!A-Za-z0-9._+-]*) echo 'Missing or invalid release asset' >&2; exit 1;; esac
if [ -n "$release_dir" ]; then
  cp "$release_dir/$asset" "$tmp/$asset"
else
  curl --proto '=https' --tlsv1.2 -fsSL "$base/$asset" -o "$tmp/$asset"
fi
expected=$(awk -v name="$asset" '$2==name {print $1}' "$tmp/SHA256SUMS")
if command -v sha256sum >/dev/null; then actual=$(sha256sum "$tmp/$asset" | awk '{print $1}'); else actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}'); fi
[ "$actual" = "$expected" ] || { echo 'Release checksum mismatch' >&2; exit 1; }
tar -xzf "$tmp/$asset" -C "$tmp"
set -- setup --bundle "$tmp/delve-llm-adapter"
[ -z "$agent" ] || set -- "$@" --agent "$agent"
[ -z "$editor" ] || set -- "$@" --editor "$editor"
executable="$tmp/delve-llm-adapter/bin/brote"
[ -x "$executable" ] || executable="$tmp/delve-llm-adapter/bin/delve-llm-adapter"
[ -x "$executable" ] || { echo 'Release has no Brote executable' >&2; exit 1; }
echo 'Installing Brote and the selected integrations…' >&2
"$executable" "$@"
[ "$agent" != pi ] || echo 'Run /reload in Pi to load Brote.'

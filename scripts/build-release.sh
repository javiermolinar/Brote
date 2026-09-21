#!/bin/sh
# CI release validation; building GitHub artifacts needs no registry credentials.
set -eu

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must name the release repository}"
version=$(node -p 'require("./package.json").version')
if [ -n "${RELEASE_TAG:-}" ] && [ "$RELEASE_TAG" != "v$version" ]; then
  echo "Release tag must match package version v$version" >&2
  exit 1
fi

# Sideloaded VSIXs may use the staging publisher. Only Marketplace publication
# requires a configured publisher; npm publication is deferred.
if [ "${VSCODE_PUBLISH_ENABLED:-false}" = true ]; then
  case "${VSCODE_PUBLISHER:-}" in
    ''|brote-local)
      echo 'Set VSCODE_PUBLISHER before enabling Marketplace publication.' >&2
      exit 1;;
  esac
fi

python3 scripts/release.py --version "$version" \
  --repository "$GITHUB_REPOSITORY" --npm-name "${BROTE_NPM_NAME:-brote-pi}"

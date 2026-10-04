#!/bin/sh
# Source this file (`. scripts/use-pnpm.sh`) to put the pnpm version each
# package.json pins in `packageManager` on PATH.
#
# corepack writes its shims into .tools/corepack-bin inside the checkout. A
# plain `corepack enable` writes them next to the host's global node instead,
# and the CI build host shares that directory with every runner and repo on it.
export COREPACK_ENABLE_DOWNLOAD_PROMPT=0
_use_pnpm_root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
mkdir -p "$_use_pnpm_root/.tools/corepack-bin"
# Node 25 and later no longer bundle corepack (the CI host's Homebrew node is
# 26). Install a pinned copy into the checkout instead of the shared host.
if ! command -v corepack >/dev/null 2>&1; then
	npm install --silent --no-save --no-package-lock --prefix "$_use_pnpm_root/.tools/corepack" corepack@0.36.0 >/dev/null
	export PATH="$_use_pnpm_root/.tools/corepack/node_modules/.bin:$PATH"
fi
corepack enable --install-directory "$_use_pnpm_root/.tools/corepack-bin" pnpm
export PATH="$_use_pnpm_root/.tools/corepack-bin:$PATH"
unset _use_pnpm_root

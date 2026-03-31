#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
export RUNOQ_ROOT

runtime_bin="${RUNOQ_RUNTIME_BIN:-}"
if [[ -n "$runtime_bin" ]]; then
  exec "$runtime_bin" "__issue_queue" "$@"
fi
go_bin="${RUNOQ_GO_BIN:-go}"
command -v "$go_bin" >/dev/null 2>&1 || {
  echo "runoq: Go toolchain not found: $go_bin" >&2
  exit 1
}
cd "$RUNOQ_ROOT"
exec "$go_bin" run "$RUNOQ_ROOT/cmd/runoq-runtime" "__issue_queue" "$@"


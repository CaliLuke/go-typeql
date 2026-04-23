#!/usr/bin/env bash
# Quality gates for day-to-day development. Scoped to unit packages
# (ast/, gotype/, tqlgen/, cmd/) — driver/ needs CGo + built Rust lib, which
# is the province of release-checks and `make test-integration`.
#
# Usage:
#   ./check.sh          run all gates
#   ./check.sh --fix    auto-format (goimports -w) then run gates
set -euo pipefail

FIX="${1:-}"
ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

UNIT_PKGS=(./ast/... ./gotype/... ./tqlgen/... ./cmd/...)
PKG_DIRS=$(go list -f '{{.Dir}}' "${UNIT_PKGS[@]}" | tr '\n' ' ')

FAILED=()
run_gate() {
  local name="$1"; shift
  echo ""
  echo "▶  $name"
  if "$@"; then return 0; else FAILED+=("$name"); return 0; fi
}

echo "══════════════════════════════════════"
echo "  Go Quality Gates (unit scope)"
echo "══════════════════════════════════════"

# Formatting first — fixable, no reason to fail later gates on it.
if [ "$FIX" = "--fix" ]; then
  run_gate "goimports (write)" bash -c "goimports -w $PKG_DIRS"
else
  run_gate "goimports (check)" bash -c "
    out=\$(goimports -l $PKG_DIRS)
    if [ -n \"\$out\" ]; then
      echo \"drifted files (run: ./check.sh --fix):\"
      echo \"\$out\"
      exit 1
    fi
  "
fi

run_gate "go build" go build "${UNIT_PKGS[@]}"
run_gate "go vet" go vet "${UNIT_PKGS[@]}"

run_gate "go mod tidy (drift)" bash -c '
  cp go.mod go.mod.bak; cp go.sum go.sum.bak
  go mod tidy
  d1=$(diff -u go.mod.bak go.mod || true)
  d2=$(diff -u go.sum.bak go.sum || true)
  mv go.mod.bak go.mod; mv go.sum.bak go.sum
  if [ -n "$d1" ] || [ -n "$d2" ]; then
    echo "go.mod/go.sum drifted — run: go mod tidy"
    echo "$d1"; echo "$d2"; exit 1
  fi
'

if [ "$FIX" = "--fix" ]; then
  run_gate "golangci-lint" golangci-lint run --fix
else
  run_gate "golangci-lint" golangci-lint run
fi

run_gate "staticcheck" "$HOME/go/bin/staticcheck" "${UNIT_PKGS[@]}"

run_gate "go test" go test "${UNIT_PKGS[@]}" -timeout 120s

# Non-test Go sources for the gocyclo / dupl blocking gates — production code
# only. Test files routinely have high parallel-symmetry (deliberate) and
# long table-driven functions; gating them fights readability.
NON_TEST_GO=$(find ast gotype tqlgen cmd -name '*.go' -not -name '*_test.go' 2>/dev/null | tr '\n' ' ')

run_gate "gocyclo (>20, non-test)" bash -c "
  out=\$(gocyclo -over 20 $NON_TEST_GO 2>/dev/null)
  if [ -n \"\$out\" ]; then
    echo \"\$out\"
    exit 1
  fi
"

run_gate "dupl (>=100 tokens, non-test)" bash -c "
  out=\$(dupl -threshold 100 $NON_TEST_GO 2>/dev/null)
  groups=\$(echo \"\$out\" | grep -c '^found')
  if [ \"\$groups\" -gt 0 ]; then
    echo \"\$out\"
    exit 1
  fi
"

# Tracked metrics — include tests, not blocking.
set +e
echo ""
echo "── reports (non-blocking) ─────────────"
dgroups=$(dupl -threshold 50 $PKG_DIRS 2>/dev/null | grep -c '^found')
echo "  dupl @50 (all):     $dgroups clone groups"
cyc=$(gocyclo -over 15 $PKG_DIRS 2>/dev/null | wc -l | tr -d ' ')
echo "  gocyclo >15 (all):  $cyc functions"
set -e

echo ""
echo "══════════════════════════════════════"
if [ "${#FAILED[@]}" -eq 0 ]; then
  echo "  All gates passed"
else
  echo "  ${#FAILED[@]} gate(s) failed:"
  for name in "${FAILED[@]}"; do echo "     - $name"; done
  exit 1
fi
echo "══════════════════════════════════════"

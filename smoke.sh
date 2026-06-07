#!/bin/bash
# remotecmd-cli — smoke test suite
# Usage: bash smoke.sh [target...]
# If no targets specified, tests all configured targets.
# Examples:
#   bash smoke.sh                    # all targets
#   bash smoke.sh dk1 rbm21         # specific targets

set -uo pipefail

OK=0
FAIL=0
TARGETS=("$@")
cd "$(dirname "$0")"
RC="./remotecmd-cli"

if [ ${#TARGETS[@]} -eq 0 ]; then
  mapfile -t TARGETS < <($RC list-targets 2>/dev/null | grep -oP '^\S+' | sed 's/→//g' | tr -d ' ')
fi

if [ ${#TARGETS[@]} -eq 0 ]; then
  echo ":: No targets configured. Run 'remotecmd-cli add-target' first."
  exit 1
fi

# Use arithmetic expressions that always return truthy to avoid bash gotchas
pass() { echo "  ✅ $1"; OK=$((OK+1)); return 0; }
fail() { echo "  ❌ $1"; FAIL=$((FAIL+1)); return 1; }
check() { local label="$1" rc="$2"; if [ "$rc" -eq 0 ]; then pass "$label"; else fail "$label"; fi; }

echo "═══ remotecmd-cli smoke test ═══"
echo "    targets: ${TARGETS[*]}"
echo "    binary:  $(readlink -f "$RC")"
echo "    version: $($RC version 2>/dev/null)"
echo ""

# ── 1. Self tests ──
echo "── 1. Unit tests ──"
go test -count=1 -timeout 60s ./... >/dev/null 2>&1; check "go test" $?

# ── 2. CLI basics ──
echo ""
echo "── 2. CLI basics ──"
$RC version >/dev/null 2>&1;                                 check "version" $?
$RC help >/dev/null 2>&1;                                    check "help" $?
$RC list-targets >/dev/null 2>&1;                            check "list-targets" $?

# ── 3. Group management ──
echo ""
echo "── 3. Group management ──"
FIRST="${TARGETS[0]}"
GROUP_OK=0
$RC group create --name smoke-test --targets "$FIRST" >/dev/null 2>&1 && GROUP_OK=1; [ "$GROUP_OK" -eq 1 ] && pass "group create" || fail "group create"
$RC group list >/dev/null 2>&1;                              check "group list" $?
$RC exec --group smoke-test --cmd 'hostname' >/dev/null 2>&1; check "exec --group" $?
$RC group delete --name smoke-test >/dev/null 2>&1;           check "group delete" $?

# ── 4. Single-target exec (rcx) ──
echo ""
echo "── 4. Single-target rcx ──"
for t in "${TARGETS[@]}"; do
  name=$(echo "$t" | sed 's/→//g' | tr -d ' ')
  rcx "$name" 'hostname' >/dev/null 2>&1;                     check "rcx $name" $?
  rcx "$name" 'echo out && echo err >&2' >/dev/null 2>&1;     check "rcx $name stderr" $?
done

# ── 5. Legacy exec syntax ──
echo ""
echo "── 5. Legacy exec ──"
$RC --target "$FIRST" --cmd 'hostname' >/dev/null 2>&1;       check "legacy --target" $?
$RC --target "$FIRST" --cmd 'echo hi' --timeout 5 >/dev/null 2>&1; check "legacy --timeout" $?

# ── 6. exec subcommand ──
echo ""
echo "── 6. exec subcommand ──"
$RC exec --target "$FIRST" --cmd 'hostname' >/dev/null 2>&1;  check "exec --target" $?
$RC exec --target "$FIRST" --cmd 'echo hi' --stream >/dev/null 2>&1; check "exec --stream" $?

# ── 7. Multi-target exec ──
echo ""
echo "── 7. Multi-target ──"
if [ ${#TARGETS[@]} -ge 2 ]; then
  LIST=$(IFS=,; echo "${TARGETS[*]}")
  $RC exec --targets "$LIST" --cmd 'hostname' --format table >/dev/null 2>&1; check "multi table" $?
  $RC exec --targets "$LIST" --cmd 'hostname' --format json >/dev/null 2>&1;  check "multi json" $?
else
  echo "  ⏭  skip (need ≥2 targets)"
fi

# ── 8. rcs daemon status ──
echo ""
echo "── 8. rcs daemon status ──"
for t in "${TARGETS[@]}"; do
  name=$(echo "$t" | sed 's/→//g' | tr -d ' ')
  rcs "$name" >/dev/null 2>&1;                               check "rcs $name" $?
done

# ── 9. File copy ──
echo ""
echo "── 9. File copy ──"
SRC=$(mktemp)
echo "smoke-test-content" > "$SRC"
DST="/tmp/smoke-test-remote.$$"
$RC cp --target "$FIRST" --src "$SRC" --dst "$DST" >/dev/null 2>&1; check "cp file" $?
rcx "$FIRST" "cat $DST" >/dev/null 2>&1;                         check "cp verify" $?
rcx "$FIRST" "rm -f $DST" >/dev/null 2>&1
rm -f "$SRC"

# ── 10. Directory copy ──
echo ""
echo "── 10. Directory copy ──"
DIR=$(mktemp -d)
echo "dir-file" > "$DIR"/test.txt
DSTDIR="/tmp/smoke-test-dir-remote.$$"
$RC cp --target "$FIRST" --src "$DIR" --dst "$DSTDIR" >/dev/null 2>&1; check "cp dir" $?
rcx "$FIRST" "cat $DSTDIR/test.txt" >/dev/null 2>&1;                 check "cp dir verify" $?
rcx "$FIRST" "rm -rf $DSTDIR" >/dev/null 2>&1
rm -rf "$DIR"

# ── 11. rcc alias ──
echo ""
echo "── 11. rcc alias ──"
SRC=$(mktemp)
echo "rcc-test" > "$SRC"
DST="/tmp/rcc-test.$$"
rcc "$FIRST" "$SRC" "$DST" >/dev/null 2>&1;               check "rcc file" $?
rcc "$FIRST" "$SRC" "$DST" --stream >/dev/null 2>&1;      check "rcc --stream" $?
rcx "$FIRST" "rm -f $DST" >/dev/null 2>&1
rm -f "$SRC"

# ── 12. Exit codes ──
echo ""
echo "── 12. Exit codes ──"
$RC exec --target "$FIRST" --cmd 'hostname' >/dev/null 2>&1;    check "exit 0 (success)" $?
$RC exec 2>/dev/null && E=0 || E=$?; [ "$E" -eq 3 ] && pass "exit 3 (missing args)" || fail "exit 3 (got $E)"
$RC nonexistent-command 2>/dev/null && E=0 || E=$?; [ "$E" -eq 3 ] && pass "exit 3 (unknown cmd)" || fail "exit 3 (got $E)"

# ── 13. rcl alias ──
echo ""
echo "── 13. rcl alias ──"
rcl >/dev/null 2>&1;                                           check "rcl" $?
rcl --help >/dev/null 2>&1;                                    check "rcl --help" $?

# ── Summary ──
echo ""
TOTAL=$((OK + FAIL))
PCT=$((OK * 100 / TOTAL))
echo "═══ Results: $OK/$TOTAL passed ($PCT%) ═══"
if [ "$FAIL" -gt 0 ]; then
  echo "  ❌ $FAIL test(s) FAILED"
  exit 1
else
  echo "  ✅ All smoke tests passed"
fi

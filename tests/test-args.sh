#!/bin/bash
# Checks the command line argument handling of the backup scripts.
# Runs on the host without database or wiki: invalid input must be rejected
# before the scripts touch anything.

cd "$(dirname "$0")/.." || exit 1

fail=0
check() {
    local desc=$1 want=$2; shift 2
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [ "$rc" -ne "$want" ] || ! grep -q usage <<<"$out"; then
        echo "FAIL: $desc (exit $rc, want $want)"; echo "$out" | sed 's/^/    /'
        fail=1
    else
        echo "ok:   $desc"
    fi
}

for script in create create-logs repair restore; do
    check "$script --help"        0 bash "./$script" --help
    check "$script unknown arg"   1 bash "./$script" bogus
done
check "restore gh-asset without repository" 1 env GH_API_TOKEN=x bash ./restore gh-asset

exit $fail

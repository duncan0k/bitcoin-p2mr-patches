#!/usr/bin/env bash
# Wait for the Job m3-build-$1 to finish (at most $2 seconds, default 3000), then print its
# summary and the tail of its log.
set -uo pipefail
K="sudo -n k3s kubectl -n p2mr-build"
RUN_ID="$1"
LIMIT="${2:-3000}"
start=$(date +%s)
state=running
while :; do
    succeeded="$($K get job "m3-build-$RUN_ID" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)"
    failed="$($K get job "m3-build-$RUN_ID" -o jsonpath='{.status.failed}' 2>/dev/null || true)"
    if [ "${succeeded:-0}" -ge 1 ]; then state=succeeded; break; fi
    if [ "${failed:-0}" -ge 1 ]; then state=failed; break; fi
    if [ $(( $(date +%s) - start )) -ge "$LIMIT" ]; then state=still-running; break; fi
    sleep 30
done
echo "==> m3-build-$RUN_ID: $state"
$K exec p2mr-shell -- cat "/work/m3-logs/$RUN_ID/summary.txt" 2>/dev/null || true
echo "==> log tail"
$K logs "job/m3-build-$RUN_ID" --tail="${TAIL:-80}" 2>/dev/null || true

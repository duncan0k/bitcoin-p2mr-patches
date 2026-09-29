#!/usr/bin/env bash
#
# Network a only: upgrade its M1 node to the M2 build after the rule's height,
# the way an operator who upgrades late would, and put it back afterwards.
#
#   KUBECTL="sudo k3s kubectl" ./m2_upgrade.sh <M2 node image> <M1 node image>
#
# 1. nodem1 gets the M2 image and node A's -signetpqblock line, and starts once
#    with -reindex-chainstate: it checks every block again and must end at
#    node A's tip.
# 2. It restarts without -reindex-chainstate: -checkblocks passes.
# 3. It goes back to the M1 image and its own configuration.
set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
M2_IMAGE="${1:?M2 node image}"
M1_IMAGE="${2:?M1 node image}"
NS=ark0-m2a
K="$KUBECTL -n $NS"
log() { printf '==> %s\n' "$*"; }

cli() { # <node> <args...>
    local node="$1"; shift
    $K exec "$node-0" -- bitcoin-cli "-conf=/config/$node.conf" -datadir=/data "$@"
}

wait_synced() { # nodem1 at node A's tip
    for _ in $(seq 1 120); do
        if [ "$(cli nodem1 getbestblockhash 2>/dev/null)" = "$(cli nodea getbestblockhash)" ]; then return 0; fi
        sleep 5
    done
    echo "nodem1 did not reach node A's tip" >&2
    return 1
}

set_node() { # <image> <conf key> [extra arg]
    local patch
    patch="$(printf '[{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"%s"},{"op":"replace","path":"/spec/template/spec/containers/0/args","value":["-conf=/config/%s","-datadir=/data","-printtoconsole","-rpcauth=$(M2_RPCAUTH)"%s]}]' \
        "$1" "$2" "${3:+,\"$3\"}")"
    $K patch statefulset nodem1 --type=json -p "$patch" > /dev/null
    $K rollout status statefulset/nodem1 --timeout=900s > /dev/null
}

log "nodem1 is at $(cli nodem1 getblockcount), node A at $(cli nodea getblockcount)"
rule="$($K get configmap m2-conf -o jsonpath='{.data.nodea\.conf}' | grep '^signetpqblock=')"
conf="$($K get configmap m2-conf -o jsonpath='{.data.nodem1\.conf}')"
upgraded="$(printf '%s\n' "$conf" | sed "s|^signetpowtargetspacing=|$rule\nsignetpowtargetspacing=|")"
printf '%s\n' "$upgraded" | grep -q '^signetpqblock=' || { echo "could not add the rule" >&2; exit 1; }
# One more key in the ConfigMap, the others untouched: a merge patch, from a file.
patch_file="$(mktemp)"
trap 'rm -f "$patch_file"' EXIT
python3 - "$upgraded" > "$patch_file" <<'EOF'
import json, sys
print(json.dumps({"data": {"nodem1-m2.conf": sys.argv[1] + "\n"}}))
EOF
$K patch configmap m2-conf --type=merge --patch-file "$patch_file" > /dev/null

log "1. M2 image, the rule, -reindex-chainstate"
set_node "$M2_IMAGE" nodem1-m2.conf -reindex-chainstate
$K logs nodem1-0 | grep -m1 'ML-DSA-44 block signature from height' || true
wait_synced
log "   nodem1 at $(cli nodem1 getblockcount), the same tip as node A"

log "2. restart without -reindex-chainstate"
set_node "$M2_IMAGE" nodem1-m2.conf
if $K logs nodem1-0 | grep -q 'Corrupted block database'; then echo "-checkblocks failed" >&2; exit 1; fi
wait_synced
log "   nodem1 at $(cli nodem1 getblockcount), the same tip as node A"

log "3. back to the M1 image and its own configuration"
set_node "$M1_IMAGE" nodem1.conf
wait_synced
log "   nodem1 at $(cli nodem1 getblockcount), the same tip as node A"

#!/usr/bin/env bash
#
# Give the two M3 nodes of ark0-m3 the tripwire rules, once the tripwire
# outputs exist.
#
#   KUBECTL="sudo k3s kubectl" ./m3_arm.sh <H3> <txid:vout>,<txid:vout>
#
# The outpoints are only known once the chain runs and `m3_drill.py tripwires`
# has created the two outputs, so the network starts with -signetmldsa44 alone.
# This adds -signettripwire=<H3>@<outpoints> to nodea.conf and nodeb.conf,
# restarts both nodes, and checks that both log the same rules and report both
# outputs unspent.
#
# It refuses unless both outputs are unspent, mempool included, with at least
# MIN_CONF confirmations (default 6), and the tip is at least MARGIN blocks
# below H3 (default 120, an hour at 30 s a block): both nodes have to run the
# rules well before the chain reaches the height. It also refuses a node that
# has another -signettripwire line already. Every node of a network has to run
# the same values from the start; a network with other values is a new network.
# Run again with the same values, it restarts nothing and only checks.
set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
H3="${1:-}"
OUTPOINTS="${2:-}"
MARGIN="${MARGIN:-120}"
MIN_CONF="${MIN_CONF:-6}"
NS=ark0-m3
K="$KUBECTL -n $NS"
log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[[ "$H3" =~ ^[0-9]+$ && "$OUTPOINTS" =~ ^[0-9a-f]{64}:[0-9]+,[0-9a-f]{64}:[0-9]+$ ]] \
    || die "usage: $0 <H3> <txid:vout>,<txid:vout>"

cli() { # <node> <args...>
    local node="$1"; shift
    $K exec "$node-0" -- bitcoin-cli "-conf=/config/$node.conf" -datadir=/data "$@"
}

tip="$(cli nodea getblockcount)"
(( H3 - tip >= MARGIN )) || die "the tip is at $tip, fewer than $MARGIN blocks below $H3"
log "tip $tip, H3 $H3"

for op in ${OUTPOINTS//,/ }; do
    out="$(cli nodea gettxout "${op%:*}" "${op##*:}" true)"
    [ -n "$out" ] || die "$op is not an unspent output, mempool included"
    read -r confirmations spk < <(python3 -c 'import json, sys; o = json.load(sys.stdin); print(o["confirmations"], o["scriptPubKey"]["hex"])' <<< "$out")
    (( confirmations >= MIN_CONF )) || die "$op has $confirmations confirmations, fewer than $MIN_CONF"
    log "$op: $confirmations confirmations, scriptPubKey $spk"
done

line="signettripwire=$H3@$OUTPOINTS"
armed=0
for node in nodea nodeb; do
    conf="$($K get configmap m3-conf -o "jsonpath={.data.$node\.conf}")"
    if grep -q '^signettripwire=' <<< "$conf"; then
        grep -qxF "$line" <<< "$conf" || die "$node.conf has another -signettripwire line already"
        armed=$((armed + 1))
    fi
done

# Add the line to both nodes' configuration and restart them.
arm() {
    log "adding $line to nodea.conf and nodeb.conf"
    patch_file="$(mktemp)"
    trap 'rm -f "$patch_file"' EXIT
    # Two keys of the ConfigMap, the others untouched: a merge patch, from a file.
    python3 - "$($K get configmap m3-conf -o 'jsonpath={.data.nodea\.conf}')" \
              "$($K get configmap m3-conf -o 'jsonpath={.data.nodeb\.conf}')" "$line" > "$patch_file" <<'EOF'
import json, sys
nodea, nodeb, line = sys.argv[1:4]

def armed(conf):
    lines = conf.rstrip("\n").split("\n")
    at = [i for i, text in enumerate(lines) if text.startswith("signetmldsa44=")]
    assert len(at) == 1, "no single -signetmldsa44 line"
    lines.insert(at[0] + 1, line)
    return "\n".join(lines) + "\n"

print(json.dumps({"data": {"nodea.conf": armed(nodea), "nodeb.conf": armed(nodeb)}}))
EOF
    $K patch configmap m3-conf --type=merge --patch-file "$patch_file" > /dev/null

    for node in nodea nodeb; do
        log "restarting $node"
        $K rollout restart "statefulset/$node" > /dev/null
        $K rollout status "statefulset/$node" --timeout=600s > /dev/null
    done
}

if [ "$armed" = "2" ]; then
    log "nodea.conf and nodeb.conf have $line already; checking only"
elif [ "$armed" = "1" ]; then
    die "only one of nodea.conf and nodeb.conf has $line"
else
    arm
fi

expected="Signet P2MR tripwire rules from height $H3 in leaf version 0xe0; tripwire outputs ${OUTPOINTS/,/ and }"
for node in nodea nodeb; do
    # Read whole: grep -q on a pipe would stop at the match, and kubectl logs, writing on into the
    # closed pipe, would fail the pipeline under pipefail although the line is there.
    node_log="$($K logs "$node-0")"
    grep -qF "$expected" <<< "$node_log" || die "$node did not log: $expected"
    python3 - "$(cli "$node" gettripwireinfo)" "$H3" "$OUTPOINTS" <<'EOF' || die "$node does not report the rules as set"
import json, sys
info, h3, outpoints = json.loads(sys.argv[1]), int(sys.argv[2]), sys.argv[3].split(",")
assert info["height"] == h3 and info["leaf_version"] == "e0", info
assert [t["outpoint"] for t in info["tripwires"]] == outpoints, info
assert all(t["state"] == "unspent" for t in info["tripwires"]), info
assert info["ec_disabled_next_block"] is False and info["tip_height"] < h3, info
EOF
    log "$node: rules from $H3, both tripwire outputs unspent"
done

for _ in $(seq 1 60); do
    [ "$(cli nodea getbestblockhash)" = "$(cli nodeb getbestblockhash)" ] && break
    sleep 5
done
[ "$(cli nodea getbestblockhash)" = "$(cli nodeb getbestblockhash)" ] || die "node A and node B do not agree on the tip"
log "armed: both nodes at $(cli nodea getblockcount), $(( H3 - $(cli nodea getblockcount) )) blocks below $H3"

#!/usr/bin/env bash
#
# Bring up the M3 staging network from a manifest render.py wrote.
#
#   KUBECTL="sudo k3s kubectl" ./up.sh m3.yaml
#
# Makes the namespace and the RPC credentials first, so that no pod starts
# without them, applies the manifest, waits for the three nodes, and gives
# node A the wallet that signs the signet solution, loaded on every startup.
# Re-running is safe: existing credentials and an existing wallet are kept.
#
# The credentials go to the cluster through stdin, never through a command
# line: a command run with sudo is logged with its arguments.
set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
FILE="${1:-}"
[ -f "$FILE" ] || { echo "usage: $0 <manifest>" >&2; exit 2; }
NS=ark0-m3
log() { printf '==> %s\n' "$*"; }

log "namespace $NS"
$KUBECTL apply -f - > /dev/null <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $NS
  labels:
    app.kubernetes.io/part-of: ark0-m3
EOF

if $KUBECTL -n "$NS" get secret m3-rpc > /dev/null 2>&1; then
    log "RPC credentials exist, kept"
else
    log "making the RPC credentials"
    user=m3
    password="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    salt="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    # rpcauth: HMAC-SHA256 keyed with the salt's hex string, as share/rpcauth/rpcauth.py does.
    hmac="$(printf '%s' "$password" | openssl dgst -sha256 -hmac "$salt" | sed 's/^.*= //')"
    $KUBECTL apply -f - > /dev/null <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: m3-rpc
  namespace: $NS
stringData:
  rpcauth: "$user:$salt\$$hmac"
  rpccredentials.conf: |
    rpcuser=$user
    rpcpassword=$password
EOF
    unset password hmac
fi

log "applying $FILE"
$KUBECTL apply -f "$FILE"

for node in nodea nodeb nodem2; do
    log "waiting for $node"
    $KUBECTL -n "$NS" rollout status "statefulset/$node" --timeout=600s
done

cli=(bitcoin-cli -conf=/config/nodea.conf -datadir=/data)
# The wallet is loaded on startup, so that the producer can sign again once node A has restarted
# (m3_arm.sh restarts it).
loaded="$($KUBECTL -n "$NS" exec nodea-0 -- "${cli[@]}" listwallets)"
present="$($KUBECTL -n "$NS" exec nodea-0 -- "${cli[@]}" listwalletdir)"
if grep -q '"ark0"' <<< "$loaded"; then
    log "node A's wallet is loaded, kept"
elif grep -q '"name": "ark0"' <<< "$present"; then
    log "loading node A's wallet, and on every startup from now on"
    $KUBECTL -n "$NS" exec nodea-0 -- "${cli[@]}" -named loadwallet filename=ark0 load_on_startup=true > /dev/null
else
    log "giving node A the wallet with the challenge key"
    $KUBECTL -n "$NS" exec nodea-0 -- "${cli[@]}" -named createwallet wallet_name=ark0 load_on_startup=true > /dev/null
    # The descriptor comes out of the Secret and goes in through stdin.
    $KUBECTL -n "$NS" get secret m3-signer -o jsonpath='{.data.import\.json}' | base64 -d \
        | $KUBECTL -n "$NS" exec -i nodea-0 -- "${cli[@]}" -rpcwallet=ark0 -stdin importdescriptors \
        | grep -q '"success": true' || { echo "importing the descriptor failed" >&2; exit 1; }
fi

log "$NS is up; the producer starts on its own"
$KUBECTL -n "$NS" get pods

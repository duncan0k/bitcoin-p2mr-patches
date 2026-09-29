#!/usr/bin/env bash
#
# Bring up one M2 staging network from a manifest render.py wrote.
#
#   KUBECTL="sudo k3s kubectl" ./up.sh a m2a.yaml
#   KUBECTL="sudo k3s kubectl" ./up.sh b m2b.yaml
#
# Makes the namespace and the RPC credentials first, so that no pod starts
# without them, applies the manifest, waits for the three nodes, and on
# network a gives node A the wallet that signs the classical signet solution.
# Re-running is safe: existing credentials and an existing wallet are kept.
#
# The credentials go to the cluster through stdin, never through a command
# line: a command run with sudo is logged with its arguments.
set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
NET="${1:-}"
FILE="${2:-}"
case "$NET" in a|b) ;; *) echo "usage: $0 a|b <manifest>" >&2; exit 2 ;; esac
[ -f "$FILE" ] || { echo "no such manifest: $FILE" >&2; exit 2; }
NS="ark0-m2$NET"
log() { printf '==> %s\n' "$*"; }

log "namespace $NS"
$KUBECTL apply -f - > /dev/null <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $NS
  labels:
    app.kubernetes.io/part-of: ark0-m2
EOF

if $KUBECTL -n "$NS" get secret m2-rpc > /dev/null 2>&1; then
    log "RPC credentials exist, kept"
else
    log "making the RPC credentials"
    user=m2
    password="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    salt="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    # rpcauth: HMAC-SHA256 keyed with the salt's hex string, as share/rpcauth/rpcauth.py does.
    hmac="$(printf '%s' "$password" | openssl dgst -sha256 -hmac "$salt" | sed 's/^.*= //')"
    $KUBECTL apply -f - > /dev/null <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: m2-rpc
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

for node in nodea nodeb nodem1; do
    log "waiting for $node"
    $KUBECTL -n "$NS" rollout status "statefulset/$node" --timeout=600s
done

if [ "$NET" = a ]; then
    cli=(bitcoin-cli -conf=/config/nodea.conf -datadir=/data)
    if $KUBECTL -n "$NS" exec nodea-0 -- "${cli[@]}" listwallets | grep -q '"ark0"'; then
        log "node A's wallet exists, kept"
    else
        log "giving node A the wallet with the classical challenge key"
        $KUBECTL -n "$NS" exec nodea-0 -- "${cli[@]}" -named createwallet wallet_name=ark0 > /dev/null
        # The descriptor comes out of the Secret and goes in through stdin.
        $KUBECTL -n "$NS" get secret m2-signer -o jsonpath='{.data.import\.json}' | base64 -d \
            | $KUBECTL -n "$NS" exec -i nodea-0 -- "${cli[@]}" -rpcwallet=ark0 -stdin importdescriptors \
            | grep -q '"success": true' || { echo "importing the descriptor failed" >&2; exit 1; }
    fi
fi

log "$NS is up; the producer starts on its own"
$KUBECTL -n "$NS" get pods

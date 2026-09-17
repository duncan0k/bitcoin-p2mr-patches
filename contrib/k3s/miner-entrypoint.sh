#!/usr/bin/env bash
#
# Entry point of the p2mr-miner image, installed as /opt/ark0/entrypoint.sh.
#
# ark0.py drives node A through `bitcoin-cli -datadir=<dir> -signet`, which on
# the host reads that node's own bitcoin.conf and cookie file. A producer in
# its own pod has neither, so this script writes a small client configuration
# into each of the two directories ark0.py uses as data directories. They hold
# nothing but a bitcoin.conf: bitcoin-cli needs no chain of its own, only
# somewhere to read connection settings from.
#
# With no arguments it runs the block producing loop. With arguments it runs
# them instead, which is how the image is inspected without a cluster.
#
# Every variable it reads has its default in Dockerfile.miner, and `set -u`
# turns a missing one into an immediate, named failure rather than a silent
# connection to the wrong place.
set -euo pipefail

die() { printf 'ark0 entrypoint: %s\n' "$*" >&2; exit 1; }
note() { printf 'ark0 entrypoint: %s\n' "$*" >&2; }

# One client configuration: <directory> <rpc host> <rpc port>. The credentials
# come from a mounted Secret and are appended verbatim, so the password never
# reaches this image, a command line, or a log.
write_client_conf() {
    mkdir -p "$1"
    {
        echo "# Written at start-up by /opt/ark0/entrypoint.sh. Not a node data directory:"
        echo "# bitcoin-cli reads its connection settings here and nothing else."
        echo "signet=1"
        echo "[signet]"
        echo "rpcconnect=$2"
        echo "rpcport=$3"
        if [ -r "$ARK0_CREDENTIALS" ]; then
            cat "$ARK0_CREDENTIALS"
        fi
    } > "$1/bitcoin.conf"
    chmod 0600 "$1/bitcoin.conf"
}

umask 077
write_client_conf "$ARK0_NODE_A" "$ARK0_RPC_HOST_A" "$ARK0_RPC_PORT_A"
write_client_conf "$ARK0_NODE_B" "$ARK0_RPC_HOST_B" "$ARK0_RPC_PORT_B"

# ark0.py reads the coinbase destination from $ARK0_HOME/reward_address.txt.
# It is an address, not a key, so it travels in the ConfigMap.
if [ -r "$ARK0_REWARD_ADDRESS_FILE" ]; then
    install -m 0644 "$ARK0_REWARD_ADDRESS_FILE" "$ARK0_HOME/reward_address.txt"
fi

if [ "$#" -gt 0 ]; then
    if [ ! -r "$ARK0_CREDENTIALS" ]; then
        note "no credentials at $ARK0_CREDENTIALS; any RPC call will be refused"
    fi
    exec "$@"
fi

# Producing blocks without credentials or a destination cannot work, so say so
# now instead of failing once every retry interval.
[ -r "$ARK0_CREDENTIALS" ] || die "no credentials at $ARK0_CREDENTIALS"
[ -r "$ARK0_HOME/reward_address.txt" ] || die "no reward address at $ARK0_REWARD_ADDRESS_FILE"

note "producing one block every ${ARK0_BLOCK_INTERVAL}s against $ARK0_RPC_HOST_A:$ARK0_RPC_PORT_A"
exec bash /opt/ark0/demo/miner_loop.sh

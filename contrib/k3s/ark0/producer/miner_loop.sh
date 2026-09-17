#!/bin/bash
# Ark-0 block producer.
#
# Produces one signet block every ARK0_BLOCK_INTERVAL seconds (default 90)
# through ark0.py, which assembles the block exactly the way
# contrib/signet/miner does: getblocktemplate -> new_block -> PSBT ->
# walletprocesspsbt (signs the signet solution) -> solve -> submitblock.
#
# contrib/signet/miner --ongoing is the upstream way to do this and works
# here too, but it paces itself at 600s * retarget_factor with the factor
# floored at 0.25, so it cannot go below 150s per block. This loop exists
# only to hit the 1-2 minute cadence this network is configured for. It is a
# producer cadence, not a consensus parameter: signet's nPowTargetSpacing is
# 600 s and nothing here changes it.
#
# Two environment variables, both with a default:
#
#   ARK0_BLOCK_INTERVAL   seconds between blocks          90
#   ARK0_PY               path to ark0.py                 $HOME/.ark0/demo/ark0.py
#
# Dockerfile.miner sets both to the layout inside the producer image.
set -uo pipefail

INTERVAL="${ARK0_BLOCK_INTERVAL:-90}"
ARK0_PY="${ARK0_PY:-$HOME/.ark0/demo/ark0.py}"

echo "ark0 block producer: one block every ${INTERVAL}s"
while true; do
    if ! python3 "$ARK0_PY" mine 1 --quiet; then
        echo "block production failed; retrying in 15s" >&2
        sleep 15
        continue
    fi
    sleep "$INTERVAL"
done

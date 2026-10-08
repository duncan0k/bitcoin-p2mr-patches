#!/usr/bin/env bash
#
# Make the two M3 images of the staging network from a run of ../m3-dev/m3-build.sh that passed
# with M3_IMGCTX=1, which left its binaries and their PROVENANCE.txt in /work/m3-imgctx/node:
#
#   KUBECTL="sudo k3s kubectl" ./images.sh <run id>
#
# Everything is staged in /work/m3-imgctx, apart from what Ark-0's release builds use, and the
# images are named after the series head that provenance records, the commit of the Core tree the
# patches were exported from: p2mr-node:v31.1-p2mr-m3-dev-<head12> and
# p2mr-miner:v31.1-p2mr-m3-dev-<head12>. The producer's context takes the signet miner and
# test_framework from /work/m3-src, so run this before another run of m3-build.sh resets that tree.
set -euo pipefail
# shellcheck source=../common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../common.sh"

RUN="${1:-}"
[[ "$RUN" =~ ^m3-[A-Za-z0-9.-]+$ ]] || die "usage: $0 <run id of a passing m3-build.sh run with M3_IMGCTX=1>"
export CONTEXT_ROOT=m3-imgctx
PROV=/work/m3-imgctx/node/PROVENANCE.txt

in_shell grep -q "^run id *: $RUN\$" "$PROV" || die "$PROV is not from run $RUN"
in_shell cmp -s "$PROV" "/work/m3-out/runs/$RUN/bin/PROVENANCE.txt" \
    || die "/work/m3-out/runs/$RUN/bin does not hold the same PROVENANCE.txt"
HEAD="$(in_shell sed -n 's/^series head *: *//p' "$PROV")"
[[ "$HEAD" =~ ^[0-9a-f]{40}$ ]] || die "no series head in $PROV"
export IMAGE_TAG_SUFFIX="${HEAD:0:12}"
log "run $RUN, series head $HEAD"

# The Ark-0 observe CronJob runs at minute 5 of every ten (5-59/10): start no kaniko pod in that
# minute or the one before it, as ../m3-dev/m3-run.sh does.
window() { while :; do m=$(( 10#$(date -u +%M) % 10 )); [ "$m" -ne 4 ] && [ "$m" -ne 5 ] && return; sleep 20; done; }

push_file "$HERE/Dockerfile.node" /work/m3-imgctx/node
BUILT_TREE=/work/m3-src BUILT_BIN="/work/m3-out/runs/$RUN/bin" "$HERE/stage-miner.sh"
window
NODE_IMAGE=p2mr-node:v31.1-p2mr-m3-dev "$HERE/build-image.sh" node
window
MINER_IMAGE=p2mr-miner:v31.1-p2mr-m3-dev "$HERE/build-image.sh" miner

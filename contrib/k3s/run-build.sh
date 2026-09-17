#!/usr/bin/env bash
#
# Start one build-and-test run and follow it.
#
# Publishes contrib/k3s/build.sh as the ConfigMap p2mr-build-scripts, renders
# build.yaml for this run, applies it, waits for the Job, and prints the tail of
# the log together with the run's summary file.
#
#   KUBECTL="sudo k3s kubectl" ./run-build.sh
#
# Everything is configured through the environment; the defaults build the ten
# consensus patches and run the P2MR functional tests. For the full M0.5 run:
#
#   KUBECTL="sudo k3s kubectl" \
#   PATCH_SETS="master m05" FRESH_CLONE=1 RUN_DEFAULT_FUNCTIONAL=1 \
#   FUNCTIONAL_TESTS="wallet_p2mr.py wallet_p2mr_signet.py wallet_p2mr_multisig.py \
#                     wallet_p2mr_timelock.py feature_p2mr.py feature_p2mr_signet.py \
#                     p2p_segwit.py" \
#   ./run-build.sh
#
# The Job itself is fire and forget: WAIT=0 returns as soon as it is created,
# and the logs and summary stay on the work volume under /work/logs/$RUN_ID.
set -euo pipefail
# shellcheck source=common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

RUN_ID="${RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}"
PATCH_SETS="${PATCH_SETS:-master}"
FRESH_CLONE="${FRESH_CLONE:-0}"
APPLY_PATCHES="${APPLY_PATCHES:-1}"
BUILD_JOBS="${BUILD_JOBS:-32}"
USE_CCACHE="${USE_CCACHE:-1}"
EXTRA_CMAKE_ARGS="${EXTRA_CMAKE_ARGS:-}"
RUN_UNIT_TESTS="${RUN_UNIT_TESTS:-1}"
UNIT_TEST_ARGS="${UNIT_TEST_ARGS:---report_level=detailed}"
FUNCTIONAL_TESTS="${FUNCTIONAL_TESTS:-feature_p2mr.py feature_p2mr_signet.py p2p_segwit.py}"
RUN_DEFAULT_FUNCTIONAL="${RUN_DEFAULT_FUNCTIONAL:-0}"
FUNCTIONAL_JOBS="${FUNCTIONAL_JOBS:-16}"
FUNCTIONAL_EXTRA_ARGS="${FUNCTIONAL_EXTRA_ARGS:-}"
CPU="${CPU:-32}"
MEMORY="${MEMORY:-48Gi}"
WAIT="${WAIT:-1}"
JOB="p2mr-build-$RUN_ID"

BUILDER_REF="$(builder_image_ref)" \
    || die "could not resolve the builder image reference"
[ -n "$BUILDER_REF" ] || die "the builder image reference resolved to nothing"
BUILDER_REF_CANONICAL="$(canonical_image_ref "$BUILDER_REF")"

ensure_workspace

# The Job pulls nothing. build.yaml sets `imagePullPolicy: IfNotPresent` and
# there is no registry behind this cluster, so a reference containerd does not
# have is a Job that sits in ImagePullBackOff until somebody reads its events.
# Saying it here costs one listing.
#
# A listing that cannot be read is not the same thing as a listing without the
# reference in it, and only the second is an error: this reads containerd
# directly and so wants root on the node, while starting a Job does not. A run
# that cannot look still goes ahead, because the failure it risks is the
# visible one it was going to get anyway.
require_builder_image() {
    local images rc=0
    images="$($CTR images ls 2>&1)" || rc=$?
    if [ "$rc" != "0" ]; then
        log "could not list containerd images, so $BUILDER_REF was not confirmed present"
        return 0
    fi
    if awk -v r="$BUILDER_REF_CANONICAL" '$1 == r { f = 1 } END { exit !f }' <<< "$images"; then
        return 0
    fi
    die "$BUILDER_REF is not in containerd, and the Job cannot pull it." \
        "Build it with: KUBECTL=\"$KUBECTL\" $HERE/build-image.sh builder." \
        "That prints the reference it wrote; if it was published under" \
        "IMAGE_TAG_SUFFIX, export BUILDER_IMAGE_REF to that reference."
}
require_builder_image
log "builder image $BUILDER_REF"

log "publishing build.sh as the ConfigMap p2mr-build-scripts"
$KUBECTL -n "$NS" create configmap p2mr-build-scripts \
    --from-file=build.sh="$HERE/build.sh" \
    --dry-run=client -o yaml | $KUBECTL apply -f - > /dev/null

log "starting $JOB (patch sets: $PATCH_SETS)"
sed -e "s|__RUN_ID__|$RUN_ID|g" \
    -e "s|__BUILDER_IMAGE__|$BUILDER_REF|g" \
    -e "s|__PATCH_SETS__|$PATCH_SETS|g" \
    -e "s|__FRESH_CLONE__|$FRESH_CLONE|g" \
    -e "s|__APPLY_PATCHES__|$APPLY_PATCHES|g" \
    -e "s|__BUILD_JOBS__|$BUILD_JOBS|g" \
    -e "s|__USE_CCACHE__|$USE_CCACHE|g" \
    -e "s|__EXTRA_CMAKE_ARGS__|$EXTRA_CMAKE_ARGS|g" \
    -e "s|__RUN_UNIT_TESTS__|$RUN_UNIT_TESTS|g" \
    -e "s|__UNIT_TEST_ARGS__|$UNIT_TEST_ARGS|g" \
    -e "s|__FUNCTIONAL_TESTS__|$FUNCTIONAL_TESTS|g" \
    -e "s|__RUN_DEFAULT_FUNCTIONAL__|$RUN_DEFAULT_FUNCTIONAL|g" \
    -e "s|__FUNCTIONAL_JOBS__|$FUNCTIONAL_JOBS|g" \
    -e "s|__FUNCTIONAL_EXTRA_ARGS__|$FUNCTIONAL_EXTRA_ARGS|g" \
    -e "s|__CPU__|$CPU|g" \
    -e "s|__MEMORY__|$MEMORY|g" \
    "$HERE/build.yaml" | $KUBECTL apply -f - > /dev/null

if [ "$WAIT" != "1" ]; then
    log "started; follow it with: $KUBECTL -n $NS logs -f job/$JOB"
    exit 0
fi

log "waiting for $JOB (a full run takes a couple of hours)"
rc=0
while :; do
    succeeded="$($KUBECTL -n "$NS" get job "$JOB" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)"
    failed="$($KUBECTL -n "$NS" get job "$JOB" -o jsonpath='{.status.failed}' 2>/dev/null || true)"
    if [ "${succeeded:-0}" -ge 1 ]; then
        break
    fi
    if [ "${failed:-0}" -ge 1 ]; then
        rc=1
        break
    fi
    printf '    %s  %s\n' "$(date -u +%H:%M:%S)" \
        "$($KUBECTL -n "$NS" logs "job/$JOB" --tail=1 2> /dev/null | tr -d '\r' | cut -c1-100)"
    sleep 60
done

echo
log "log tail"
$KUBECTL -n "$NS" logs "job/$JOB" --tail=60 || true
echo
log "summary (/work/logs/$RUN_ID/summary.txt)"
in_shell cat "/work/logs/$RUN_ID/summary.txt" || true

if [ "$rc" = "0" ]; then
    log "$JOB succeeded"
else
    log "$JOB failed; the full logs are on the volume under /work/logs/$RUN_ID"
fi
exit "$rc"

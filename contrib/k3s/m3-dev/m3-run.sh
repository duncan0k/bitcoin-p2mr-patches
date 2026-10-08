#!/usr/bin/env bash
#
# Start one M3 development build in the p2mr-build namespace, from contrib/k3s/m3-dev in a checkout
# of this repository on the build host.
#
#   RUN_ID=m3-<name> [PATCH_SETS=...] [UNIT_TEST_ARGS=...] [FUNCTIONAL_TESTS=...] ./m3-run.sh
#
# Copies the repository's patches-m3/ and SHA256SUMS-m3 to /work/m3-patches, a directory of its own:
# /work/patches/m3 is the release pipeline's copy (../seed.sh). The earlier sets are read from
# /work/patches as that pipeline seeds them, and the Job runs on its builder image (builder_image_ref
# in ../common.sh; BUILDER_IMAGE_REF overrides it). Publishes m3-build.sh as the
# ConfigMap m3-build-scripts, waits while the minute's last digit is 4 or 5 (the Ark-0 observe
# CronJob runs at minute 5 of every ten, 5-59/10), and creates the Job m3-build-$RUN_ID. Touches
# no other directory on the volume and nothing outside the p2mr-build namespace.
set -euo pipefail
# shellcheck source=../common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../common.sh"
cd "$(dirname "$0")"
K="sudo -n k3s kubectl -n p2mr-build"

RUN_ID="${RUN_ID:?RUN_ID is required}"
case "$RUN_ID" in m3-*) ;; *) die "RUN_ID must start with m3-";; esac
PATCH_SETS="${PATCH_SETS:-master m05 spacing m1 m2 m3}"
BUILD_JOBS="${BUILD_JOBS:-8}"
CPU="${CPU:-8}"
MEMORY="${MEMORY:-20Gi}"
EXTRA_CMAKE_ARGS="${EXTRA_CMAKE_ARGS:-}"
RUN_UNIT_TESTS="${RUN_UNIT_TESTS:-1}"
UNIT_TEST_ARGS="${UNIT_TEST_ARGS:---report_level=detailed}"
FUNCTIONAL_TESTS="${FUNCTIONAL_TESTS:-}"
RUN_DEFAULT_FUNCTIONAL="${RUN_DEFAULT_FUNCTIONAL:-0}"
FUNCTIONAL_JOBS="${FUNCTIONAL_JOBS:-8}"
FUNCTIONAL_EXTRA_ARGS="${FUNCTIONAL_EXTRA_ARGS:-}"
M3_PATCH_LIMIT="${M3_PATCH_LIMIT:-}"
FUZZ_TARGET="${FUZZ_TARGET:-}"
FUZZ_INPUTS="${FUZZ_INPUTS:-}"
M3_IMGCTX="${M3_IMGCTX:-0}"
[ "$BUILD_JOBS" -le 10 ] && [ "$CPU" -le 10 ] || die "BUILD_JOBS and CPU must not exceed 10"
[ "$FUNCTIONAL_JOBS" -le 10 ] || die "FUNCTIONAL_JOBS must not exceed 10"

if $K get job "m3-build-$RUN_ID" > /dev/null 2>&1; then die "job m3-build-$RUN_ID exists already"; fi

BUILDER_REF="$(builder_image_ref)" || die "could not resolve the builder image reference"
[ -n "$BUILDER_REF" ] || die "the builder image reference resolved to nothing"
echo "==> builder image $BUILDER_REF"

echo "==> checking patches-m3 and copying it to /work/m3-patches"
(cd "$REPO_ROOT/patches-m3" && sha256sum -c --quiet ../SHA256SUMS-m3) || die "patches-m3 does not match SHA256SUMS-m3"
$K exec p2mr-shell -- sh -c "rm -rf /work/m3-patches && mkdir -p /work/m3-patches"
tar -C "$REPO_ROOT/patches-m3" -cf - . | $K exec -i p2mr-shell -- tar -C /work/m3-patches -xf -
tar -C "$REPO_ROOT" -cf - SHA256SUMS-m3 | $K exec -i p2mr-shell -- tar -C /work/m3-patches -xf -
$K exec p2mr-shell -- sh -c "cd /work/m3-patches && sha256sum -c --quiet SHA256SUMS-m3 && ls *.patch | wc -l"

fuzz_dir=""
if [ -n "$FUZZ_INPUTS" ]; then
    [ -n "$FUZZ_TARGET" ] && [ -d "$FUZZ_INPUTS" ] || die "FUZZ_INPUTS must be a directory here, and FUZZ_TARGET set"
    fuzz_dir="/work/m3-fuzz-inputs/$RUN_ID"
    echo "==> copying $FUZZ_INPUTS to $fuzz_dir"
    $K exec p2mr-shell -- sh -c "rm -rf $fuzz_dir && mkdir -p $fuzz_dir"
    tar -C "$FUZZ_INPUTS" -cf - . | $K exec -i p2mr-shell -- tar -C "$fuzz_dir" -xf -
fi

echo "==> publishing m3-build.sh as the ConfigMap m3-build-scripts"
$K create configmap m3-build-scripts --from-file=m3-build.sh=./m3-build.sh --dry-run=client -o yaml | $K apply -f - > /dev/null

# The Ark-0 observe CronJob runs at minute 5 of every ten (5-59/10): no pod starts in that minute
# or the one before it.
while :; do m=$(( 10#$(date -u +%M) % 10 )); [ "$m" -ne 4 ] && [ "$m" -ne 5 ] && break; sleep 20; done
echo "==> starting m3-build-$RUN_ID at $(date -u +%H:%M:%SZ)"
sed -e "s|__RUN_ID__|$RUN_ID|g" \
    -e "s|__BUILDER_IMAGE__|$BUILDER_REF|g" \
    -e "s|__PATCH_SETS__|$PATCH_SETS|g" \
    -e "s|__BUILD_JOBS__|$BUILD_JOBS|g" \
    -e "s|__EXTRA_CMAKE_ARGS__|$EXTRA_CMAKE_ARGS|g" \
    -e "s|__RUN_UNIT_TESTS__|$RUN_UNIT_TESTS|g" \
    -e "s|__UNIT_TEST_ARGS__|$UNIT_TEST_ARGS|g" \
    -e "s|__FUNCTIONAL_TESTS__|$FUNCTIONAL_TESTS|g" \
    -e "s|__RUN_DEFAULT_FUNCTIONAL__|$RUN_DEFAULT_FUNCTIONAL|g" \
    -e "s|__FUNCTIONAL_JOBS__|$FUNCTIONAL_JOBS|g" \
    -e "s|__FUNCTIONAL_EXTRA_ARGS__|$FUNCTIONAL_EXTRA_ARGS|g" \
    -e "s|__M3_PATCH_LIMIT__|$M3_PATCH_LIMIT|g" \
    -e "s|__FUZZ_TARGET__|$FUZZ_TARGET|g" \
    -e "s|__FUZZ_INPUTS__|$fuzz_dir|g" \
    -e "s|__M3_IMGCTX__|$M3_IMGCTX|g" \
    -e "s|__CPU__|$CPU|g" \
    -e "s|__MEMORY__|$MEMORY|g" \
    m3-build.yaml | $K apply -f - > /dev/null
echo "started"

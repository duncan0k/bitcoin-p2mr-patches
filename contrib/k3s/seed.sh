#!/usr/bin/env bash
#
# Create the namespace, the work volume and the helper pod, then copy the patch
# series and the two Dockerfiles onto the volume.
#
# Run it on a machine that can reach the cluster, from a checkout of this
# repository. Nothing is left behind outside the cluster.
#
#   KUBECTL="sudo k3s kubectl" ./seed.sh
#
# Re-running is safe and is how an updated patch series is published: the patch
# directories are replaced, the source tree, ccache and logs are untouched.
set -euo pipefail
# shellcheck source=common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

ensure_workspace

log "clearing the patch directories"
in_shell rm -rf /work/patches/master /work/patches/m05 /work/patches/spacing /work/patches/m1 /work/patches/m2 /work/patches/m2a /work/patches/m2b

log "copying the consensus series (patches/) to /work/patches/master"
push_dir "$REPO_ROOT/patches" /work/patches/master
push_file "$REPO_ROOT/SHA256SUMS" /work/patches/master

if [ -d "$REPO_ROOT/patches-m05" ]; then
    log "copying the M0.5 series (patches-m05/) to /work/patches/m05"
    push_dir "$REPO_ROOT/patches-m05" /work/patches/m05
    push_file "$REPO_ROOT/SHA256SUMS-m05" /work/patches/m05
else
    log "patches-m05/ is absent on this branch, skipping the M0.5 series"
fi

if [ -d "$REPO_ROOT/patches-spacing" ]; then
    log "copying the retarget spacing patch (patches-spacing/) to /work/patches/spacing"
    push_dir "$REPO_ROOT/patches-spacing" /work/patches/spacing
    push_file "$REPO_ROOT/SHA256SUMS-spacing" /work/patches/spacing
else
    log "patches-spacing/ is absent on this branch, skipping it"
fi

if [ -d "$REPO_ROOT/patches-m1" ]; then
    log "copying the M1 series (patches-m1/) to /work/patches/m1"
    push_dir "$REPO_ROOT/patches-m1" /work/patches/m1
    push_file "$REPO_ROOT/SHA256SUMS-m1" /work/patches/m1
else
    log "patches-m1/ is absent on this branch, skipping the M1 series"
fi

# M2: m2 is the series Ark-0 runs (-signetpqblock, and the rule fixed for
# Ark-0), on m1. m2a and m2b are the two candidates it was chosen from: m2a
# (-signetpqblock) on m1, m2b (-signetpqchallenge) on m2a.
for set_name in m2 m2a m2b; do
    if [ -d "$REPO_ROOT/patches-$set_name" ]; then
        log "copying the M2 series (patches-$set_name/) to /work/patches/$set_name"
        push_dir "$REPO_ROOT/patches-$set_name" "/work/patches/$set_name"
        push_file "$REPO_ROOT/SHA256SUMS-$set_name" "/work/patches/$set_name"
    else
        log "patches-$set_name/ is absent on this branch, skipping it"
    fi
done

log "copying the image build contexts"
push_file "$HERE/Dockerfile.builder" /work/imgctx/builder
push_file "$HERE/Dockerfile.node" /work/imgctx/node
in_shell mkdir -p /work/imgctx/node/bin /work/img

log "verifying the checksums on the volume"
for set_name in master m05 spacing m1 m2 m2a m2b; do
    if in_shell test -d "/work/patches/$set_name"; then
        # sha256sum's own exit status, on its own, with nothing downstream of
        # it. The previous version ended in `| tail -3`, so the pipeline
        # reported tail's success whatever sha256sum had found, and the inner
        # `sh` does not inherit this script's `pipefail` either. A patch that
        # arrived corrupted would have been announced as verified.
        #
        # --quiet prints nothing for a file that matches and the file name for
        # one that does not, so a failure names itself before this stops.
        in_shell sh -c "cd /work/patches/$set_name && sha256sum -c --quiet SHA256SUMS*" \
            || die "checksums do not match in /work/patches/$set_name"
        count="$(in_shell sh -c "ls /work/patches/$set_name/*.patch | wc -l" | tr -d '\r[:space:]')"
        log "  $set_name: $count patches, checksums verified"
    fi
done

log "seeded"
in_shell sh -c 'ls -la /work; echo; du -sh /work/patches/* 2>/dev/null'

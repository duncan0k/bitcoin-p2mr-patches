#!/usr/bin/env bash
#
# Shared settings and helpers for the host-side scripts in this directory.
# Sourced, never executed.
#
#   KUBECTL   how to reach the cluster. On a k3s node without a kubeconfig in
#             the invoking user's home, export KUBECTL="sudo k3s kubectl".
#   CTR       how to reach containerd, for importing image tarballs.
#   NS        the namespace everything lives in.

KUBECTL="${KUBECTL:-kubectl}"
CTR="${CTR:-sudo k3s ctr}"
NS="${NS:-p2mr-build}"
SHELL_POD="${SHELL_POD:-p2mr-shell}"
PVC="${PVC:-p2mr-work}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# contrib/k3s/../.. is the repository root, which holds patches/ and patches-m05/.
REPO_ROOT="${REPO_ROOT:-$(cd "$HERE/../.." && pwd)}"

# Name and series only. Every image this tooling writes carries an immutable
# suffix on top of these; see build-image.sh, and builder_image_ref below.
BUILDER_IMAGE="${BUILDER_IMAGE:-p2mr-builder:v31.1}"
MINER_IMAGE="${MINER_IMAGE:-p2mr-miner:v31.1-p2mr}"

# There is no single node image. The two nodes deliberately run different
# builds -- node A the ten consensus patches, node B those plus the twenty-four
# M0.5 wallet patches, each with the retarget spacing patch on top since
# 2026-09-22 -- so one tag cannot name both, and the default used to
# name neither: it was p2mr-node:v31.1-p2mr while the manifests asked for
# -m0 and -m05, so following the documented commands produced an image no
# workload would ever pull.
#
# The suffix is therefore derived from what was actually built and tested,
# rather than typed. build.sh records the patch sets in PROVENANCE.txt beside
# the binaries, and build-image.sh looks the tag up from there. Tagging an
# M0.5 build as -m0 would put wallet code on the block producing node; making
# the tag a consequence of the build rather than of a command line is what
# stops that being one typo away.
NODE_IMAGE_BASE="${NODE_IMAGE_BASE:-p2mr-node:v31.1-p2mr}"

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# An image reference as containerd writes it in `ctr images ls`, which is
# always fully qualified and always normalised. Everything here names images
# the short way, `p2mr-node:v31.1-p2mr-m0-<head12>`, and containerd calls that
# `docker.io/library/p2mr-node:v31.1-p2mr-m0-<head12>`, so a lookup has to
# normalise before it compares, and the temporary import name and its cleanup
# have to use the same answer as the lookup.
#
# Two earlier versions were wrong in opposite directions. The first prepended
# `docker.io/library/` unconditionally, so a BUILDER_IMAGE_REF that was already
# qualified became `docker.io/library/docker.io/library/...` and read as
# missing. The second kept any recognised registry exactly as given, which is
# right for a private registry and wrong for Docker Hub: `docker.io/p2mr-node`
# is a reference containerd stores as `docker.io/library/p2mr-node`, so it read
# as missing too, and an import named that way would have been looked up and
# cleaned up under a name that does not exist.
#
# So this is Docker's normalisation, the same three rules containerd applies to
# an archive reference (distribution/reference normalize.go, containerd
# core/images/archive/reference.go):
#
#   1. The part before the first slash is the registry only if it contains a
#      dot or a colon, is exactly `localhost`, or has an upper-case letter --
#      repository path components are lower-case, so an upper-case first
#      component cannot be one. Anything else, including a reference with no
#      slash at all, means the registry is `docker.io` and the whole string is
#      the repository.
#   2. `index.docker.io` is the old name for `docker.io`.
#   3. Under `docker.io` only, a repository with no slash in it sits in the
#      implicit `library` namespace and gets it written out.
#
# Everything else is left alone: other registries keep their host, their port
# if they have one, and their path, and a `@sha256:...` digest is part of the
# repository string and is never touched. `build-image.sh --self-test` runs
# the table in self_test_canonical_image_ref below against all of this.
canonical_image_ref() {
    local ref="$1" domain remainder
    case "$ref" in
        */*) domain="${ref%%/*}"; remainder="${ref#*/}" ;;
        *)   domain="";           remainder="$ref"      ;;
    esac
    # Rule 1: is that first component a registry at all?
    case "$domain" in
        ""|localhost|*.*|*:*|*[A-Z]*) ;;
        *) remainder="$domain/$remainder"; domain="" ;;
    esac
    [ -n "$domain" ] || domain="docker.io"
    # Rule 2.
    [ "$domain" != "index.docker.io" ] || domain="docker.io"
    # Rule 3.
    if [ "$domain" = "docker.io" ]; then
        case "$remainder" in
            */*) ;;
            *)   remainder="library/$remainder" ;;
        esac
    fi
    printf '%s/%s\n' "$domain" "$remainder"
}

# The table canonical_image_ref is expected to satisfy, as input and output
# pairs. Run by `build-image.sh --self-test`, which reaches no cluster, no
# containerd and no volume, so it is safe anywhere this file can be read.
self_test_canonical_image_ref() {
    local failed=0 input expected got
    while read -r input expected; do
        [ -n "$input" ] || continue
        got="$(canonical_image_ref "$input")"
        if [ "$got" = "$expected" ]; then
            printf '    ok    %-48s -> %s\n' "$input" "$got"
        else
            printf '    FAIL  %-48s -> %s (expected %s)\n' "$input" "$got" "$expected"
            failed=1
        fi
    done <<'TABLE'
p2mr-node:v31.1-p2mr-m0-0123456789ab docker.io/library/p2mr-node:v31.1-p2mr-m0-0123456789ab
p2mr-builder:v31.1 docker.io/library/p2mr-builder:v31.1
p2mr-miner:import-0011223344556677 docker.io/library/p2mr-miner:import-0011223344556677
docker.io/p2mr-builder:v31.1-0123456789ab docker.io/library/p2mr-builder:v31.1-0123456789ab
docker.io/library/p2mr-builder:v31.1-0123456789ab docker.io/library/p2mr-builder:v31.1-0123456789ab
index.docker.io/p2mr-miner:v31.1-p2mr docker.io/library/p2mr-miner:v31.1-p2mr
index.docker.io/library/p2mr-miner:v31.1-p2mr docker.io/library/p2mr-miner:v31.1-p2mr
rancher/mirrored-pause:3.6 docker.io/rancher/mirrored-pause:3.6
docker.io/rancher/mirrored-pause:3.6 docker.io/rancher/mirrored-pause:3.6
localhost/p2mr-node:v31.1 localhost/p2mr-node:v31.1
localhost:5000/p2mr-node:v31.1 localhost:5000/p2mr-node:v31.1
registry.example.com/team/p2mr-node:v31.1 registry.example.com/team/p2mr-node:v31.1
registry.example.com:5000/team/p2mr-node:v31.1 registry.example.com:5000/team/p2mr-node:v31.1
p2mr-node@sha256:0000000000000000000000000000000000000000000000000000000000000000 docker.io/library/p2mr-node@sha256:0000000000000000000000000000000000000000000000000000000000000000
docker.io/p2mr-node@sha256:0000000000000000000000000000000000000000000000000000000000000000 docker.io/library/p2mr-node@sha256:0000000000000000000000000000000000000000000000000000000000000000
TABLE
    return "$failed"
}

# Patch-set list, exactly as build.sh recorded it, to the node image tag the
# manifests expect. An unknown list is an error rather than a guess: a new
# series needs a tag and a manifest, and silently reusing one of these would
# mislabel it.
node_image_for_patch_sets() {
    case "$1" in
        "master")             printf '%s-m0\n'          "$NODE_IMAGE_BASE" ;;
        "master m05")         printf '%s-m05\n'         "$NODE_IMAGE_BASE" ;;
        "master spacing")     printf '%s-m0-spacing\n'  "$NODE_IMAGE_BASE" ;;
        "master m05 spacing") printf '%s-m05-spacing\n' "$NODE_IMAGE_BASE" ;;
        *) die "no node image tag is defined for patch sets '$1';" \
               "add one to node_image_for_patch_sets in common.sh" ;;
    esac
}

# The builder image's immutable reference, resolved the same way by the script
# that writes it and the script that runs a Job with it.
#
# Those two used to disagree. build-image.sh produced
# p2mr-builder:v31.1-<hash12> while run-build.sh asked for p2mr-builder:v31.1,
# so a fresh installation could not use the builder it had just made, and an
# installation that still had the old shared tag silently kept building with
# it. A name computed in one place and typed in another is a name that drifts,
# so it is computed once here and read from here by both.
#
# The builder has no build to be the provenance of: it is Ubuntu plus a package
# list, built from Dockerfile.builder in this repository. That file is
# therefore what identifies it, and the suffix is the first twelve characters
# of its hash. Editing the Dockerfile produces a different reference, which is
# the point; the previous builder keeps its own name and keeps working.
#
# BUILDER_IMAGE_REF overrides the result, for a builder published under
# IMAGE_TAG_SUFFIX. build-image.sh prints the reference it wrote and says so.
builder_suffix() {
    local h
    h="$(sha256sum "$HERE/Dockerfile.builder" | cut -d' ' -f1)" \
        || die "could not hash $HERE/Dockerfile.builder"
    [ "${#h}" -ge 12 ] || die "could not hash $HERE/Dockerfile.builder"
    printf '%s\n' "${h:0:12}"
}

builder_image_ref() {
    local suffix
    if [ -n "${BUILDER_IMAGE_REF:-}" ]; then
        printf '%s\n' "$BUILDER_IMAGE_REF"
        return 0
    fi
    # Separately, and checked. `printf '%s-%s' "$BUILDER_IMAGE"
    # "$(builder_suffix)"` reports success whatever the substitution did:
    # builder_suffix calls die, which exits its own subshell and not this
    # function, so an unreadable Dockerfile produced the reference
    # "p2mr-builder:v31.1-" and a zero exit status. Paired with a containerd
    # listing that also could not run, run-build.sh would then have started a
    # Job under a name nothing could have built.
    suffix="$(builder_suffix)" || return 1
    [ -n "$suffix" ] || return 1
    printf '%s-%s\n' "$BUILDER_IMAGE" "$suffix"
}

# Bring up the namespace, the volume and the helper pod, and wait until the
# pod is running. local-path is WaitForFirstConsumer, so the volume is only
# created once this pod is scheduled.
ensure_workspace() {
    $KUBECTL apply -f "$HERE/namespace.yaml" > /dev/null
    $KUBECTL apply -f "$HERE/pvc.yaml" > /dev/null
    $KUBECTL apply -f "$HERE/shell.yaml" > /dev/null
    log "waiting for $SHELL_POD"
    $KUBECTL -n "$NS" wait --for=condition=Ready "pod/$SHELL_POD" --timeout=300s > /dev/null \
        || die "$SHELL_POD did not become ready"
}

# Run a command in the helper pod.
in_shell() { $KUBECTL -n "$NS" exec "$SHELL_POD" -- "$@"; }

# Stream a local directory's contents into a directory on the work volume.
# `kubectl cp` has changed its behaviour for directory destinations between
# releases; a tar pipe is unambiguous in every version.
push_dir() {
    local src="$1" dest="$2"
    [ -d "$src" ] || die "no such directory: $src"
    in_shell mkdir -p "$dest"
    tar -C "$src" -cf - . | $KUBECTL -n "$NS" exec -i "$SHELL_POD" -- tar -C "$dest" -xf -
}

# Stream a single local file into a directory on the work volume.
push_file() {
    local src="$1" dest="$2"
    [ -f "$src" ] || die "no such file: $src"
    in_shell mkdir -p "$dest"
    tar -C "$(dirname "$src")" -cf - "$(basename "$src")" \
        | $KUBECTL -n "$NS" exec -i "$SHELL_POD" -- tar -C "$dest" -xf -
}

# Absolute path of the work volume on the node, so that the host can read the
# image tarballs kaniko writes. local-path PVs carry it in .spec.local.path;
# older provisioner versions used .spec.hostPath.path.
pvc_host_path() {
    local pv path
    pv="$($KUBECTL -n "$NS" get pvc "$PVC" -o jsonpath='{.spec.volumeName}')"
    [ -n "$pv" ] || die "PVC $PVC is not bound yet"
    path="$($KUBECTL get pv "$pv" -o jsonpath='{.spec.local.path}')"
    [ -n "$path" ] || path="$($KUBECTL get pv "$pv" -o jsonpath='{.spec.hostPath.path}')"
    [ -n "$path" ] || die "could not resolve the node path of $pv"
    printf '%s\n' "$path"
}

#!/usr/bin/env bash
#
# Build one of the two images with kaniko and import it into the node's
# containerd. There is no Docker daemon and no registry, so kaniko writes a
# tarball onto the work volume and `ctr images import` reads it from the node
# path of that volume.
#
#   KUBECTL="sudo k3s kubectl" ./build-image.sh builder
#   KUBECTL="sudo k3s kubectl" ./build-image.sh node
#   KUBECTL="sudo k3s kubectl" ./build-image.sh miner
#   ./build-image.sh --self-test
#
# The builder image is self-contained. The node image needs the binaries of a
# successful build: build.sh stages them at /work/imgctx/node/bin, so run it
# after a build Job has finished.
#
# --self-test checks the image reference normalisation in common.sh against its
# table and exits. It reaches no cluster and changes nothing, so it needs
# neither KUBECTL nor root.
set -euo pipefail
# shellcheck source=common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

TARGET="${1:-}"

# BASE_IMAGE is the name and series; the immutable suffix is added below, once
# the provenance that names the source has been read.
case "$TARGET" in
    --self-test)
        log "canonical_image_ref"
        self_test_canonical_image_ref || die "the reference normalisation table does not hold"
        log "self-test passed"
        exit 0
        ;;
    builder)
        BASE_IMAGE="$BUILDER_IMAGE"
        CONTEXT_SUBPATH="imgctx/builder"
        DOCKERFILE="Dockerfile.builder"
        ;;
    node)
        # Filled in below from the provenance of the build being packaged.
        BASE_IMAGE=""
        CONTEXT_SUBPATH="imgctx/node"
        DOCKERFILE="Dockerfile.node"
        ;;
    miner)
        BASE_IMAGE="$MINER_IMAGE"
        CONTEXT_SUBPATH="imgctx/miner"
        DOCKERFILE="Dockerfile.miner"
        ;;
    *)
        die "usage: $0 builder|node|miner|--self-test"
        ;;
esac

POD="kaniko-$TARGET"
TAR_NAME="p2mr-$TARGET.tar"

ensure_workspace

# Both runtime images are made out of binaries, and a binary on its own says
# nothing about whether it passed anything. build.sh publishes a context only
# after every check it was asked to run has passed, and writes PROVENANCE.txt
# beside the binaries recording the patch sets, the hash of the applied commit
# list and the check results. Requiring that file here is what stops a failed
# build from being turned into a runtime image: presence of the file means a
# passing run put it there, and the summary is printed so whoever runs this
# sees which patch series is about to become an image.
require_provenance() {
    local dir="$1" prov="$1/PROVENANCE.txt"
    in_shell test -f "$prov" \
        || die "$prov is missing: $dir was not published by a passing build (see build.sh)"
    in_shell grep -q '^result *: *pass' "$prov" \
        || die "$prov does not record a passing run"
    log "provenance of $dir"
    in_shell grep -E '^(run id|patch sets|series verified|commits over tag|patches applied|head|commit list sha256) ' "$prov" \
        | sed 's/^/    /'
}

# One field out of a provenance file, with the label, the padding and any
# trailing whitespace removed. The labels are padded to a column, so a long one
# can end up flush against its colon with no space at all; ' *:' matches both.
provenance_field() {
    in_shell sh -c "grep '^$2 *:' '$1/PROVENANCE.txt' | head -1 | sed 's/^[^:]*: *//; s/[[:space:]]*\$//'"
}

# What a set of binaries has to prove before it may become a runtime image.
#
# Three separate things, and all three are required for both runtime targets:
#
#   result           : pass          the run that produced them passed
#   series verified  : yes           the source was matched to a known series
#   head             : <commit>      and this is the commit it was matched at
#
# The first says the binaries are not from a failed run. The second is the one
# that used to be skippable, and it is the one that matters most: build.sh
# compares the patch-ids of the commits above the base tag against the patch
# files, so 'yes' means the source really was that series rather than merely
# having been asked for it. Without it, a tree left over from an earlier run
# can be published under whatever label was the default that day. The third
# names the build, and is required even when it does not name the tag.
#
# Naming overrides change what the image is called. They do not change what has
# to be true before it is called anything, and each one logs the identity it is
# departing from. The builder is the documented exception: it is built from a
# Dockerfile in this repository, there is no build for it to be the provenance
# of, and it is handled on its own below.
PROV_HEAD=""
require_verified_provenance() {
    local dir="$1" series
    require_provenance "$dir"
    series="$(provenance_field "$dir" 'series verified')"
    [ "$series" = "yes" ] \
        || die "$dir/PROVENANCE.txt records 'series verified: ${series:-missing}'." \
               "The built source was not matched to a known patch set, so nothing here" \
               "knows what these binaries are. Re-run the build against a tree it can" \
               "identify. NODE_IMAGE and IMAGE_TAG_SUFFIX rename a build; they cannot" \
               "stand in for identifying one."
    PROV_HEAD="$(provenance_field "$dir" 'head')"
    [ -n "$PROV_HEAD" ] \
        || die "$dir/PROVENANCE.txt records no head commit, so this build has no identity"
    # A commit id, not merely something long enough. Checking the length alone
    # accepted `not-a-commit` and would have written it into a tag, which is
    # the opposite of a name that identifies a build. The whole field has to be
    # hexadecimal -- `*[!0-9a-fA-F]*` matches the value and not a line inside
    # it, so there is no anchoring to get wrong -- and its length has to be one
    # of the two a git object id has.
    case "$PROV_HEAD" in
        *[!0-9a-fA-F]*)
            die "the head in $dir/PROVENANCE.txt is not a hexadecimal commit id" ;;
    esac
    case "${#PROV_HEAD}" in
        40|64) ;;
        *) die "the head in $dir/PROVENANCE.txt is ${#PROV_HEAD} characters;" \
               "a commit id is 40 (sha1) or 64 (sha256)" ;;
    esac
}

if [ "$TARGET" = "node" ]; then
    in_shell test -x /work/imgctx/node/bin/bitcoind \
        || die "/work/imgctx/node/bin/bitcoind is missing; run a build Job first"
    require_verified_provenance /work/imgctx/node
    # The tag follows the build. Node A and node B run deliberately different
    # series, so the name has to come from what was compiled and tested rather
    # than from whoever is typing.
    PATCH_SETS_BUILT="$(provenance_field /work/imgctx/node 'patch sets verified')"
    if [ -n "${NODE_IMAGE:-}" ]; then
        BASE_IMAGE="$NODE_IMAGE"
        log "NODE_IMAGE is set: naming this $BASE_IMAGE rather than the tag for" \
            "verified patch sets '${PATCH_SETS_BUILT:-unrecorded}'"
    else
        [ -n "$PATCH_SETS_BUILT" ] || die "PROVENANCE.txt records no verified patch sets"
        BASE_IMAGE="$(node_image_for_patch_sets "$PATCH_SETS_BUILT")"
        log "verified patch sets '$PATCH_SETS_BUILT' -> $BASE_IMAGE"
    fi
fi
if [ "$TARGET" = "miner" ]; then
    in_shell test -f /work/imgctx/miner/demo/ark0.py \
        || die "/work/imgctx/miner is not staged; run ./stage-miner.sh first"
    # stage-miner.sh copies the provenance across from the published binaries it
    # took the CLI and the utility from, so the producer image is traceable to
    # the same build as a node image, and is held to the same three conditions.
    require_verified_provenance /work/imgctx/miner
fi

# An image this script imports is never replaced. Every build produces its own
# tag, and a tag, once it names content, keeps naming it.
#
# The problem this replaces: `ctr images import` moves a tag onto new content
# in place, so rebuilding a tag a workload uses takes away the only name its
# current image had. That happened on 2026-09-16 and cost the roll its way
# back. The answer here was a guard that asked the cluster which images were in
# use and preserved those first, and it was wrong four times in four reviews:
# it compared config digests with manifest digests, it mis-normalised registry
# references, it skipped pods by phase and missed containers running inside
# Pending ones, and each repair added another lookup that could fail quietly.
# The question "what is running right now" has no small, complete answer, and
# every incomplete answer was a way to lose an image.
#
# So the question is not asked. The tag carries the identity of the build:
#
#   p2mr-node:v31.1-p2mr-m05-<head12>
#
# where the suffix is the first twelve characters of the commit the provenance
# records, which is the source that was compiled and tested. Two builds of
# different source get different tags and cannot collide. Two builds of the
# same source get the same tag, and then one of two things is true: the content
# is identical, which is a no-op worth reporting, or it is not, which is worth
# stopping for. Nothing is ever moved, so nothing needs preserving, so nothing
# needs to know what is running.
#
# Rolling a workload is then a manifest change rather than a rebuild: the
# `images:` block in ark0/kustomization.yaml names the tag each workload runs,
# and rolling back is setting it to the tag recorded in the roll table.
IMAGE_TAG_SUFFIX="${IMAGE_TAG_SUFFIX:-}"
CTR_IMAGES=""

load_ctr_images() {
    local rc=0
    CTR_IMAGES="$($CTR images ls 2>&1)" || rc=$?
    [ "$rc" = "0" ] \
        || die "could not list containerd images ($CTR_IMAGES); refusing to import without knowing what is there"
}

# The manifest digest containerd has for one reference, or nothing. Reads to
# end of input: a consumer that stops early takes SIGPIPE and, under pipefail,
# turns a found answer into a failure.
ctr_digest_of() {
    awk -v r="$1" '$1 == r && !f { d = $3; f = 1 } END { if (f) print d }' <<< "$CTR_IMAGES"
}

# First twelve characters of a sha256, with or without the prefix.
short12() {
    local v="${1#sha256:}"
    printf '%s\n' "${v:0:12}"
}

# The suffix that makes the tag immutable.
#
# For the two runtime images it is the commit the provenance records, which is
# the source that was compiled and tested: same source, same tag. The builder
# has no provenance, so it takes the hash of the Dockerfile it is built from,
# which is the only input this script knows about. IMAGE_TAG_SUFFIX overrides
# either, for a deliberate rebuild of unchanged inputs whose result is expected
# to differ -- the apt repositories move, and README.md says so.
#
# The override is a rename and nothing more. For a runtime target the identity
# checks above have already run and the head is already in hand, so a build
# named this way is still a build that passed and was matched to a series; the
# log says which commit the name was taken away from, so the reading is not
# lost by renaming it.
if [ "$TARGET" = "builder" ]; then
    if [ -n "$IMAGE_TAG_SUFFIX" ]; then
        SUFFIX="$IMAGE_TAG_SUFFIX"
        log "IMAGE_TAG_SUFFIX is set: naming the builder $BASE_IMAGE-$SUFFIX"
        log "run-build.sh resolves the builder from Dockerfile.builder, so export" \
            "BUILDER_IMAGE_REF=$BASE_IMAGE-$SUFFIX to run a build with this one"
    else
        # The same hash run-build.sh resolves, from the same function, so the
        # reference this writes is the reference that Job asks for.
        SUFFIX="$(builder_suffix)" || die "could not derive the builder suffix"
        [ -n "$SUFFIX" ] || die "the builder suffix resolved to nothing"
    fi
else
    HEAD_SUFFIX="$(short12 "$PROV_HEAD")"
    [ "${#HEAD_SUFFIX}" = "12" ] \
        || die "the head commit in PROVENANCE.txt is too short to name a tag"
    if [ -n "$IMAGE_TAG_SUFFIX" ]; then
        SUFFIX="$IMAGE_TAG_SUFFIX"
        log "IMAGE_TAG_SUFFIX is set: naming this build $BASE_IMAGE-$SUFFIX" \
            "rather than $BASE_IMAGE-$HEAD_SUFFIX, which is the commit its provenance records"
    else
        SUFFIX="$HEAD_SUFFIX"
    fi
fi

[ -n "$BASE_IMAGE" ] || die "no base image name was resolved for target $TARGET"
IMAGE="$BASE_IMAGE-$SUFFIX"
# What containerd calls it. BASE_IMAGE comes from NODE_IMAGE, MINER_IMAGE or
# BUILDER_IMAGE, any of which may already be fully qualified, so the prefix is
# decided rather than prepended.
IMAGE_CANONICAL="$(canonical_image_ref "$IMAGE")"

# Nothing is imported under the final tag, ever.
#
# `ctr images import` takes the names out of the tarball and creates them, and
# creating a name that exists moves it. So an import straight to the final tag
# is a write: by the time the digests can be compared, the tag has already been
# replaced and whatever it named has already lost its only name. The first
# version of this did exactly that and reported the mismatch afterwards, which
# is a description of the damage rather than a check.
#
# So the build is imported under a reference belonging to this run alone, and
# nothing is published until the content has been weighed against what is
# already there. The temporary reference is removed on the way out whatever
# happens; it is a second name for content the final tag also names, so
# removing it never removes content -- confirmed on this cluster against a tag
# sharing a digest with a running workload.
IMPORT_REF="${IMAGE%%:*}:import-$(od -An -tx1 -N8 /dev/urandom | tr -d ' \n')"
case "$IMPORT_REF" in
    *:import-????????????????) ;;
    *) die "could not generate a unique import reference" ;;
esac
IMPORTED_REF="$(canonical_image_ref "$IMPORT_REF")"
IMPORT_ARMED=0

# Armed immediately before `ctr images import` rather than after it returns.
# containerd can create the reference and the client still fail, or be
# interrupted, before control comes back; a cleanup that only runs on the
# success path leaves that name behind exactly when something has gone wrong.
# Arming early costs nothing, because removing a reference that was never
# created is not an error: `ctr images rm` says "image not found" and exits 0.
#
# It also must not turn a failure into a different one. The status on entry is
# the script's, the remove is guarded so `errexit` cannot fire inside a trap,
# and its output is only shown when it fails, so the "not found" of the case
# this exists to cover stays quiet.
remove_import_ref() {
    local status=$? out rc=0
    [ "$IMPORT_ARMED" = "1" ] || return "$status"
    IMPORT_ARMED=0
    out="$($CTR images rm "$IMPORTED_REF" 2>&1)" || rc=$?
    [ "$rc" = "0" ] \
        || printf 'warning: could not remove %s (%s); remove it with `ctr images rm`\n' \
                  "$IMPORTED_REF" "$out" >&2
    return "$status"
}
trap remove_import_ref EXIT

log "building $IMAGE from $CONTEXT_SUBPATH/$DOCKERFILE"
log "importing it as $IMPORT_REF first; $IMAGE is only named if this agrees with it"
$KUBECTL -n "$NS" delete pod "$POD" --ignore-not-found --wait=true > /dev/null
if [ -n "${REGISTRY_MIRROR:-}" ]; then
    MIRROR_SED=(-e "s|__REGISTRY_MIRROR_ARG__|--registry-mirror=$REGISTRY_MIRROR|g")
else
    MIRROR_SED=(-e "/__REGISTRY_MIRROR_ARG__/d")
fi
sed "${MIRROR_SED[@]}" \
    -e "s|__NAME__|$POD|g" \
    -e "s|__IMAGE__|$IMPORT_REF|g" \
    -e "s|__CONTEXT_SUBPATH__|$CONTEXT_SUBPATH|g" \
    -e "s|__DOCKERFILE__|$DOCKERFILE|g" \
    -e "s|__TAR_NAME__|$TAR_NAME|g" \
    "$HERE/kaniko.yaml" | $KUBECTL apply -f - > /dev/null

log "waiting for $POD"
deadline=$(( $(date +%s) + 2400 ))
while :; do
    phase="$($KUBECTL -n "$NS" get pod "$POD" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    case "$phase" in
        Succeeded) break ;;
        Failed)
            $KUBECTL -n "$NS" logs "$POD" | tail -40
            die "$POD failed"
            ;;
    esac
    [ "$(date +%s)" -lt "$deadline" ] || die "$POD timed out in phase ${phase:-unknown}"
    sleep 10
done
$KUBECTL -n "$NS" logs "$POD" | tail -15

HOST_PATH="$(pvc_host_path)"
log "importing $HOST_PATH/img/$TAR_NAME as $IMPORT_REF"
IMPORT_ARMED=1
$CTR images import "$HOST_PATH/img/$TAR_NAME"

# The tarball is only needed for the import; the image now lives in containerd.
in_shell rm -f "/work/img/$TAR_NAME"
$KUBECTL -n "$NS" delete pod "$POD" --ignore-not-found > /dev/null

# One snapshot, taken here rather than before the build. A build takes an hour
# or more, and a tag read an hour ago is a guess about the present: the earlier
# version read it before kaniko started, so a tag created during the build was
# invisible to the comparison and got overwritten with the comparison reporting
# success.
load_ctr_images
NEW_DIGEST="$(ctr_digest_of "$IMPORTED_REF")"
[ -n "$NEW_DIGEST" ] || die "$IMPORT_REF is not in containerd after the import"
EXISTING_DIGEST="$(ctr_digest_of "$IMAGE_CANONICAL")"

if [ -z "$EXISTING_DIGEST" ]; then
    # `--local` is not a detail. Without it `ctr images tag` goes through the
    # transfer service, which creates the target whether or not it exists and
    # exits 0 either way; `--force` then has nothing to add. With it, creating
    # a name that already exists fails with "already exists" and changes
    # nothing. That is the only create-only operation containerd offers here,
    # and it is what closes the gap between the snapshot above and this line:
    # if another build named this tag in between, this refuses rather than
    # overwriting whatever that build put there. Measured on containerd
    # v2.1.5-k3s1; both behaviours were tested against a tag that existed.
    $CTR images tag --local "$IMPORTED_REF" "$IMAGE_CANONICAL" > /dev/null \
        || die "could not name $IMAGE: it did not exist a moment ago and does now," \
               "so another build is publishing the same tag. Nothing here was changed;" \
               "the content of this build is still $NEW_DIGEST."
    log "imported and named $IMAGE"
elif [ "$EXISTING_DIGEST" = "$NEW_DIGEST" ]; then
    log "already present: $IMAGE names this content already and was not touched"
else
    printf 'error: %s names %s and this build produced %s\n' \
        "$IMAGE" "$EXISTING_DIGEST" "$NEW_DIGEST" >&2
    printf 'error: the tag was not changed and still names what it named before.\n' >&2
    printf 'error: The same source produced different content. Set IMAGE_TAG_SUFFIX\n' >&2
    printf 'error: to publish this under a name of its own, and see the reproducibility\n' >&2
    printf 'error: notes in contrib/k3s/README.md.\n' >&2
    exit 1
fi

# The tag is the useful output: it is what an `images:` entry in
# ark0/kustomization.yaml is set to.
printf '\n%s\n' "$IMAGE"
printf '%s\n' "$NEW_DIGEST"

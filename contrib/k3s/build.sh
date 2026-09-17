#!/usr/bin/env bash
#
# In-pod driver for the P2MR build Job. Clones Bitcoin Core v31.1 onto the work
# PVC, applies the patch series, builds it, and runs the unit and functional
# tests. Everything it writes stays under $WORK, so a run can be inspected, and
# the build tree reused, long after the Job is gone.
#
# It is mounted into the Job from the ConfigMap p2mr-build-scripts and is never
# executed on a host. Every knob is an environment variable set by build.yaml.
#
# Where the binaries go:
#
#   $WORK/out/runs/$RUN_ID/bin   this run's binaries, written as soon as the
#                                build finishes, whatever the tests then say
#   $WORK/out/bin                the last set that passed every requested check
#   $WORK/imgctx/node/bin        the same set, as the node image build context
#
# The last two are written only at the end of a run that passed, and each gets
# a PROVENANCE.txt naming the patch sets, the hash of the applied commit list
# and the check results. build-image.sh refuses a context without one.
#
#   WORK                   root of the work PVC mount                 (/work)
#   RUN_ID                 names the log and tmp directories          (timestamp)
#   CORE_REPO/CORE_TAG     upstream to clone           (bitcoin/bitcoin, v31.1)
#   CORE_COMMIT            short commit the tag must resolve to       (9be056a)
#   PATCH_SETS             subdirectories of $WORK/patches, in order
#                          "master" = the 10 consensus patches
#                          "master m05" = those plus the 24 M0.5 wallet patches
#   FRESH_CLONE            1 = delete and re-clone the source tree first
#   APPLY_PATCHES          1 = reset to the tag and git am the series
#                          0 = build whatever is already in the tree
#   BUILD_JOBS             parallelism of cmake --build
#   USE_CCACHE             1 = add the ccache compiler launchers
#   EXTRA_CMAKE_ARGS       appended to the configure step (e.g. fuzz binary)
#   RUN_UNIT_TESTS         1 = run build/bin/test_bitcoin
#   UNIT_TEST_ARGS         arguments for it
#   FUNCTIONAL_TESTS       space separated test scripts; empty = skip the phase
#   RUN_DEFAULT_FUNCTIONAL 1 = also run the default set (no test list)
#   FUNCTIONAL_JOBS        --jobs for test_runner.py
#   FUNCTIONAL_EXTRA_ARGS  appended to every test_runner.py invocation
#
set -euo pipefail

WORK="${WORK:-/work}"
RUN_ID="${RUN_ID:-$(date -u +%Y%m%d-%H%M%S)}"
CORE_REPO="${CORE_REPO:-https://github.com/bitcoin/bitcoin.git}"
CORE_TAG="${CORE_TAG:-v31.1}"
CORE_COMMIT="${CORE_COMMIT:-9be056a}"
PATCH_SETS="${PATCH_SETS:-master}"
FRESH_CLONE="${FRESH_CLONE:-0}"
APPLY_PATCHES="${APPLY_PATCHES:-1}"
BUILD_JOBS="${BUILD_JOBS:-32}"
USE_CCACHE="${USE_CCACHE:-1}"
EXTRA_CMAKE_ARGS="${EXTRA_CMAKE_ARGS:-}"
RUN_UNIT_TESTS="${RUN_UNIT_TESTS:-1}"
UNIT_TEST_ARGS="${UNIT_TEST_ARGS:---report_level=detailed}"
FUNCTIONAL_TESTS="${FUNCTIONAL_TESTS:-}"
RUN_DEFAULT_FUNCTIONAL="${RUN_DEFAULT_FUNCTIONAL:-0}"
FUNCTIONAL_JOBS="${FUNCTIONAL_JOBS:-16}"
FUNCTIONAL_EXTRA_ARGS="${FUNCTIONAL_EXTRA_ARGS:-}"

SRC="$WORK/src"
LOGDIR="$WORK/logs/$RUN_ID"
SUMMARY="$LOGDIR/summary.txt"
# Where this run's binaries live until they have earned publication. The two
# shared directories below are written only at the very end, and only if every
# check that was asked for passed.
STAGE="$WORK/out/runs/$RUN_ID/bin"
PUBLISHED="$WORK/out/bin"
IMGCTX="$WORK/imgctx/node"
PROVENANCE_NAME="PROVENANCE.txt"
mkdir -p "$LOGDIR" "$STAGE" "$WORK/test-cache"

# Phase bookkeeping: every phase appends one line to the summary, so the last
# few lines of one file answer "what ran, how long, and did it pass".
say() { printf '\n=== [%s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }
record() { printf '%-28s %-8s %s\n' "$1" "$2" "${3:-}" >> "$SUMMARY"; }
secs_since() { echo $(( $(date +%s) - $1 )); }
hms() { printf '%dh%02dm%02ds' $(($1 / 3600)) $((($1 % 3600) / 60)) $(($1 % 60)); }

RUN_START=$(date +%s)
{
  echo "run id      : $RUN_ID"
  echo "started     : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "patch sets  : $PATCH_SETS"
  echo "build jobs  : $BUILD_JOBS"
  echo "toolchain   :"
  sed 's/^/              /' /opt/p2mr/toolchain.txt 2>/dev/null || true
  echo
} > "$SUMMARY"

say "environment"
cmake --version | head -1
g++ --version | head -1
python3 --version
echo "logs: $LOGDIR"
df -h "$WORK" | tail -1

# --- source tree -------------------------------------------------------------
if [ "$FRESH_CLONE" = "1" ] && [ -e "$SRC" ]; then
    say "removing the existing source tree (FRESH_CLONE=1)"
    rm -rf "$SRC"
fi

# The PVC is written by several pods; git refuses to operate on a tree it
# considers foreign unless the path is marked safe.
git config --global --add safe.directory "$SRC" || true
git config --global advice.detachedHead false || true

if [ ! -d "$SRC/.git" ]; then
    say "cloning $CORE_REPO at $CORE_TAG"
    t0=$(date +%s)
    git clone --branch "$CORE_TAG" --depth 1 "$CORE_REPO" "$SRC" 2>&1 | tee "$LOGDIR/clone.log"
    record clone ok "$(hms "$(secs_since "$t0")")"
else
    say "reusing the existing source tree at $SRC"
    record clone reused
fi

cd "$SRC"
# v31.1 is an annotated tag, so it has to be peeled to the commit it points at:
# git rev-parse on the tag name alone yields the tag object.
if [ "$APPLY_PATCHES" = "1" ]; then
    tag_short="$(git rev-parse --short "$CORE_TAG^{commit}")"
    if [ "$tag_short" != "$CORE_COMMIT" ]; then
        echo "expected $CORE_TAG to be $CORE_COMMIT, got $tag_short" >&2
        exit 1
    fi
fi
echo "HEAD $(git rev-parse --short HEAD), $CORE_TAG $(git rev-parse --short "$CORE_TAG^{commit}" 2> /dev/null || echo unknown)"

# --- patches -----------------------------------------------------------------
if [ "$APPLY_PATCHES" = "1" ]; then
    say "verifying patch checksums"
    for set_name in $PATCH_SETS; do
        dir="$WORK/patches/$set_name"
        if [ ! -d "$dir" ]; then
            echo "patch set $set_name not found at $dir" >&2
            exit 1
        fi
        sums="$(find "$dir" -maxdepth 1 -name 'SHA256SUMS*' | head -1)"
        if [ -z "$sums" ]; then
            echo "no SHA256SUMS file in $dir" >&2
            exit 1
        fi
        ( cd "$dir" && sha256sum -c "$(basename "$sums")" ) | tee -a "$LOGDIR/checksums.log"
    done
    record checksums ok

    say "applying the series on top of $CORE_TAG"
    t0=$(date +%s)
    git checkout -q -B p2mr "$CORE_TAG"
    applied=0
    for set_name in $PATCH_SETS; do
        dir="$WORK/patches/$set_name"
        count=$(find "$dir" -maxdepth 1 -name '*.patch' | wc -l)
        echo "--- $set_name: $count patches"
        # A failed git am leaves the tree mid-apply; dump enough to debug it
        # from the Job log, then stop, rather than building a half-patched tree.
        if ! git -c user.name=CipherScope -c user.email=dev@cipherscope.io \
                 am "$dir"/*.patch 2>&1 | tee -a "$LOGDIR/git-am.log"; then
            echo "git am failed in set $set_name" >&2
            git am --show-current-patch=diff 2>&1 | head -60 >&2 || true
            git status --short >&2 || true
            git am --abort || true
            record patches FAILED "$set_name"
            exit 1
        fi
        applied=$(( applied + count ))
    done
    echo "applied $applied patches, HEAD is now $(git rev-parse --short HEAD)"
    record patches ok "$applied patches, $(hms "$(secs_since "$t0")")"
else
    say "leaving the working tree as it is (APPLY_PATCHES=0)"
    record patches skipped
fi

# --- what this tree actually is ----------------------------------------------
# A runtime image's tag is derived from the patch sets recorded in the
# provenance, so those have to describe the source that was built rather than
# the sets the caller asked for. With APPLY_PATCHES=0 and a source tree left
# over from an earlier run those are different things: a tree still carrying
# the M0.5 series builds, passes every test and publishes as "master", because
# that is the default, and the image is then tagged -m0 and deployed to the
# block producing node. Wallet code on node A is not what -m0 is supposed to
# mean.
#
# So the tree is identified rather than believed. `git patch-id` reduces a diff
# to a hash that ignores commit metadata, context line numbers and whitespace,
# which is exactly the comparison wanted here: the patch-id list of
# $CORE_TAG..HEAD against the patch-id list of the .patch files on the volume.
# Equal lists in the same order mean the tree is that series and no other.
#
# This runs whether or not the patches were applied this time. When they were,
# it is a cheap confirmation that `git am` produced what the files describe.
patch_ids_of_tree() {
    local c
    for c in $(git -C "$SRC" log --format=%H --reverse "$CORE_TAG..HEAD" 2>/dev/null); do
        git -C "$SRC" show "$c" | git -C "$SRC" patch-id --stable | cut -d' ' -f1
    done
}

patch_ids_of_sets() {
    local set_name p found=0
    for set_name in $1; do
        [ -d "$WORK/patches/$set_name" ] || return 1
        for p in "$WORK/patches/$set_name"/*.patch; do
            [ -f "$p" ] || return 1
            git -C "$SRC" patch-id --stable < "$p" | cut -d' ' -f1
            found=1
        done
    done
    [ "$found" = "1" ]
}

# patch-id compares committed history, and cmake compiles the working tree.
# Those are the same thing only while the working tree is clean. An uncommitted
# edit to a tracked file, or a new untracked .cpp, changes what gets compiled
# and changes nothing patch-id looks at, so a modified tree would pass
# verification and receive the tag of the series it no longer is.
#
# With APPLY_PATCHES=1 the tree is reset to the tag and the series replayed, so
# it is clean by construction. With APPLY_PATCHES=0 it is whatever the previous
# run left behind plus anything done to it since, which is exactly the case
# this check exists for.
#
# Untracked files are only disqualifying where they would become build inputs.
# build/, the ccache directory and the functional tests' output are untracked
# by design and say nothing about what the compiler saw.
BUILD_INPUT_PATHS="src test cmake CMakeLists.txt depends"
DIRTY_REASON=""
CLEAN_QUERY_FAILED=0

# Returns 0 only for a tree that was inspected and found clean.
#
# Both commands used to end in `2>/dev/null || true`, which turns a failure
# with no stdout into an empty result, and an empty result reads as clean. A
# git that could not run at all, a repository it refused to open, a broken
# index: all three arrived here as "nothing modified", and a committed history
# that happened to match a patch set then produced `series verified: yes` for a
# tree nobody had actually looked at. Not knowing is its own answer and is kept
# distinct from both clean and dirty.
tree_is_clean() {
    local dirty untracked rc
    CLEAN_QUERY_FAILED=0

    rc=0
    dirty="$(git -C "$SRC" status --porcelain --untracked-files=no 2>&1)" || rc=$?
    if [ "$rc" != "0" ]; then
        DIRTY_REASON="git status failed (exit $rc)"
        CLEAN_QUERY_FAILED=1
        awk 'NR <= 5 { print }' <<< "$dirty" >&2
        return 1
    fi
    if [ -n "$dirty" ]; then
        DIRTY_REASON="$(awk 'END { print NR }' <<< "$dirty") tracked file(s) modified or staged"
        awk 'NR <= 20 { print }' <<< "$dirty" >&2
        return 1
    fi

    rc=0
    # shellcheck disable=SC2086  # BUILD_INPUT_PATHS is a pathspec list
    untracked="$(git -C "$SRC" ls-files --others --exclude-standard -- $BUILD_INPUT_PATHS 2>&1)" || rc=$?
    if [ "$rc" != "0" ]; then
        DIRTY_REASON="git ls-files failed (exit $rc)"
        CLEAN_QUERY_FAILED=1
        awk 'NR <= 5 { print }' <<< "$untracked" >&2
        return 1
    fi
    if [ -n "$untracked" ]; then
        DIRTY_REASON="$(awk 'END { print NR }' <<< "$untracked") untracked file(s) under $BUILD_INPUT_PATHS"
        awk 'NR <= 20 { print }' <<< "$untracked" >&2
        return 1
    fi
    return 0
}

say "identifying the series in the source tree"
TREE_IDS="$(patch_ids_of_tree || true)"
TREE_COMMITS="$(printf '%s\n' "$TREE_IDS" | grep -c . || true)"
SERIES_VERIFIED=no
PATCH_SETS_VERIFIED=unknown
SERIES_NOTE=""

# The requested list first, so a match reports the name the caller used; then
# the combinations this tooling knows, so that a mismatch can still say what
# the tree actually is instead of only that it is wrong.
try_series() {
    if [ "$SERIES_VERIFIED" = "yes" ]; then return 0; fi
    local ids
    ids="$(patch_ids_of_sets "$1" 2>/dev/null || true)"
    if [ -n "$ids" ] && [ "$ids" = "$TREE_IDS" ]; then
        SERIES_VERIFIED=yes
        PATCH_SETS_VERIFIED="$1"
    fi
    return 0
}

if tree_is_clean; then
    try_series "$PATCH_SETS"
    try_series "master"
    try_series "master m05"
    if [ "$SERIES_VERIFIED" != "yes" ]; then
        SERIES_NOTE="the $TREE_COMMITS commits over $CORE_TAG match no known patch set"
    fi
elif [ "$CLEAN_QUERY_FAILED" = "1" ]; then
    # Not dirty, and not clean either: unknown. The patch-id comparison is not
    # run at all, because its answer would describe commits while saying
    # nothing about the files cmake is about to read.
    SERIES_NOTE="cleanliness could not be determined: $DIRTY_REASON"
    echo "the working tree could not be inspected, so the series is not classified" >&2
    echo "check that git can read $SRC, then re-run" >&2
else
    # Do not fall through to the patch-id comparison. It would succeed, and
    # succeeding is the bug.
    SERIES_NOTE="working tree is not clean: $DIRTY_REASON"
    echo "the working tree has uncommitted changes, so what gets compiled is not" >&2
    echo "what the commits describe; the series cannot be verified from history" >&2
    echo "re-run with FRESH_CLONE=1, or commit the changes and regenerate the patches" >&2
fi

if [ "$SERIES_VERIFIED" = "yes" ]; then
    echo "tree matches patch sets '$PATCH_SETS_VERIFIED' ($TREE_COMMITS commits, clean)"
    if [ "$PATCH_SETS_VERIFIED" != "$PATCH_SETS" ]; then
        echo "WARNING: '$PATCH_SETS' was requested, but this tree is '$PATCH_SETS_VERIFIED'" >&2
        echo "WARNING: the provenance records the verified series, so the image will be tagged for it" >&2
        record series MISMATCH "requested '$PATCH_SETS', tree is '$PATCH_SETS_VERIFIED'"
    else
        record series ok "$PATCH_SETS_VERIFIED, $TREE_COMMITS commits"
    fi
else
    echo "series not verified: $SERIES_NOTE" >&2
    echo "automatic image tagging will be refused; this run can still build and test" >&2
    record series UNVERIFIED "$SERIES_NOTE"
fi

# --- configure and build -----------------------------------------------------
# These flags are the ones apply.sh uses. USE_CCACHE only adds the two compiler
# launchers; EXTRA_CMAKE_ARGS is empty unless a caller asks for more.
CMAKE_ARGS=(
    -B build
    -DCMAKE_BUILD_TYPE=Release
    -DBUILD_TESTS=ON
    -DENABLE_WALLET=ON
    -DBUILD_GUI=OFF
    -DWITH_ZMQ=OFF
    -DENABLE_IPC=OFF
)
if [ "$USE_CCACHE" = "1" ]; then
    export CCACHE_DIR="${CCACHE_DIR:-$WORK/ccache}"
    mkdir -p "$CCACHE_DIR"
    ccache --zero-stats > /dev/null 2>&1 || true
    CMAKE_ARGS+=(-DCMAKE_C_COMPILER_LAUNCHER=ccache -DCMAKE_CXX_COMPILER_LAUNCHER=ccache)
fi
if [ -n "$EXTRA_CMAKE_ARGS" ]; then
    # shellcheck disable=SC2206  # deliberate word splitting of a flag list
    CMAKE_ARGS+=($EXTRA_CMAKE_ARGS)
fi

say "cmake configure"
t0=$(date +%s)
cmake "${CMAKE_ARGS[@]}" 2>&1 | tee "$LOGDIR/configure.log"
record configure ok "$(hms "$(secs_since "$t0")")"

say "cmake build -j$BUILD_JOBS"
t0=$(date +%s)
cmake --build build -j"$BUILD_JOBS" 2>&1 | tee "$LOGDIR/build.log"
record build ok "$(hms "$(secs_since "$t0")")"
if [ "$USE_CCACHE" = "1" ]; then
    ccache --show-stats 2>&1 | tee "$LOGDIR/ccache.log" | head -20
fi

say "staging binaries for this run only ($STAGE)"
for b in bitcoind bitcoin-cli bitcoin-tx bitcoin-util bitcoin-wallet; do
    if [ -x "build/bin/$b" ]; then
        install -m 0755 "build/bin/$b" "$STAGE/$b"
    fi
done
"$STAGE/bitcoind" -version | head -2 | tee "$LOGDIR/bitcoind-version.txt"
du -sh "$STAGE"

# --- unit tests --------------------------------------------------------------
if [ "$RUN_UNIT_TESTS" = "1" ]; then
    say "unit tests: build/bin/test_bitcoin $UNIT_TEST_ARGS"
    t0=$(date +%s)
    unit_rc=0
    # shellcheck disable=SC2086  # UNIT_TEST_ARGS is a flag list
    ./build/bin/test_bitcoin $UNIT_TEST_ARGS > "$LOGDIR/test_bitcoin.log" 2>&1 || unit_rc=$?
    tail -20 "$LOGDIR/test_bitcoin.log"
    # Boost prints its own module summary at the top of a detailed report, and
    # that is the authoritative count. Callers who override UNIT_TEST_ARGS may
    # turn the report off, in which case there is nothing to quote.
    npass=$(grep -m1 -E '[0-9]+ test cases? out of [0-9]+ passed' "$LOGDIR/test_bitcoin.log" | sed 's/^ *//' || true)
    nskip=$(grep -m1 -E '[0-9]+ test cases? out of [0-9]+ skipped' "$LOGDIR/test_bitcoin.log" | sed 's/^ *//' || true)
    ncases="${npass:-see the log}${nskip:+, $nskip}"
    if [ "$unit_rc" = "0" ]; then
        record unit-tests ok "$ncases, $(hms "$(secs_since "$t0")")"
    else
        record unit-tests FAILED "exit $unit_rc, $(hms "$(secs_since "$t0")")"
        echo "unit tests failed, see $LOGDIR/test_bitcoin.log" >&2
        cat "$SUMMARY"
        exit "$unit_rc"
    fi
else
    record unit-tests skipped
fi

# --- functional tests --------------------------------------------------------
# test_runner.py insists on an empty --tmpdir, so each phase gets its own. The
# --cachedir is shared: it only holds a pre-mined regtest chain, which the
# framework regenerates when it does not match the binaries.
run_functional() {
    phase="$1"
    shift
    tmpdir="$WORK/test-tmp/$RUN_ID/$phase"
    log="$LOGDIR/functional-$phase.log"
    rm -rf "$tmpdir"
    mkdir -p "$tmpdir"
    say "functional ($phase): ${*:-<default set>}"
    t0=$(date +%s)
    rc=0
    # shellcheck disable=SC2086  # FUNCTIONAL_EXTRA_ARGS is a flag list
    ./build/test/functional/test_runner.py \
        --jobs="$FUNCTIONAL_JOBS" \
        --cachedir="$WORK/test-cache" \
        --tmpdir="$tmpdir" \
        $FUNCTIONAL_EXTRA_ARGS \
        "$@" > "$log" 2>&1 || rc=$?
    tail -25 "$log"
    # test_runner.py reports each test as "<n>/<total> - <name> <result>", so
    # the three counts come straight out of those lines.
    n_pass=$(grep -cE '^[0-9]+/[0-9]+ - .+ passed' "$log" || true)
    n_fail=$(grep -cE '^[0-9]+/[0-9]+ - .+ failed' "$log" || true)
    n_skip=$(grep -cE '^[0-9]+/[0-9]+ - .+ skipped' "$log" || true)
    counts="$n_pass passed, $n_fail failed, $n_skip skipped"
    if [ "$rc" = "0" ]; then
        record "functional-$phase" ok "$counts, $(hms "$(secs_since "$t0")")"
    else
        record "functional-$phase" FAILED "$counts, exit $rc, $(hms "$(secs_since "$t0")")"
    fi
    # Passing tests clean up after themselves; failures keep their datadirs.
    rmdir "$tmpdir" 2> /dev/null || true
    return "$rc"
}

fn_rc=0
if [ -n "$FUNCTIONAL_TESTS" ]; then
    # shellcheck disable=SC2086  # FUNCTIONAL_TESTS is a list of test scripts
    run_functional p2mr $FUNCTIONAL_TESTS || fn_rc=$?
else
    record functional-p2mr skipped
fi

if [ "$RUN_DEFAULT_FUNCTIONAL" = "1" ]; then
    run_functional default || fn_rc=$?
else
    record functional-default skipped
fi

# --- publication --------------------------------------------------------------
# Until now the binaries only exist at $STAGE, under this run's id.
#
# They used to be installed into $PUBLISHED and $IMGCTX/bin immediately after
# the build, before a single test had run, and build-image.sh only checked that
# bitcoind was an executable file. A run whose tests failed therefore left its
# binaries exactly where the image build reads from, and nothing recorded that
# they had failed. Publication is the last step now, it happens only when every
# check that was asked for passed, and it writes a provenance file that
# build-image.sh requires.
#
# A failing run publishes nothing and leaves $PUBLISHED as it was, so what is
# published is always the most recent set that passed, together with the record
# of what it passed.
# Built in $LOGDIR and copied out from there, so that hashing the staged
# binaries never has to hash the half-written provenance file itself.
write_provenance() {
    local out="$LOGDIR/provenance.txt" head_full commit_list
    head_full="$(git -C "$SRC" rev-parse HEAD)"
    # The ordered list of commits on top of the base tag, hashed. Two builds
    # sharing this value applied the same patches in the same order; a
    # reordered or edited series gives a different value even when the patch
    # count matches.
    commit_list="$(git -C "$SRC" log --format=%H --reverse "$CORE_TAG..HEAD" 2>/dev/null \
                   | sha256sum | cut -d' ' -f1 || true)"
    {
        echo "# Provenance of the binaries in this directory."
        echo "# Written by contrib/k3s/build.sh only after every requested check passed."
        echo "result              : pass"
        echo "run id              : $RUN_ID"
        echo "finished            : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "core repo           : $CORE_REPO"
        echo "core tag            : $CORE_TAG"
        echo "core commit         : $CORE_COMMIT"
        # Requested is what the caller asked for; verified is what the source
        # tree was measured to be. build-image.sh derives the image tag from
        # the verified value and refuses to derive one at all unless the
        # verification succeeded.
        echo "patch sets requested: $PATCH_SETS"
        echo "patch sets verified : $PATCH_SETS_VERIFIED"
        echo "series verified     : $SERIES_VERIFIED"
        echo "series note         : ${SERIES_NOTE:-clean tree, patch-ids match}"
        echo "commits over tag    : $TREE_COMMITS"
        echo "patches applied     : ${applied:-0}"
        echo "head                : $head_full"
        echo "commit list sha256  : $commit_list"
        echo "binaries            :"
        ( cd "$STAGE" && sha256sum -b -- * | sed 's/^/  /' )
        echo "checks              :"
        sed 's/^/  /' "$SUMMARY"
    } > "$out"
    printf '%s\n' "$out"
}

if [ "$fn_rc" = "0" ]; then
    record TOTAL ok "$(hms "$(secs_since "$RUN_START")")"
    say "publishing $STAGE to $PUBLISHED and $IMGCTX/bin"
    prov="$(write_provenance)"
    mkdir -p "$PUBLISHED" "$IMGCTX/bin"
    # Replace the contents rather than merging, so a binary that this series no
    # longer builds cannot linger and be picked up by an image build.
    rm -f "$PUBLISHED"/* "$IMGCTX/bin"/*
    cp -p "$STAGE"/* "$PUBLISHED/"
    for b in bitcoind bitcoin-cli bitcoin-tx bitcoin-util bitcoin-wallet; do
        if [ -f "$STAGE/$b" ]; then cp -p "$STAGE/$b" "$IMGCTX/bin/$b"; fi
    done
    # Beside the context's bin/ rather than inside it: Dockerfile.node copies
    # bin/ wholesale, and the provenance describes the build rather than being
    # part of the runtime.
    install -m 0644 "$prov" "$STAGE/$PROVENANCE_NAME"
    install -m 0644 "$prov" "$PUBLISHED/$PROVENANCE_NAME"
    install -m 0644 "$prov" "$IMGCTX/$PROVENANCE_NAME"
    record published ok "$PUBLISHED, $IMGCTX/bin"
else
    record TOTAL FAILED "$(hms "$(secs_since "$RUN_START")")"
    record published no "checks failed; binaries left at $STAGE"
    echo "not publishing: this run's binaries stay at $STAGE" >&2
    echo "$PUBLISHED still holds the last set that passed, if there is one" >&2
fi

# --- summary -----------------------------------------------------------------
say "summary ($SUMMARY)"
cat "$SUMMARY"
exit "$fn_rc"

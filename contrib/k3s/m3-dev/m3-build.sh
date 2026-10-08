#!/usr/bin/env bash
#
# In-pod driver for the M3 (P2MR tripwire) development builds. A trimmed copy of
# contrib/k3s/build.sh that keeps to its own directories on the work volume, so
# that it touches nothing the M2 release builds use:
#
#   /work/m3-patches              the M3 series the run applies (copied there by m3-run.sh); the
#                                 earlier sets are read from /work/patches, the release pipeline's
#   /work/m3-src                  source tree (cloned once, reset per run)
#   /work/m3-ccache               compiler cache (CCACHE_DIR, set by the Job)
#   /work/m3-logs/$RUN_ID         summary.txt and one log per phase
#   /work/m3-out/runs/$RUN_ID/bin this run's binaries
#   /work/m3-test-cache           functional test --cachedir
#   /work/m3-test-tmp/$RUN_ID     functional test --tmpdir
#   /work/m3-fuzz-inputs/$RUN_ID  inputs for the fuzz target FUZZ_TARGET (copied there by m3-run.sh)
#   /work/m3-src/build-fuzz       the build that runs fuzz targets (-DBUILD_FOR_FUZZING=ON)
#   /work/m3-imgctx/node          with M3_IMGCTX=1, the binaries of a run that passed, as the
#                                 node image context, with their PROVENANCE.txt
#
# It never writes /work/src, /work/ccache, /work/out or /work/imgctx, and it
# publishes nothing.
set -euo pipefail

WORK="${WORK:-/work}"
RUN_ID="${RUN_ID:?RUN_ID is required}"
CORE_REPO="${CORE_REPO:-https://github.com/bitcoin/bitcoin.git}"
CORE_TAG="${CORE_TAG:-v31.1}"
CORE_COMMIT="${CORE_COMMIT:-9be056a}"
PATCH_SETS="${PATCH_SETS:-master m05 spacing m1 m2 m3}"
BUILD_JOBS="${BUILD_JOBS:-10}"
EXTRA_CMAKE_ARGS="${EXTRA_CMAKE_ARGS:-}"
RUN_UNIT_TESTS="${RUN_UNIT_TESTS:-1}"
UNIT_TEST_ARGS="${UNIT_TEST_ARGS:---report_level=detailed}"
FUNCTIONAL_TESTS="${FUNCTIONAL_TESTS:-}"
RUN_DEFAULT_FUNCTIONAL="${RUN_DEFAULT_FUNCTIONAL:-0}"
FUNCTIONAL_JOBS="${FUNCTIONAL_JOBS:-8}"
FUNCTIONAL_EXTRA_ARGS="${FUNCTIONAL_EXTRA_ARGS:-}"
# Apply only the first N patches of the m3 set, to check each commit of the series on its own.
M3_PATCH_LIMIT="${M3_PATCH_LIMIT:-}"
# Run the fuzz target FUZZ_TARGET on each file in FUZZ_INPUTS, in a build of its own: the fuzz binary
# of a Release build only compiles the targets, it does not run them.
FUZZ_TARGET="${FUZZ_TARGET:-}"
FUZZ_INPUTS="${FUZZ_INPUTS:-}"
# 1 = publish the binaries of a passing run to /work/m3-imgctx/node, for the staging images.
M3_IMGCTX="${M3_IMGCTX:-0}"

SRC="$WORK/m3-src"
LOGDIR="$WORK/m3-logs/$RUN_ID"
SUMMARY="$LOGDIR/summary.txt"
STAGE="$WORK/m3-out/runs/$RUN_ID/bin"
mkdir -p "$LOGDIR" "$STAGE" "$WORK/m3-test-cache"

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
  echo
} > "$SUMMARY"

git config --global --add safe.directory "$SRC" || true
git config --global advice.detachedHead false || true

if [ ! -d "$SRC/.git" ]; then
    say "cloning $CORE_REPO at $CORE_TAG into $SRC"
    t0=$(date +%s)
    git clone --branch "$CORE_TAG" --depth 1 "$CORE_REPO" "$SRC" 2>&1 | tee "$LOGDIR/clone.log"
    record clone ok "$(hms "$(secs_since "$t0")")"
else
    record clone reused
fi

cd "$SRC"
tag_short="$(git rev-parse --short "$CORE_TAG^{commit}")"
[ "$tag_short" = "$CORE_COMMIT" ] || { echo "expected $CORE_TAG to be $CORE_COMMIT, got $tag_short" >&2; exit 1; }

# The M3 series is m3-run.sh's own copy; /work/patches/m3 belongs to the release pipeline.
set_dir() { if [ "$1" = "m3" ]; then echo "$WORK/m3-patches"; else echo "$WORK/patches/$1"; fi; }

say "verifying patch checksums"
for set_name in $PATCH_SETS; do
    dir="$(set_dir "$set_name")"
    [ -d "$dir" ] || { echo "patch set $set_name not found at $dir" >&2; exit 1; }
    sums="$(find "$dir" -maxdepth 1 -name 'SHA256SUMS*' | head -1)"
    [ -n "$sums" ] || { echo "no SHA256SUMS file in $dir" >&2; exit 1; }
    ( cd "$dir" && sha256sum -c "$(basename "$sums")" ) | tee -a "$LOGDIR/checksums.log"
done
record checksums ok

say "applying the series on top of $CORE_TAG"
t0=$(date +%s)
git am --abort > /dev/null 2>&1 || true
git checkout -q -f -B p2mr "$CORE_TAG"
git clean -q -fdx -e build -e build-fuzz
applied=0
last_patch=""
for set_name in $PATCH_SETS; do
    dir="$(set_dir "$set_name")"
    patches=("$dir"/*.patch)
    if [ "$set_name" = "m3" ] && [ -n "$M3_PATCH_LIMIT" ]; then
        patches=("${patches[@]:0:$M3_PATCH_LIMIT}")
    fi
    count=${#patches[@]}
    last_patch="${patches[$((count - 1))]}"
    echo "--- $set_name: $count patches"
    if ! git -c user.name=CipherScope -c user.email=dev@cipherscope.io \
             am "${patches[@]}" 2>&1 | tee -a "$LOGDIR/git-am.log"; then
        echo "git am failed in set $set_name" >&2
        git am --show-current-patch=diff 2>&1 | head -60 >&2 || true
        git am --abort || true
        record patches FAILED "$set_name"
        exit 1
    fi
    applied=$(( applied + count ))
done
echo "applied $applied patches, HEAD is now $(git rev-parse HEAD)"
record patches ok "$applied patches, head $(git rev-parse --short=12 HEAD), $(hms "$(secs_since "$t0")")"

CMAKE_ARGS=(
    -B build
    -DCMAKE_BUILD_TYPE=Release
    -DBUILD_TESTS=ON
    -DENABLE_WALLET=ON
    -DBUILD_GUI=OFF
    -DWITH_ZMQ=OFF
    -DENABLE_IPC=OFF
    -DCMAKE_C_COMPILER_LAUNCHER=ccache
    -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
)
if [ -n "$EXTRA_CMAKE_ARGS" ]; then
    # shellcheck disable=SC2206
    CMAKE_ARGS+=($EXTRA_CMAKE_ARGS)
fi
mkdir -p "$CCACHE_DIR"
ccache --zero-stats > /dev/null 2>&1 || true

say "cmake configure"
t0=$(date +%s)
cmake "${CMAKE_ARGS[@]}" > "$LOGDIR/configure.log" 2>&1 || { tail -40 "$LOGDIR/configure.log"; record configure FAILED; exit 1; }
record configure ok "$(hms "$(secs_since "$t0")")"

say "cmake build -j$BUILD_JOBS"
t0=$(date +%s)
build_rc=0
cmake --build build -j"$BUILD_JOBS" > "$LOGDIR/build.log" 2>&1 || build_rc=$?
if [ "$build_rc" != "0" ]; then
    grep -n -E "error|Error" "$LOGDIR/build.log" | head -80
    tail -60 "$LOGDIR/build.log"
    record build FAILED "exit $build_rc, $(hms "$(secs_since "$t0")")"
    cat "$SUMMARY"
    exit "$build_rc"
fi
record build ok "$(hms "$(secs_since "$t0")")"
ccache --show-stats > "$LOGDIR/ccache.log" 2>&1 || true
grep -c "warning:" "$LOGDIR/build.log" > "$LOGDIR/warnings.count" || true

for b in bitcoind bitcoin-cli bitcoin-tx bitcoin-util bitcoin-wallet; do
    [ -x "build/bin/$b" ] && install -m 0755 "build/bin/$b" "$STAGE/$b"
done

unit_rc=0
if [ "$RUN_UNIT_TESTS" = "1" ]; then
    say "unit tests: test_bitcoin $UNIT_TEST_ARGS"
    t0=$(date +%s)
    # shellcheck disable=SC2086
    ./build/bin/test_bitcoin $UNIT_TEST_ARGS > "$LOGDIR/test_bitcoin.log" 2>&1 || unit_rc=$?
    tail -30 "$LOGDIR/test_bitcoin.log"
    npass=$(grep -m1 -E '[0-9]+ test cases? out of [0-9]+ passed' "$LOGDIR/test_bitcoin.log" | sed 's/^ *//' || true)
    nskip=$(grep -m1 -E '[0-9]+ test cases? out of [0-9]+ skipped' "$LOGDIR/test_bitcoin.log" | sed 's/^ *//' || true)
    nfail=$(grep -m1 -E '[0-9]+ test cases? out of [0-9]+ failed' "$LOGDIR/test_bitcoin.log" | sed 's/^ *//' || true)
    if [ "$unit_rc" = "0" ]; then
        record unit-tests ok "${npass:-?}${nskip:+, $nskip}, $(hms "$(secs_since "$t0")")"
    else
        record unit-tests FAILED "exit $unit_rc, ${nfail:-?}, $(hms "$(secs_since "$t0")")"
    fi
else
    record unit-tests skipped
fi

fuzz_rc=0
if [ -n "$FUZZ_INPUTS" ]; then
    say "fuzz build (BUILD_FOR_FUZZING), then $FUZZ_TARGET on each file in $FUZZ_INPUTS"
    t0=$(date +%s)
    if cmake -B build-fuzz -DBUILD_FOR_FUZZING=ON -DCMAKE_BUILD_TYPE=Release -DENABLE_WALLET=OFF -DBUILD_GUI=OFF \
             -DWITH_ZMQ=OFF -DENABLE_IPC=OFF -DCMAKE_C_COMPILER_LAUNCHER=ccache -DCMAKE_CXX_COMPILER_LAUNCHER=ccache \
             > "$LOGDIR/fuzz-configure.log" 2>&1 &&
       cmake --build build-fuzz -j"$BUILD_JOBS" --target fuzz > "$LOGDIR/fuzz-build.log" 2>&1; then
        record fuzz-build ok "$(hms "$(secs_since "$t0")")"
        n_ok=0
        n_all=0
        for f in "$FUZZ_INPUTS"/*; do
            n_all=$(( n_all + 1 ))
            echo "--- $(basename "$f")" >> "$LOGDIR/fuzz.log"
            if FUZZ="$FUZZ_TARGET" ./build-fuzz/bin/fuzz "$f" >> "$LOGDIR/fuzz.log" 2>&1; then
                n_ok=$(( n_ok + 1 ))
                record "  $(basename "$f")" ok
            else
                fuzz_rc=1
                record "  $(basename "$f")" FAILED
            fi
        done
        tail -40 "$LOGDIR/fuzz.log"
        if [ "$fuzz_rc" = "0" ]; then
            record "fuzz-$FUZZ_TARGET" ok "$n_ok of $n_all inputs"
        else
            record "fuzz-$FUZZ_TARGET" FAILED "$n_ok of $n_all inputs ran through"
        fi
    else
        fuzz_rc=1
        tail -40 "$LOGDIR/fuzz-configure.log" "$LOGDIR/fuzz-build.log" 2>/dev/null || true
        record fuzz-build FAILED "$(hms "$(secs_since "$t0")")"
    fi
fi

run_functional() {
    phase="$1"; shift
    tmpdir="$WORK/m3-test-tmp/$RUN_ID/$phase"
    log="$LOGDIR/functional-$phase.log"
    rm -rf "$tmpdir"; mkdir -p "$tmpdir"
    say "functional ($phase): ${*:-<default set>}"
    t0=$(date +%s)
    rc=0
    # shellcheck disable=SC2086
    ./build/test/functional/test_runner.py --jobs="$FUNCTIONAL_JOBS" --cachedir="$WORK/m3-test-cache" \
        --tmpdir="$tmpdir" $FUNCTIONAL_EXTRA_ARGS "$@" > "$log" 2>&1 || rc=$?
    tail -40 "$log"
    n_pass=$(grep -cE '^[0-9]+/[0-9]+ - .+ passed' "$log" || true)
    n_fail=$(grep -cE '^[0-9]+/[0-9]+ - .+ failed' "$log" || true)
    n_skip=$(grep -cE '^[0-9]+/[0-9]+ - .+ skipped' "$log" || true)
    counts="$n_pass passed, $n_fail failed, $n_skip skipped"
    if [ "$rc" = "0" ]; then
        record "functional-$phase" ok "$counts, $(hms "$(secs_since "$t0")")"
    else
        record "functional-$phase" FAILED "$counts, exit $rc, $(hms "$(secs_since "$t0")")"
    fi
    return "$rc"
}

fn_rc=0
if [ -n "$FUNCTIONAL_TESTS" ]; then
    # shellcheck disable=SC2086
    run_functional m3 $FUNCTIONAL_TESTS || fn_rc=$?
fi
if [ "$RUN_DEFAULT_FUNCTIONAL" = "1" ]; then
    run_functional default || fn_rc=$?
fi

# The binaries of a run that passed, as the node image context of the staging network
# (../m3-staging/README.md), with the PROVENANCE.txt ../build-image.sh requires; the same file goes
# beside them in this run's directory, from where ../stage-miner.sh copies it into the producer's
# context. Only the whole series, and only when the tree is what this run applied: tracked files
# unchanged and as many commits above the tag as patches applied. The series head is the commit
# of the Core tree the last patch was exported from, which names the images.
publish_imgctx() {
    local ctx="$WORK/m3-imgctx/node" prov="$LOGDIR/provenance.txt" commits dirty series_head
    [ -z "$M3_PATCH_LIMIT" ] || { echo "not publishing a prefix of the series" >&2; return 1; }
    commits="$(git rev-list --count "$CORE_TAG..HEAD")"
    dirty="$(git status --porcelain --untracked-files=no)"
    [ -z "$dirty" ] && [ "$commits" = "$applied" ] \
        || { echo "the tree is not the series applied: $commits commits, $applied patches, ${dirty:+tracked files changed}" >&2; return 1; }
    series_head="$(sed -n '1s/^From \([0-9a-f]\{40\}\) .*/\1/p' "$last_patch")"
    [ -n "$series_head" ] || { echo "no commit in the first line of $last_patch" >&2; return 1; }
    {
        echo "# Provenance of the binaries in this directory."
        echo "# Written by contrib/k3s/m3-dev/m3-build.sh only after every requested check passed."
        echo "result              : pass"
        echo "run id              : $RUN_ID"
        echo "finished            : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "core repo           : $CORE_REPO"
        echo "core tag            : $CORE_TAG"
        echo "core commit         : $CORE_COMMIT"
        echo "patch sets requested: $PATCH_SETS"
        echo "patch sets verified : $PATCH_SETS"
        echo "series verified     : yes"
        echo "series note         : applied in this run onto the tag from the patch sets as their SHA256SUMS name them; tracked files unchanged; one commit above the tag for each patch"
        echo "series head         : $series_head"
        echo "commits over tag    : $commits"
        echo "patches applied     : $applied"
        echo "head                : $(git rev-parse HEAD)"
        echo "commit list sha256  : $(git log --format=%H --reverse "$CORE_TAG..HEAD" | sha256sum | cut -d' ' -f1)"
        echo "binaries            :"
        ( cd "$STAGE" && sha256sum -b -- * | sed 's/^/  /' )
        echo "checks              :"
        sed 's/^/  /' "$SUMMARY"
    } > "$prov"
    mkdir -p "$ctx/bin" && rm -f "$ctx/bin"/* "$ctx/PROVENANCE.txt" || return 1
    for b in bitcoind bitcoin-cli bitcoin-tx bitcoin-util bitcoin-wallet; do
        cp -p "$STAGE/$b" "$ctx/bin/$b" || return 1
    done
    install -m 0644 "$prov" "$STAGE/PROVENANCE.txt" && install -m 0644 "$prov" "$ctx/PROVENANCE.txt" || return 1
    echo "$series_head"
}

publish_rc=0
if [ "$unit_rc" = "0" ] && [ "$fn_rc" = "0" ] && [ "$fuzz_rc" = "0" ]; then
    record TOTAL ok "$(hms "$(secs_since "$RUN_START")")"
    if [ "$M3_IMGCTX" = "1" ]; then
        if series_head="$(publish_imgctx)"; then
            record published ok "$WORK/m3-imgctx/node, series head ${series_head:0:12}"
        else
            publish_rc=1
            record published FAILED "see the log; the context may be incomplete"
        fi
    fi
else
    record TOTAL FAILED "$(hms "$(secs_since "$RUN_START")")"
fi
say "summary"
cat "$SUMMARY"
[ "$unit_rc" = "0" ] && [ "$fn_rc" = "0" ] && [ "$fuzz_rc" = "0" ] && [ "$publish_rc" = "0" ]

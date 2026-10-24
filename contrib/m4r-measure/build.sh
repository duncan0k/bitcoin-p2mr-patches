#!/usr/bin/env bash
# Build the measurement-only node of the M4-R whole-block gate (README.md): bitcoind_m4r_measure, with
# the worst-case hook of the rescue verifier (M4R_WORST=1), its portable multiplication (M4R_PORTABLE=1)
# and verification counters. It is not a node for any network, and it cannot be installed.
#
# Usage: bash build.sh <core-tree> <work-dir>
#   <core-tree>  a Bitcoin Core v31.1 git tree with every patch set up to patches-m4r/ applied (88
#                patches, not the kill switch); it is not modified
#   <work-dir>   a new directory: a worktree of that tree with measurement-build.patch applied, and the
#                build directory build/ (bitcoind_m4r_measure)
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$1"; WORK="$2"
[ ! -e "$WORK" ] || { echo "$WORK exists; give a new directory" >&2; exit 1; }
git -C "$SRC" worktree add --detach "$WORK" HEAD
# The patch was written for these files as they are in the series: each must have its pinned SHA-256,
# or the hook is reviewed again against the new code before anything is built.
(cd "$WORK" && sha256sum --check --strict --quiet "$HERE/measurement-build.pins") || {
  echo "refusing: a file measurement-build.patch changes is not the one it was written for (see above);" \
       "review the patch against it and update measurement-build.pins" >&2
  exit 1
}
git -C "$WORK" apply "$HERE/measurement-build.patch"
# The patch's CMake refuses M4R_MEASURE if the configuration defines any executable or shared library
# target but bitcoind_m4r_measure (it checks the targets, not the options), so every other one is off.
OFF="-DBUILD_BITCOIN_BIN=OFF -DBUILD_DAEMON=OFF -DBUILD_GUI=OFF -DENABLE_IPC=OFF -DBUILD_CLI=OFF
     -DBUILD_TESTS=OFF -DBUILD_TX=OFF -DBUILD_UTIL=OFF -DBUILD_UTIL_CHAINSTATE=OFF -DBUILD_KERNEL_LIB=OFF
     -DBUILD_WALLET_TOOL=OFF -DBUILD_FUZZ_BINARY=OFF -DBUILD_FOR_FUZZING=OFF -DWITH_ZMQ=OFF -DBUILD_BENCH=OFF
     -DENABLE_WALLET=OFF"
# shellcheck disable=SC2086
cmake -S "$WORK" -B "$WORK/build" -DCMAKE_BUILD_TYPE=Release $OFF -DM4R_MEASURE=ON
if grep -Eq '^(BUILD_GUI|ENABLE_IPC|BUILD_DAEMON):BOOL=(ON|TRUE|1)$' "$WORK/build/CMakeCache.txt"; then
  echo "refusing: a node or GUI target is enabled in the measurement build" >&2
  exit 1
fi
cmake --build "$WORK/build" -j"$(nproc)" --target bitcoind_m4r_measure
# The node refuses every chain but regtest: check it on mainnet before anything else uses it.
mkdir -p "$WORK/build/guard-check"
if timeout 60 "$WORK/build/bin/bitcoind_m4r_measure" -chain=main -datadir="$WORK/build/guard-check" \
     > "$WORK/build/guard-check.log" 2>&1 \
   || ! grep -q "runs on regtest only" "$WORK/build/guard-check.log"; then
  echo "bitcoind_m4r_measure did not refuse mainnet as expected; refusing to continue" >&2
  exit 1
fi
echo "built $WORK/build/bin/bitcoind_m4r_measure (refuses mainnet: checked)"

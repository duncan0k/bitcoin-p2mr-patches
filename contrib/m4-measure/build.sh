#!/usr/bin/env bash
# Build the measurement-only binaries of M4-S (README.md): bench_bitcoin, and the measurement-only node
# bitcoind_m4_measure of the whole-block script, in a timing build (no counters) and a counting build
# (counters, one script thread). None is a node for any network, and none can be installed.
#
# Usage: bash build.sh <core-tree> <work-dir>
#   <core-tree>  a Bitcoin Core v31.1 tree with every patch set up to patches-m4/ applied (as apply.sh
#                and contrib/k3s do it); it is not modified
#   <work-dir>   a new directory: a copy of the tree with measurement-build.patch applied, and three build
#                directories: build/ (bench_bitcoin), build-node/ (bitcoind_m4_measure, timing) and
#                build-count/ (bitcoind_m4_measure, counting)
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$1"; WORK="$2"
[ ! -e "$WORK" ] || { echo "$WORK exists; give a new directory" >&2; exit 1; }
git -C "$SRC" worktree add --detach "$WORK" HEAD
git -C "$WORK" apply "$HERE/measurement-build.patch"
# The patch's CMake refuses SLH_MEASURE_WORST if the configuration defines any executable or shared
# library target but bench_bitcoin and bitcoind_m4_measure (it checks the targets, not the options),
# since each would link the hooked library; so every other executable is turned off here.
OFF="-DBUILD_BITCOIN_BIN=OFF -DBUILD_DAEMON=OFF -DBUILD_GUI=OFF -DENABLE_IPC=OFF -DBUILD_CLI=OFF
     -DBUILD_TESTS=OFF -DBUILD_TX=OFF -DBUILD_UTIL=OFF -DBUILD_UTIL_CHAINSTATE=OFF -DBUILD_KERNEL_LIB=OFF
     -DBUILD_WALLET_TOOL=OFF -DBUILD_FUZZ_BINARY=OFF -DBUILD_FOR_FUZZING=OFF -DWITH_ZMQ=OFF"
refuse_node_targets() {
  if grep -Eq '^(BUILD_GUI|ENABLE_IPC|BUILD_DAEMON):BOOL=(ON|TRUE|1)$' "$1/CMakeCache.txt"; then
    echo "refusing: a node or GUI target is enabled in the measurement build $1" >&2
    exit 1
  fi
}
# shellcheck disable=SC2086
cmake -S "$WORK" -B "$WORK/build" -DCMAKE_BUILD_TYPE=Release -DBUILD_BENCH=ON -DENABLE_WALLET=ON $OFF \
      -DSLH_MEASURE_WORST=ON
refuse_node_targets "$WORK/build"
cmake --build "$WORK/build" -j"$(nproc)" --target bench_bitcoin
echo "built $WORK/build/bin/bench_bitcoin"
# Separate build directories for the measurement-only node: no wallet, no bench, no install rule. The
# timing build has no counters; the counting build has them and checks scripts in one thread.
build_node() {
  # shellcheck disable=SC2086
  cmake -S "$WORK" -B "$WORK/$1" -DCMAKE_BUILD_TYPE=Release -DBUILD_BENCH=OFF -DENABLE_WALLET=OFF $OFF \
        -DSLH_MEASURE_WORST=ON -DSLH_MEASURE_NODE=ON "$2"
  refuse_node_targets "$WORK/$1"
  cmake --build "$WORK/$1" -j"$(nproc)" --target bitcoind_m4_measure
  # The node refuses every chain but regtest: check it on mainnet before anything else uses it.
  mkdir -p "$WORK/$1/guard-check"
  if timeout 60 "$WORK/$1/bin/bitcoind_m4_measure" -chain=main -datadir="$WORK/$1/guard-check" \
       > "$WORK/$1/guard-check.log" 2>&1 \
     || ! grep -q "runs on regtest only" "$WORK/$1/guard-check.log"; then
    echo "$1: bitcoind_m4_measure did not refuse mainnet as expected; refusing to continue" >&2
    exit 1
  fi
  echo "built $WORK/$1/bin/bitcoind_m4_measure (refuses mainnet: checked)"
}
build_node build-node -DSLH_MEASURE_COUNT=OFF
build_node build-count -DSLH_MEASURE_COUNT=ON

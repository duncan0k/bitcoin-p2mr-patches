#!/usr/bin/env bash
# Build the measurement-only binaries of M4-S (README.md): bench_bitcoin, and the measurement-only node
# bitcoind_m4_measure of the whole-block script. Neither is a node for any network, and neither can be
# installed.
#
# Usage: bash build.sh <core-tree> <work-dir>
#   <core-tree>  a Bitcoin Core v31.1 tree with every patch set up to patches-m4/ applied (as apply.sh
#                and contrib/k3s do it); it is not modified
#   <work-dir>   a new directory: a copy of the tree with measurement-build.patch applied, and two build
#                directories, build/ (bench_bitcoin) and build-node/ (bitcoind_m4_measure)
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
# A separate build directory for the measurement-only node: no wallet, no bench, no install rule.
# shellcheck disable=SC2086
cmake -S "$WORK" -B "$WORK/build-node" -DCMAKE_BUILD_TYPE=Release -DBUILD_BENCH=OFF -DENABLE_WALLET=OFF $OFF \
      -DSLH_MEASURE_WORST=ON -DSLH_MEASURE_NODE=ON
refuse_node_targets "$WORK/build-node"
cmake --build "$WORK/build-node" -j"$(nproc)" --target bitcoind_m4_measure
# The node refuses every chain but regtest: check it on mainnet before anything else uses it.
mkdir -p "$WORK/build-node/guard-check"
if timeout 60 "$WORK/build-node/bin/bitcoind_m4_measure" -chain=main -datadir="$WORK/build-node/guard-check" \
     > "$WORK/build-node/guard-check.log" 2>&1 \
   || ! grep -q "runs on regtest only" "$WORK/build-node/guard-check.log"; then
  echo "bitcoind_m4_measure did not refuse mainnet as expected; refusing to continue" >&2
  exit 1
fi
echo "built $WORK/build-node/bin/bitcoind_m4_measure (refuses mainnet: checked)"

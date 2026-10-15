#!/usr/bin/env bash
# Build the measurement-only bench_bitcoin of M4-S (README.md), never a node.
#
# Usage: bash build.sh <core-tree> <work-dir>
#   <core-tree>  a Bitcoin Core v31.1 tree with every patch set up to patches-m4/ applied (as apply.sh
#                and contrib/k3s do it); it is not modified
#   <work-dir>   a new directory: a copy of the tree with measurement-build.patch applied, and its build
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$1"; WORK="$2"
[ ! -e "$WORK" ] || { echo "$WORK exists; give a new directory" >&2; exit 1; }
git -C "$SRC" worktree add --detach "$WORK" HEAD
git -C "$WORK" apply "$HERE/measurement-build.patch"
# bench_bitcoin is the only executable: the patch's CMake refuses SLH_MEASURE_WORST if the configuration
# defines any other executable or shared library target (it checks the targets, not the options), since
# each would link the hooked library.
cmake -S "$WORK" -B "$WORK/build" -DCMAKE_BUILD_TYPE=Release -DBUILD_BENCH=ON -DENABLE_WALLET=ON \
      -DBUILD_BITCOIN_BIN=OFF -DBUILD_DAEMON=OFF -DBUILD_GUI=OFF -DENABLE_IPC=OFF -DBUILD_CLI=OFF \
      -DBUILD_TESTS=OFF -DBUILD_TX=OFF -DBUILD_UTIL=OFF -DBUILD_UTIL_CHAINSTATE=OFF -DBUILD_KERNEL_LIB=OFF \
      -DBUILD_WALLET_TOOL=OFF -DBUILD_FUZZ_BINARY=OFF -DBUILD_FOR_FUZZING=OFF -DWITH_ZMQ=OFF -DSLH_MEASURE_WORST=ON
if grep -Eq '^(BUILD_GUI|ENABLE_IPC|BUILD_DAEMON):BOOL=(ON|TRUE|1)$' "$WORK/build/CMakeCache.txt"; then
  echo "refusing: a node or GUI target is enabled in the measurement build" >&2
  exit 1
fi
cmake --build "$WORK/build" -j"$(nproc)" --target bench_bitcoin
echo "built $WORK/build/bin/bench_bitcoin"

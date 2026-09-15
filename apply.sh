#!/usr/bin/env bash
# Apply the P2MR (BIP-360) patch series on top of Bitcoin Core v31.1 and build it.
#
# Usage: bash apply.sh [target-dir]      (default: ./bitcoin)
# Requires: git, cmake >= 3.22, a C++20 compiler, libevent, boost headers, sqlite3 (wallet).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${1:-$HERE/bitcoin}"
BASE_TAG="v31.1"
BASE_COMMIT="9be056a"

if [ ! -d "$TARGET/.git" ]; then
  git clone --branch "$BASE_TAG" --depth 1 https://github.com/bitcoin/bitcoin.git "$TARGET"
fi
cd "$TARGET"
if [ "$(git rev-parse --short HEAD)" != "$BASE_COMMIT" ]; then
  echo "expected HEAD $BASE_COMMIT ($BASE_TAG), got $(git rev-parse --short HEAD)" >&2
  exit 1
fi

(cd "$HERE/patches" && sha256sum -c ../SHA256SUMS)
git checkout -q -B p2mr "$BASE_TAG"
git -c user.name=CipherScope -c user.email=dev@cipherscope.io am "$HERE"/patches/*.patch

cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_TESTS=ON -DENABLE_WALLET=ON \
      -DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF
cmake --build build -j"$(nproc)"

./build/bin/test_bitcoin --run_test=script_tests,script_standard_tests,transaction_tests,policy_tests
build/test/functional/test_runner.py -j4 feature_p2mr feature_p2mr_signet p2p_segwit feature_taproot
echo "P2MR patch series applied, built and verified in $TARGET"

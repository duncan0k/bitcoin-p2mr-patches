#!/usr/bin/env bash
# Apply the P2MR (BIP-360) patch series on top of Bitcoin Core v31.1 and build it.
#
# Usage: bash apply.sh [target-dir]      (default: ./bitcoin)
# Requires: git, cmake >= 3.22, a C++20 compiler, libevent, boost headers, sqlite3 (wallet).
#
# SERIES names the patch directories to apply, in order (default: "patches", the consensus
# series). Each is checked against its checksum file first (SHA256SUMS for patches/,
# SHA256SUMS-<name> for patches-<name>/). M4-T and M4-B are both built on M4-R, M4-B on top of
# M4-T: patches-m4b/ is applied only right after patches-m4t/. The whole tree with both:
#
#   SERIES="patches patches-m05 patches-spacing patches-m1 patches-m2 patches-m3 patches-m4 \
#           patches-m4r patches-m4t patches-m4b" bash apply.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${1:-$HERE/bitcoin}"
BASE_TAG="v31.1"
BASE_COMMIT="9be056a"

# The series, checked before anything is cloned, verified or applied: each name is canonical,
# "patches" or "patches-<name>" in lower-case letters, digits and hyphens, with no slash and no
# dot, so that "./patches-m4b" or "patches-m4b/" cannot name M4-B past the order check; and
# patches-m4b comes right after patches-m4t.
SERIES="${SERIES:-patches}"
previous=""
for dir in $SERIES; do
  if ! [[ "$dir" =~ ^(patches|patches-[a-z0-9][a-z0-9-]*)$ ]]; then
    echo "SERIES: '$dir' is not a series name (patches or patches-<name>, without a slash or a dot)" >&2
    exit 1
  fi
  if [ "$dir" = "patches-m4b" ] && [ "$previous" != "patches-m4t" ]; then
    echo "patches-m4b/ applies on top of patches-m4t/ only: list patches-m4t right before it" >&2
    exit 1
  fi
  previous="$dir"
done

if [ ! -d "$TARGET/.git" ]; then
  git clone --branch "$BASE_TAG" --depth 1 https://github.com/bitcoin/bitcoin.git "$TARGET"
fi
cd "$TARGET"
if [ "$(git rev-parse --short HEAD)" != "$BASE_COMMIT" ]; then
  echo "expected HEAD $BASE_COMMIT ($BASE_TAG), got $(git rev-parse --short HEAD)" >&2
  exit 1
fi

# Every file the checksum file lists matches, and git am below applies no .patch file that it does
# not list (one left behind by a renumbering, say).
for dir in $SERIES; do
  [ "$dir" = "patches" ] && sums="SHA256SUMS" || sums="SHA256SUMS-${dir#patches-}"
  (cd "$HERE/$dir" && sha256sum -c "../$sums")
  listed="$(sed -e 's/^[0-9a-f]\{64\} [ *]//' "$HERE/$sums")"
  for patch in "$HERE/$dir"/*.patch; do
    if ! printf '%s\n' "$listed" | grep -Fxq -- "$(basename "$patch")"; then
      echo "$dir/$(basename "$patch") is not listed in $sums: not applying $dir/" >&2
      exit 1
    fi
  done
done
git checkout -q -B p2mr "$BASE_TAG"
for dir in $SERIES; do
  git -c user.name=duncan0k -c user.email=dev@ark0.si am "$HERE/$dir"/*.patch
done

cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_TESTS=ON -DENABLE_WALLET=ON \
      -DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF
cmake --build build -j"$(nproc)"

./build/bin/test_bitcoin --run_test=script_tests,script_standard_tests,transaction_tests,policy_tests
build/test/functional/test_runner.py -j4 feature_p2mr feature_p2mr_signet p2p_segwit feature_taproot
echo "P2MR patch series applied, built and verified in $TARGET"

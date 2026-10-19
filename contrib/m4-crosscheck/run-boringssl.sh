#!/usr/bin/env bash
# Run the known-answer driver against BoringSSL and compare it, line by line, with the driver built from
# the vendored slhdsa-c files.
#
# Usage: bash run-boringssl.sh [work-dir]          (default: ./work)
#
# Environment:
#   CORE     a patched Bitcoin Core v31.1 tree, as for run.sh (cloned and patched into WORK/core if unset)
#   TARGETS  subset of: x86_64 aarch64 (default: both; aarch64 needs aarch64-linux-gnu gcc/g++ and qemu-user)
#
# Needs, in addition to what run.sh needs: cmake, ninja and git.
set -u

BSSL_COMMIT=437b75f34c3899230cb3f3fcf05543cb104faf2b   # 2026-10-15
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
WORK="${1:-$HERE/work}"
mkdir -p "$WORK"
WORK="$(cd "$WORK" && pwd)"
TARGETS="${TARGETS:-x86_64 aarch64}"

if [ -z "${CORE:-}" ]; then
  CORE="$WORK/core"
  if [ ! -d "$CORE/.git" ]; then
    git clone -q --branch v31.1 --depth 1 https://github.com/bitcoin/bitcoin.git "$CORE" || exit 2
  fi
  if [ ! -f "$CORE/src/crypto/slhdsa128s.cpp" ]; then
    (cd "$CORE" && git -c user.name=crosscheck -c user.email=crosscheck@localhost am -q \
      "$REPO"/patches/*.patch "$REPO"/patches-m05/*.patch "$REPO"/patches-spacing/*.patch \
      "$REPO"/patches-m1/*.patch "$REPO"/patches-m2/*.patch "$REPO"/patches-m3/*.patch \
      "$REPO"/patches-m4/*.patch) || exit 2
  fi
fi
SRC="$CORE/src"
VEND="$SRC/crypto/slhdsa128s"
python3 "$HERE/gen_vectors.py" "$SRC" "$WORK/vectors.txt" || exit 2

BSSL="$WORK/boringssl"
if [ ! -d "$BSSL/.git" ]; then
  mkdir -p "$BSSL" && git -C "$BSSL" init -q &&
    git -C "$BSSL" fetch -q --depth 1 https://boringssl.googlesource.com/boringssl "$BSSL_COMMIT" &&
    git -C "$BSSL" checkout -q FETCH_HEAD || exit 2
fi
[ "$(git -C "$BSSL" rev-parse HEAD)" = "$BSSL_COMMIT" ] || { echo "BoringSSL is not at $BSSL_COMMIT" >&2; exit 2; }
echo "BoringSSL $BSSL_COMMIT ($(git -C "$BSSL" log -1 --format=%cs))"

FAIL=0
REF=""
# one_target NAME CC CXX RUNNER CMAKE_SYSTEM_PROCESSOR
one_target() {
  local name="$1" cc="$2" cxx="$3" runner="$4" proc="$5"
  case " $TARGETS " in *" $name "*) ;; *) return ;; esac
  if ! command -v "$cc" >/dev/null || ! command -v "$cxx" >/dev/null || { [ -n "$runner" ] && ! command -v "${runner%% *}" >/dev/null; }; then
    echo "$name: skipped, tools not installed"; return
  fi
  local d="$WORK/bssl-$name"
  mkdir -p "$d"
  # The vendored driver at -O2, the reference.
  local obj=()
  for f in sha2_256.c sha2_512.c slh_dsa.c slh_sha2.c; do
    $cc -std=c11 -O2 -I"$VEND" -c "$VEND/$f" -o "$d/${f%.c}.o" || { FAIL=1; return; }
    obj+=("$d/${f%.c}.o")
  done
  $cc -std=c11 -O2 -I"$VEND" -c "$HERE/crosscheck.c" -o "$d/crosscheck.o" &&
  $cxx -std=c++20 -O2 -I"$SRC" -I"$VEND" -c "$HERE/shim.cpp" -o "$d/shim.o" &&
  $cxx -std=c++20 -O2 -I"$SRC" -I"$VEND" -c "$SRC/crypto/slhdsa128s.cpp" -o "$d/wrapper.o" &&
  $cxx "$d/crosscheck.o" "$d/shim.o" "$d/wrapper.o" "${obj[@]}" -o "$d/vendored" || { FAIL=1; return; }
  # BoringSSL's crypto library, then the driver against it.
  local tc=()
  if [ -n "$proc" ]; then
    printf 'set(CMAKE_SYSTEM_NAME Linux)\nset(CMAKE_SYSTEM_PROCESSOR %s)\nset(CMAKE_C_COMPILER %s)\nset(CMAKE_CXX_COMPILER %s)\n' \
      "$proc" "$cc" "$cxx" > "$d/toolchain.cmake"
    tc=(-DCMAKE_TOOLCHAIN_FILE="$d/toolchain.cmake")
  else
    tc=(-DCMAKE_C_COMPILER="$cc" -DCMAKE_CXX_COMPILER="$cxx")
  fi
  cmake -GNinja -S "$BSSL" -B "$d/build" -DCMAKE_BUILD_TYPE=Release "${tc[@]}" >"$d/cmake.log" 2>&1 &&
  ninja -C "$d/build" crypto >"$d/ninja.log" 2>&1 || { echo "$name: BoringSSL build failed, see $d/ninja.log"; FAIL=1; return; }
  $cc -std=c11 -O2 -DM4CC_BORINGSSL -I"$BSSL/include" -c "$HERE/crosscheck.c" -o "$d/bs-driver.o" &&
  $cxx -std=c++20 -O2 -I"$BSSL/include" -I"$BSSL" -c "$HERE/bssl_shim.cc" -o "$d/bs-shim.o" &&
  $cxx "$d/bs-driver.o" "$d/bs-shim.o" "$d/build/libcrypto.a" -o "$d/boringssl" -lpthread || { FAIL=1; return; }

  local v b
  for v in vendored boringssl; do
    # shellcheck disable=SC2086
    $runner "$d/$v" "$WORK/vectors.txt" > "$d/$v.out" 2> "$d/$v.err" || { echo "$name $v: exit status $?"; FAIL=1; }
    head -n -1 "$d/$v.out" > "$d/$v.lines"
  done
  [ -z "$REF" ] && REF="$d/vendored.lines"
  for v in vendored boringssl; do
    printf '%-8s %-10s -O2 %s lines=%s\n' "$name" "$v" "$(tail -n1 "$d/$v.out" | cut -d' ' -f1-4)" "$(wc -l < "$d/$v.lines")"
  done
  for v in vendored boringssl; do
    cmp -s "$d/$v.lines" "$REF" || { echo "$name $v: DIFFERENT from reference"; diff "$REF" "$d/$v.lines" | head -20; FAIL=1; }
  done
}

one_target x86_64 gcc g++ "" ""
one_target aarch64 aarch64-linux-gnu-gcc aarch64-linux-gnu-g++ "qemu-aarch64 -L /usr/aarch64-linux-gnu" aarch64
if [ "$FAIL" -ne 0 ]; then echo "FAILED"; exit 1; fi
echo "BoringSSL and the vendored verifier agree on every line"

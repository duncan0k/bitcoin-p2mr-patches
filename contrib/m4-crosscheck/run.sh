#!/usr/bin/env bash
# Run the SLH-DSA-SHA2-128s known-answer driver on every target of the matrix and compare the results.
#
# Usage: bash run.sh [work-dir]          (default: ./work, created if missing)
#
# Environment:
#   CORE   a Bitcoin Core v31.1 tree with all patch sets applied (as README.md of the patch repository
#          says). If unset, v31.1 is cloned into WORK/core and the patches are applied there with git am.
#   TARGETS  space-separated subset of: x86_64-gcc x86_64-clang i386-gcc aarch64 armhf s390x
#
# Needs: bash, git, python3, sha256sum, gcc, and per target as available: clang, gcc-multilib and
# g++-multilib, {aarch64,arm,s390x}-linux-gnu gcc and g++, qemu-user. A target whose tools are missing is
# reported as skipped.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
WORK="${1:-$HERE/work}"
mkdir -p "$WORK"
WORK="$(cd "$WORK" && pwd)"
# A 32-bit g++ on a 64-bit Debian or Ubuntu host may not find the kernel headers of <cerrno>.
CXXEXTRA=""
[ -d /usr/include/asm ] || [ ! -d /usr/include/x86_64-linux-gnu/asm ] || CXXEXTRA_I386="-idirafter /usr/include/x86_64-linux-gnu"
TARGETS="${TARGETS:-x86_64-gcc x86_64-clang i386-gcc aarch64 armhf s390x}"

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
FILES=(sha2_256.c sha2_512.c slh_dsa.c slh_sha2.c)
python3 "$HERE/gen_vectors.py" "$SRC" "$WORK/vectors.txt" || exit 2
echo "vectors: $(wc -l < "$WORK/vectors.txt") (before bit flips); Core tree: $(git -C "$CORE" rev-parse --short HEAD)"

MATRIX="$WORK/matrix.txt"
: > "$MATRIX"
FAIL=0
REF_LINES=""
REF_DIGEST=""

# run_config NAME CC CXX "ARCH FLAGS" "RUNNER" WARNFLAG OPT SAN
run_config() {
  local name="$1" cc="$2" cxx="$3" arch="$4" runner="$5" warn="$6" opt="$7" san="$8"
  local tag="$name-O$opt${san:+-ubsan}"
  local dir="$WORK/$tag"
  rm -rf -- "${dir:?}"; mkdir -p "$dir"
  local sanflags=""
  [ -n "$san" ] && sanflags="-fsanitize=undefined -fno-sanitize-recover=all"
  local obj=()
  for f in "${FILES[@]}"; do
    # shellcheck disable=SC2086
    $cc $arch -std=c11 -O$opt $sanflags -Wall -Wextra $warn -I"$VEND" -c "$VEND/$f" -o "$dir/${f%.c}.o" 2>>"$dir/vendored.log" || { echo "$tag: build failed" >> "$MATRIX"; FAIL=1; return; }
    obj+=("$dir/${f%.c}.o")
  done
  # shellcheck disable=SC2086
  $cc $arch -std=c11 -O$opt $sanflags -Wall -Wextra -I"$VEND" -c "$HERE/crosscheck.c" -o "$dir/crosscheck.o" 2>"$dir/driver.log" &&
  $cxx $arch -std=c++20 -O$opt $sanflags -Wall -Wextra $CXXEXTRA -I"$SRC" -I"$VEND" -c "$HERE/shim.cpp" -o "$dir/shim.o" 2>>"$dir/driver.log" &&
  $cxx $arch -std=c++20 -O$opt $sanflags -Wall -Wextra $CXXEXTRA -I"$SRC" -I"$VEND" -c "$SRC/crypto/slhdsa128s.cpp" -o "$dir/wrapper.o" 2>>"$dir/driver.log" &&
  $cxx $arch $sanflags "$dir/crosscheck.o" "$dir/shim.o" "$dir/wrapper.o" "${obj[@]}" -o "$dir/crosscheck" 2>>"$dir/driver.log" ||
    { echo "$tag: build failed (see $dir/driver.log)" | tee -a "$MATRIX"; FAIL=1; return; }

  # shellcheck disable=SC2086
  $runner "$dir/crosscheck" "$WORK/vectors.txt" > "$dir/out.txt" 2> "$dir/run.log"
  local rc=$?
  local digest lines vecs mism
  digest="$(tail -n1 "$dir/out.txt" | awk '{print $2}')"
  head -n -1 "$dir/out.txt" > "$dir/lines.txt"
  local check
  check="$(sha256sum "$dir/lines.txt" | awk '{print $1}')"
  vecs="$(wc -l < "$dir/lines.txt")"
  mism="$(tail -n1 "$dir/out.txt" | awk '{print $NF}')"
  local nwarn nrt
  nwarn="$(grep -c 'warning:' "$dir/vendored.log")"
  nrt="$(grep -c 'runtime error' "$dir/run.log")"
  local status=ok
  [ "$rc" -ne 0 ] && status="exit $rc"
  [ "$digest" != "$check" ] && status="$status; digest differs from sha256sum"
  [ "$mism" != "0" ] && status="$status; $mism mismatches"
  if [ -z "$REF_DIGEST" ]; then REF_DIGEST="$digest"; REF_LINES="$dir/lines.txt"; fi
  if [ "$digest" != "$REF_DIGEST" ] || ! cmp -s "$dir/lines.txt" "$REF_LINES"; then status="$status; differs from first run"; fi
  [ "$status" != ok ] && FAIL=1
  printf '%-14s %-8s %-3s %-6s vectors=%-3s warnings=%-2s ubsan_reports=%-2s %s  %s\n' \
    "$name" "$(echo "$cc" | awk '{print $1}')" "-O$opt" "${san:+ubsan}" "$vecs" "$nwarn" "$nrt" "$digest" "$status" | tee -a "$MATRIX"
  [ "$nrt" -ne 0 ] && grep 'runtime error' "$dir/run.log" | head -5
  return 0
}

# have_ubsan CC CXX ARCH RUNNER: can a program be built with UBSan, linked and run on this target?
have_ubsan() {
  local cc="$1" arch="$3" runner="$4"
  local t="$WORK/ubsan-probe"
  echo 'int main(void){return 0;}' > "$t.c"
  # shellcheck disable=SC2086
  $cc $arch -fsanitize=undefined -fno-sanitize-recover=all "$t.c" -o "$t" 2>/dev/null && $runner "$t" 2>/dev/null
}

# target NAME CC CXX ARCH RUNNER WARNFLAG
target() {
  local name="$1" cc="$2" cxx="$3" arch="$4" runner="$5" warn="$6"
  case " $TARGETS " in *" $name "*) ;; *) return ;; esac
  if ! command -v "${cc%% *}" >/dev/null || ! command -v "${cxx%% *}" >/dev/null; then
    echo "$name: skipped, ${cc%% *} or ${cxx%% *} not installed" | tee -a "$MATRIX"; return
  fi
  if [ -n "$runner" ] && ! command -v "${runner%% *}" >/dev/null; then
    echo "$name: skipped, ${runner%% *} not installed" | tee -a "$MATRIX"; return
  fi
  # A 32-bit or cross target needs its C library and libstdc++.
  echo 'int main(void){return 0;}' > "$WORK/probe.c"
  # shellcheck disable=SC2086
  if ! $cc $arch "$WORK/probe.c" -o "$WORK/probe" 2>/dev/null || ! $runner "$WORK/probe" 2>/dev/null; then
    echo "$name: skipped, cannot build and run a C program with: $cc $arch" | tee -a "$MATRIX"; return
  fi
  CXXEXTRA=""
  [ "$name" = i386-gcc ] && CXXEXTRA="${CXXEXTRA_I386:-}"
  local ub=""
  have_ubsan "$cc" "$cxx" "$arch" "$runner" && ub=1
  [ -z "$ub" ] && echo "$name: UBSan not available with $cc" | tee -a "$MATRIX"
  for opt in 0 3; do
    run_config "$name" "$cc" "$cxx" "$arch" "$runner" "$warn" "$opt" ""
    [ -n "$ub" ] && run_config "$name" "$cc" "$cxx" "$arch" "$runner" "$warn" "$opt" 1
  done
}

GCCW="-Wcast-align=strict"
CLANGW="-Wcast-align"
target x86_64-gcc   gcc   g++     ""        ""                                      "$GCCW"
target x86_64-clang clang clang++ ""        ""                                      "$CLANGW"
target i386-gcc     gcc   g++     "-m32"    ""                                      "$GCCW"
target aarch64      aarch64-linux-gnu-gcc     aarch64-linux-gnu-g++     "" "qemu-aarch64 -L /usr/aarch64-linux-gnu"   "$GCCW"
target armhf        arm-linux-gnueabihf-gcc   arm-linux-gnueabihf-g++   "" "qemu-arm -L /usr/arm-linux-gnueabihf"     "$GCCW"
target s390x        s390x-linux-gnu-gcc       s390x-linux-gnu-g++       "" "qemu-s390x -L /usr/s390x-linux-gnu"       "$GCCW"

echo
echo "Warnings of the vendored files (-Wall -Wextra $GCCW / $CLANGW), per configuration:"
cat "$WORK"/*/vendored.log 2>/dev/null | grep 'warning:' | sort | uniq -c | head -50
echo "matrix written to $MATRIX"
if [ "$FAIL" -ne 0 ]; then echo "FAILED"; exit 1; fi
echo "all configurations agree"

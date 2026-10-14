#!/bin/sh
# Combine the two macOS bitcoind builds without changing either slice's signed bytes.
set -eu
[ "$#" = 3 ] || { echo "usage: $0 ARM64_BITCOIND X86_64_BITCOIND OUTPUT" >&2; exit 1; }
if command -v llvm-lipo-19 > /dev/null 2>&1; then
    LIPO=llvm-lipo-19
elif command -v llvm-lipo > /dev/null 2>&1; then
    LIPO=llvm-lipo
else
    echo "llvm-lipo-19 or llvm-lipo is required" >&2
    exit 1
fi
arm=$(realpath -e -- "$1")
intel=$(realpath -e -- "$2")
out=$(realpath -m -- "$3")
[ "$out" != "$arm" ] && [ "$out" != "$intel" ] \
    || { echo "output must differ from both inputs" >&2; exit 1; }
check_header() {
    header=$(od -An -v -tx1 -N16 -- "$1" | tr -d '[:space:]')
    case "$header" in
        cffaedfe"$2"????????02000000) ;;
        *) echo "$1 must be a thin 64-bit $3 Mach-O executable" >&2; exit 1 ;;
    esac
}
check_header "$arm" 0c000001 arm64
check_header "$intel" 07000001 x86_64
arm_archs=$("$LIPO" -archs "$arm")
intel_archs=$("$LIPO" -archs "$intel")
[ "$(printf '%s\n' "$arm_archs" | xargs)" = arm64 ] || { echo "first input must contain only arm64" >&2; exit 1; }
[ "$(printf '%s\n' "$intel_archs" | xargs)" = x86_64 ] || { echo "second input must contain only x86_64" >&2; exit 1; }
suffix=${LIPO#llvm-lipo}
if command -v "llvm-objdump$suffix" > /dev/null 2>&1; then
    headers=$("llvm-objdump$suffix" --macho --private-headers "$arm")
elif command -v "llvm-otool$suffix" > /dev/null 2>&1; then
    headers=$("llvm-otool$suffix" -l "$arm")
else
    echo "llvm-objdump$suffix or llvm-otool$suffix is required" >&2
    exit 1
fi
printf '%s\n' "$headers" | grep -q '^[[:space:]]*cmd LC_CODE_SIGNATURE$' \
    || { echo "arm64 input has no LC_CODE_SIGNATURE" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' 0
trap 'exit 130' INT
trap 'exit 143' TERM
"$LIPO" -create "$arm" "$intel" -output "$out"
info=$("$LIPO" -info "$out")
printf '%s\n' "$info"
archs=$(printf '%s\n' "$info" | sed -n 's/^Architectures in the fat file: .* are: //p' | xargs)
case "$archs" in
    'arm64 x86_64'|'x86_64 arm64') ;;
    *) echo "output must contain exactly arm64 and x86_64" >&2; exit 1 ;;
esac
"$LIPO" -thin arm64 "$out" -output "$tmp/arm64"
"$LIPO" -thin x86_64 "$out" -output "$tmp/x86_64"
cmp "$arm" "$tmp/arm64"
cmp "$intel" "$tmp/x86_64"
chmod 0755 "$out"
sha256sum "$out"

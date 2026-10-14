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
arm_archs=$("$LIPO" -archs "$arm")
intel_archs=$("$LIPO" -archs "$intel")
[ "$(printf '%s\n' "$arm_archs" | xargs)" = arm64 ] || { echo "first input must contain only arm64" >&2; exit 1; }
[ "$(printf '%s\n' "$intel_archs" | xargs)" = x86_64 ] || { echo "second input must contain only x86_64" >&2; exit 1; }
"$LIPO" -create "$arm" "$intel" -output "$out"
info=$("$LIPO" -info "$out")
printf '%s\n' "$info"
archs=$(printf '%s\n' "$info" | sed -n 's/^Architectures in the fat file: .* are: //p' | xargs)
case "$archs" in
    'arm64 x86_64'|'x86_64 arm64') ;;
    *) echo "output must contain exactly arm64 and x86_64" >&2; exit 1 ;;
esac
chmod 0755 "$out"
sha256sum "$out"

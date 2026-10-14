#!/usr/bin/env bash
# Cross-compile the Ark-0 M3 node, as root in a fresh debian:trixie (macOS) or ubuntu:22.04 (Linux)
# container: Bitcoin Core v31.1 with the six patch sets of the published M3 release (tag ark0-node-m3),
# applied as JOIN.md applies them, built with Core's own depends system and its CMake toolchain file.
# No GUI, wallet, ZMQ, IPC or USDT: the witness program only talks RPC.
#
# TARGET selects macos-arm64, macos-x86_64, linux-x86_64 or linux-aarch64. PHASE=build builds and hashes only.
# The build lives under /work, which should start empty, the source always at /work/src, since the binaries
# carry source paths. Results go to /work/out; packages.txt lists every installed package's version,
# which a later build needs to match it. The Apple SDK stays outside out because it is not redistributable.
set -euo pipefail
PHASE="${PHASE:-build}"
TARGET="${TARGET:-}"
JOBS="${JOBS:-8}"
W=/work
OUT=$W/out
SRC=$W/src
CORE_TAG=v31.1
CORE_COMMIT=9be056a8a72b624dae9623b2f7bded92c2a21c91
PATCH_TAG=ark0-node-m3
PATCH_COMMIT=c0015ecf9a7c02415c88c9371e99dbe2f7e46ef2
SDK_FILE=Xcode-26.1.1-17B100-extracted-SDK-with-libcxx-headers.tar
SDK_SHA256=9600fa93644df674ee916b5e2c8a6ba8dacf631996a65dc922d003b98b5ea3b1
SDK_BYTES=204748800
SDK_PATH=$W/SDKs
mkdir -p "$OUT"
exec > >(tee "$OUT/run.log") 2>&1
log_pid=$!
say() { echo "==> $(date -u +%H:%M:%SZ) $*"; }
since() { echo "$(( $(date +%s) - $1 ))s"; }
finish() {
    rc=$?
    trap - EXIT
    exec 1>&- 2>&-
    wait "$log_pid" || rc=$?
    printf '%s\n' "$rc" > "$OUT/EXIT"
    exit "$rc"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

[ "$PHASE" = build ] || { echo "PHASE must be build" >&2; exit 1; }
[[ "$JOBS" =~ ^[1-9][0-9]*$ ]] || { echo "JOBS must be a positive integer" >&2; exit 1; }
[ "$(id -u)" = 0 ] || { echo "run as root in a fresh container" >&2; exit 1; }
. /etc/os-release
PKGS=(ca-certificates curl git make cmake pkgconf python3 patch xz-utils bzip2)
DEPENDS_ARGS=()
CMAKE_ARGS=()
case "$TARGET" in
    macos-arm64|macos-x86_64)
        [ "$ID" = debian ] && [ "$VERSION_CODENAME" = trixie ] \
            || { echo "macOS targets require debian:trixie" >&2; exit 1; }
        if [ "$TARGET" = macos-arm64 ]; then HOST_TRIPLET=arm64-apple-darwin; else HOST_TRIPLET=x86_64-apple-darwin; fi
        PKGS+=(clang lld llvm)
        DEPENDS_ARGS+=("SDK_PATH=$SDK_PATH" build_CC=clang build_CXX=clang++
                       build_AR=llvm-ar build_RANLIB=llvm-ranlib build_NM=llvm-nm build_STRIP=llvm-strip)
        # Depends disables ad hoc signatures for release signing. The last linker flag restores them so
        # Apple Silicon can run these binaries, and keeping the linked bytes preserves their signatures.
        CMAKE_ARGS+=(-DAPPEND_LDFLAGS=-Wl,-adhoc_codesign)
        ;;
    linux-x86_64|linux-aarch64)
        [ "$ID" = ubuntu ] && [ "$VERSION_ID" = 22.04 ] \
            || { echo "Linux targets require ubuntu:22.04" >&2; exit 1; }
        [ "$(uname -m)" = x86_64 ] || { echo "Linux builds require an amd64 container" >&2; exit 1; }
        PKGS+=(gcc-12 g++-12 binutils)
        if [ "$TARGET" = linux-x86_64 ]; then
            HOST_TRIPLET=x86_64-pc-linux-gnu
            CC=gcc-12
            CXX=g++-12
            TOOL_PREFIX=
        else
            HOST_TRIPLET=aarch64-linux-gnu
            CC=aarch64-linux-gnu-gcc-12
            CXX=aarch64-linux-gnu-g++-12
            TOOL_PREFIX=aarch64-linux-gnu-
            PKGS+=(gcc-12-aarch64-linux-gnu g++-12-aarch64-linux-gnu binutils-aarch64-linux-gnu)
        fi
        AR=${TOOL_PREFIX}ar
        RANLIB=${TOOL_PREFIX}ranlib
        STRIP=${TOOL_PREFIX}strip
        NM=${TOOL_PREFIX}nm
        OBJDUMP=${TOOL_PREFIX}objdump
        DEPENDS_ARGS+=("CC=$CC" "CXX=$CXX" "AR=$AR" "RANLIB=$RANLIB" "STRIP=$STRIP" "NM=$NM"
                       "OBJDUMP=$OBJDUMP" "OBJCOPY=${TOOL_PREFIX}objcopy" build_CC=gcc-12 build_CXX=g++-12)
        # Static C++ and GCC runtimes leave only the distribution's glibc libraries to be supplied at run time.
        CMAKE_ARGS+=("-DCMAKE_C_COMPILER=$CC" "-DCMAKE_CXX_COMPILER=$CXX"
                    "-DAPPEND_LDFLAGS=-static-libstdc++ -static-libgcc")
        ;;
    *) echo "TARGET must be macos-arm64, macos-x86_64, linux-x86_64 or linux-aarch64" >&2; exit 1 ;;
esac

say "target $TARGET ($HOST_TRIPLET), phase $PHASE, $JOBS jobs"
export DEBIAN_FRONTEND=noninteractive SOURCE_DATE_EPOCH=1 LANG=C.UTF-8 LC_ALL=C.UTF-8 TZ=UTC
umask 022
t0=$(date +%s)
apt-get update -qq
apt-get install -y -qq --no-install-recommends "${PKGS[@]}" > /dev/null
say "packages installed in $(since "$t0")"
{
    echo "image: $PRETTY_NAME"
    echo "target: $TARGET ($HOST_TRIPLET)"
    echo "SOURCE_DATE_EPOCH: $SOURCE_DATE_EPOCH"
    if [[ "$TARGET" = macos-* ]]; then
        echo "clang: $(clang --version | sed -n '1p')"
        echo "clang++: $(clang++ --version | sed -n '1p')"
        echo "lld: $(ld.lld --version)"
        echo "Mach-O lld: $(ld64.lld --version)"
        echo "llvm: $(llvm-objdump --version | sed -n '1p')"
        echo "SDK file: $SDK_FILE"
        echo "SDK SHA-256: $SDK_SHA256"
        echo "SDK bytes: $SDK_BYTES"
    else
        echo "C compiler: $($CC --version | sed -n '1p')"
        echo "C++ compiler: $($CXX --version | sed -n '1p')"
        echo "build compiler: $(g++-12 --version | sed -n '1p')"
        echo "binutils: $(${TOOL_PREFIX}ld --version | sed -n '1p')"
        echo "strip: $($STRIP --version | sed -n '1p')"
        echo "glibc: $(getconf GNU_LIBC_VERSION)"
    fi
    echo "cmake: $(cmake --version | sed -n '1p')"
} | tee "$OUT/toolchain.txt"
dpkg-query -W -f='${Package}=${Version}\n' > "$OUT/packages.txt"

say "cloning Core $CORE_TAG and the patch repository at $PATCH_TAG"
git clone -q --branch "$CORE_TAG" --depth 1 https://github.com/bitcoin/bitcoin.git "$SRC"
core_head=$(git -C "$SRC" rev-parse HEAD)
[ "$core_head" = "$CORE_COMMIT" ] || { echo "expected $CORE_TAG at $CORE_COMMIT, got $core_head" >&2; exit 1; }
git clone -q --branch "$PATCH_TAG" --depth 1 https://github.com/duncan0k/bitcoin-p2mr-patches.git "$W/patches"
patch_head=$(git -C "$W/patches" rev-parse HEAD)
[ "$patch_head" = "$PATCH_COMMIT" ] || { echo "expected $PATCH_TAG at $PATCH_COMMIT, got $patch_head" >&2; exit 1; }
echo "core $core_head, patches $patch_head" | tee "$OUT/sources.txt"
cd "$W/patches"
for pair in patches:SHA256SUMS patches-m05:SHA256SUMS-m05 patches-spacing:SHA256SUMS-spacing \
            patches-m1:SHA256SUMS-m1 patches-m2:SHA256SUMS-m2 patches-m3:SHA256SUMS-m3; do
    ( cd "${pair%%:*}" && sha256sum --quiet -c "../${pair#*:}" ) || { echo "checksums of ${pair%%:*} do not match" >&2; exit 1; }
done
say "all six patch sets match their SHA256SUMS"
cd "$SRC"
# The committer date taken from each patch makes HEAD the same in every build of this script.
git -c user.name=duncan0k -c user.email=dev@ark0.si am -q --committer-date-is-author-date \
    "$W"/patches/patches/*.patch "$W"/patches/patches-m05/*.patch "$W"/patches/patches-spacing/*.patch \
    "$W"/patches/patches-m1/*.patch "$W"/patches/patches-m2/*.patch "$W"/patches/patches-m3/*.patch
echo "applied $(git rev-list --count "$core_head"..HEAD) patches, HEAD $(git rev-parse HEAD)" | tee -a "$OUT/sources.txt"

if [[ "$TARGET" = macos-* ]]; then
    grep -qx 'XCODE_VERSION=26.1.1' depends/hosts/darwin.mk
    grep -qx 'XCODE_BUILD_ID=17B100' depends/hosts/darwin.mk
    say "downloading and checking $SDK_FILE"
    mkdir -p "$SDK_PATH"
    curl -fL "https://bitcoincore.org/depends-sources/sdks/$SDK_FILE" -o "$W/$SDK_FILE"
    [ "$(stat -c %s "$W/$SDK_FILE")" = "$SDK_BYTES" ] || { echo "SDK size does not match" >&2; exit 1; }
    printf '%s  %s\n' "$SDK_SHA256" "$W/$SDK_FILE" | sha256sum -c -
    tar -xf "$W/$SDK_FILE" -C "$SDK_PATH"
    [ -d "$SDK_PATH/${SDK_FILE%.tar}/usr/include/c++/v1" ] || { echo "SDK layout does not match depends" >&2; exit 1; }
fi

say "depends for $HOST_TRIPLET"
t0=$(date +%s)
make -C depends -j"$JOBS" HOST="$HOST_TRIPLET" NO_QT=1 NO_QR=1 NO_WALLET=1 NO_ZMQ=1 NO_USDT=1 NO_IPC=1 \
    "${DEPENDS_ARGS[@]}" > "$OUT/depends.log" 2>&1 || { tail -40 "$OUT/depends.log"; exit 1; }
say "depends done in $(since "$t0")"

say "configure"
t0=$(date +%s)
cmake -B build --toolchain "depends/$HOST_TRIPLET/toolchain.cmake" \
    -DCMAKE_BUILD_TYPE=Release -DBUILD_TESTS=ON -DBUILD_BENCH=OFF -DBUILD_FUZZ_BINARY=OFF \
    -DENABLE_WALLET=OFF -DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF -DWITH_USDT=OFF \
    "${CMAKE_ARGS[@]}" > "$OUT/configure.log" 2>&1 || { tail -40 "$OUT/configure.log"; exit 1; }
say "build"
cmake --build build -j"$JOBS" --target bitcoind test_bitcoin > "$OUT/build.log" 2>&1 \
    || { tail -60 "$OUT/build.log"; exit 1; }
say "configure and build done in $(since "$t0")"

for b in bitcoind test_bitcoin; do
    install -m 0755 "build/bin/$b" "$OUT/$b"
done
cd "$OUT"
if [[ "$TARGET" = macos-* ]]; then
    # Stripping would change the signed bytes, so these are the binaries exactly as lld linked them.
    for b in bitcoind test_bitcoin; do
        headers=$(llvm-objdump --macho --private-headers "$b")
        signature=no
        if grep -q '^[[:space:]]*cmd LC_CODE_SIGNATURE$' <<< "$headers"; then signature=yes; fi
        echo "$b LC_CODE_SIGNATURE: $signature" | tee -a toolchain.txt
        if [ "$TARGET" = macos-arm64 ] && [ "$b" = bitcoind ] && [ "$signature" != yes ]; then
            echo "arm64 bitcoind has no ad hoc signature" >&2
            exit 1
        fi
    done
    sha256sum bitcoind test_bitcoin | tee SHA256SUMS
else
    for b in bitcoind test_bitcoin; do
        symbols=$($OBJDUMP -T "$b")
        highest=$(printf '%s\n' "$symbols" | grep -oE 'GLIBC_[0-9]+(\.[0-9]+)+' | sort -Vu | tail -n 1)
        needed=$($OBJDUMP -p "$b" | awk '$1 == "NEEDED" { print $2 }')
        echo "$b highest GLIBC symbol version: $highest" | tee -a toolchain.txt
        printf '%s NEEDED:\n%s\n' "$b" "$needed" | tee -a toolchain.txt
        [ "$(printf '%s\n' GLIBC_2.35 "$highest" | sort -V | tail -n 1)" = GLIBC_2.35 ] \
            || { echo "$b requires glibc newer than 2.35" >&2; exit 1; }
        [ -n "$needed" ] || { echo "$b has no dynamic glibc dependencies" >&2; exit 1; }
        while IFS= read -r lib; do
            case "$lib" in
                libc.so.6|libm.so.6|libpthread.so.0|librt.so.1|libdl.so.2|ld-linux-x86-64.so.2|ld-linux-aarch64.so.1) ;;
                *) echo "$b needs a non-glibc runtime library: $lib" >&2; exit 1 ;;
            esac
        done <<< "$needed"
    done
    # Keep the linked binaries too; SOURCE_DATE_EPOCH also fixes any timestamp strip might otherwise write.
    mkdir -p stripped
    for b in bitcoind test_bitcoin; do "$STRIP" -o "stripped/$b" "$b"; done
    sha256sum bitcoind test_bitcoin stripped/bitcoind stripped/test_bitcoin | tee SHA256SUMS
    ( cd stripped && sha256sum bitcoind test_bitcoin > SHA256SUMS )
fi
sha256sum -c SHA256SUMS
say "done"

#!/usr/bin/env bash
# Cross-compile the Ark-0 node for Windows (x86_64-w64-mingw32), as root in a fresh ubuntu:24.04 container
# or machine: Bitcoin Core v31.1 (9be056a) with the eight patch sets up to the M4-R series (patches-m4r)
# applied as JOIN.md applies them, built with Core's own depends system and its CMake toolchain file
# (doc/build-windows.md), and the rescue verifier of patches-m4r with the Rust toolchain that series pins.
# The patch repository is the development branch m4r-dev, not published yet, from the git bundle the pod
# mounts at /in. No GUI, wallet, ZMQ, IPC or USDT: the witness program only talks RPC.
#
# PHASE=full  also runs test_bitcoin.exe and an end-to-end sync of bitcoind.exe under Wine.
# PHASE=build builds and hashes only (the second, clean build of the determinism check).
#
# The build lives under /work, which should start empty, the source always at /work/src, since the binaries carry
# source paths (PHASE=full also lets Wine keep its server's socket under /tmp). Both repositories must be at the
# commits below, or the script stops before it builds. Results go to /work/out: stripped/bitcoind.exe is the
# bitcoind.exe of the witness program's release (witness/README.md); toolchain.txt names the toolchain, and
# packages.txt lists every installed package's version, which a later build needs to match it.
set -euo pipefail
PHASE="${PHASE:-full}"
JOBS="${JOBS:-8}"
W=/work
OUT=$W/out
SRC=$W/src
CORE_TAG=v31.1
CORE_COMMIT=9be056a8a72b624dae9623b2f7bded92c2a21c91
PATCH_REPO=/in/bitcoin-p2mr-patches.bundle
PATCH_TAG=m4r-dev
PATCH_COMMIT=8632d36a0ff733613ecb238f59914c54ebc6ffb6
PATCH_SETS=(patches patches-m05 patches-spacing patches-m1 patches-m2 patches-m3 patches-m4 patches-m4r)
# The tree the patch sets give on top of CORE_COMMIT: the reviewed series (0001-0088).
SERIES_TREE=09b8afcca5d28a8bff876c9d6de9b850d0963b70
HOST_TRIPLET=x86_64-w64-mingw32
RUST_VERSION=1.97.0
RUST_TARGET=x86_64-pc-windows-gnu
RUST_PREFIX=/opt/rust
# xz_hash of each component in channel-rust-1.97.0.toml, whose signature is by the Rust release key
# 108F 6620 5EAE B0AA A8DD 5E1C 85AB 96E6 FA1B E5FE.
declare -A RUST_SHA256=(
    [rustc:x86_64-unknown-linux-gnu]=0a8787303c88b018af61b5c53a0c7024d516d175e623eeab35a35eab11dbcad0
    [cargo:x86_64-unknown-linux-gnu]=5c5bc94af02797b699bb7d4e85b356192eab9fb50376053a1e6edf509fcf7129
    [rust-std:x86_64-unknown-linux-gnu]=0c43618128a7a6b96b0b04c04cf7b28dd52fb0991a74c9615fbd1a684ff88712
    [rust-std:x86_64-pc-windows-gnu]=98e6ef703b003b1f7d75c3249a831f4ac07f0deb41b0e1dfe41054847ac2816f
)
mkdir -p "$OUT"
exec > >(tee -a "$OUT/run.log") 2>&1
say() { echo "==> $(date -u +%H:%M:%SZ) $*"; }
since() { echo "$(( $(date +%s) - $1 ))s"; }

say "phase $PHASE, $JOBS jobs"
export DEBIAN_FRONTEND=noninteractive
t0=$(date +%s)
apt-get update -qq
PKGS="ca-certificates curl git make cmake g++ pkg-config python3 xz-utils bzip2 patch file g++-mingw-w64-x86-64-posix"
[ "$PHASE" = full ] && PKGS="$PKGS wine64"
apt-get install -y -qq --no-install-recommends $PKGS > /dev/null
say "packages installed in $(since "$t0")"
{
    echo "image: $(. /etc/os-release && echo "$PRETTY_NAME")"
    echo "mingw g++: $(x86_64-w64-mingw32-g++ --version | head -1)"
    echo "mingw thread model: $(x86_64-w64-mingw32-g++ -v 2>&1 | grep -o 'Thread model: [a-z0-9]*')"
    echo "mingw-w64 packages: $(dpkg-query -W -f='${Package}=${Version} ' 'mingw-w64*' 'g++-mingw-w64-x86-64-posix' 'gcc-mingw-w64-x86-64-posix-runtime' 2>/dev/null)"
    echo "binutils: $(x86_64-w64-mingw32-ld --version | head -1)"
    echo "cmake: $(cmake --version | head -1)"
    if [ "$PHASE" = full ]; then echo "wine: $(dpkg-query -W -f='${Version}' wine64 2>/dev/null)"; fi
} | tee "$OUT/toolchain.txt"
dpkg-query -W -f='${Package}=${Version}\n' > "$OUT/packages.txt"
x86_64-w64-mingw32-g++ -v 2>&1 | grep -q 'Thread model: posix' || { echo "the mingw g++ is not the POSIX-thread one" >&2; exit 1; }

say "Rust $RUST_VERSION for $RUST_TARGET"
t0=$(date +%s)
mkdir -p "$W/rust-dist"
for c in rustc:x86_64-unknown-linux-gnu cargo:x86_64-unknown-linux-gnu rust-std:x86_64-unknown-linux-gnu "rust-std:$RUST_TARGET"; do
    f="${c%%:*}-$RUST_VERSION-${c#*:}"
    curl -fsSL --retry 5 --retry-all-errors "https://static.rust-lang.org/dist/$f.tar.xz" -o "$W/rust-dist/$f.tar.xz"
    printf '%s  %s\n' "${RUST_SHA256[$c]}" "$W/rust-dist/$f.tar.xz" | sha256sum --quiet -c - \
        || { echo "$f.tar.xz does not match its hash" >&2; exit 1; }
    tar -xJf "$W/rust-dist/$f.tar.xz" -C "$W/rust-dist"
    "$W/rust-dist/$f/install.sh" --prefix="$RUST_PREFIX" --disable-ldconfig > /dev/null
done
rm -rf "$W/rust-dist"
{
    echo "rustc: $("$RUST_PREFIX/bin/rustc" -V)"
    echo "cargo: $("$RUST_PREFIX/bin/cargo" -V)"
    echo "Rust target: $RUST_TARGET"
} | tee -a "$OUT/toolchain.txt"
say "Rust installed in $(since "$t0")"

say "cloning Core $CORE_TAG and the patch repository at $PATCH_TAG"
git clone -q --branch "$CORE_TAG" --depth 1 https://github.com/bitcoin/bitcoin.git "$SRC"
core_head=$(git -C "$SRC" rev-parse HEAD)
[ "$core_head" = "$CORE_COMMIT" ] || { echo "expected $CORE_TAG at $CORE_COMMIT, got $core_head" >&2; exit 1; }
git clone -q --branch "$PATCH_TAG" "$PATCH_REPO" "$W/patches"
patch_head=$(git -C "$W/patches" rev-parse HEAD)
[ "$patch_head" = "$PATCH_COMMIT" ] || { echo "expected $PATCH_TAG at $PATCH_COMMIT, got $patch_head" >&2; exit 1; }
echo "core $core_head, patches $patch_head" | tee "$OUT/sources.txt"
cd "$W/patches"
patch_files=()
for set in "${PATCH_SETS[@]}"; do
    ( cd "$set" && sha256sum --quiet -c "../SHA256SUMS${set#patches}" ) || { echo "checksums of $set do not match" >&2; exit 1; }
    patch_files+=("$W/patches/$set"/*.patch)
done
say "all ${#PATCH_SETS[@]} patch sets match their SHA256SUMS"
cd "$SRC"
# The committer date taken from each patch makes HEAD the same in every build of this script.
git -c user.name=duncan0k -c user.email=dev@ark0.si am -q --committer-date-is-author-date "${patch_files[@]}"
tree=$(git rev-parse 'HEAD^{tree}')
echo "applied $(git rev-list --count "$core_head"..HEAD) patches, HEAD $(git rev-parse HEAD), tree $tree" | tee -a "$OUT/sources.txt"
[ "$tree" = "$SERIES_TREE" ] || { echo "the patch sets give tree $tree, not the reviewed $SERIES_TREE" >&2; exit 1; }

say "depends for $HOST_TRIPLET"
t0=$(date +%s)
make -C depends -j"$JOBS" HOST="$HOST_TRIPLET" NO_QT=1 NO_QR=1 NO_WALLET=1 NO_ZMQ=1 NO_USDT=1 NO_IPC=1 \
    > "$OUT/depends.log" 2>&1 || { tail -40 "$OUT/depends.log"; exit 1; }
say "depends done in $(since "$t0")"
ls depends/"$HOST_TRIPLET"/lib | tee "$OUT/depends-libs.txt"

say "configure"
t0=$(date +%s)
# --no-insert-timestamp as Core's own Guix build links for mingw: without it the linker writes the link time
# into the PE header and the export table, the only bytes in which two builds of this script differed.
cmake -B build --toolchain "depends/$HOST_TRIPLET/toolchain.cmake" \
    -DCMAKE_BUILD_TYPE=Release -DBUILD_TESTS=ON -DBUILD_BENCH=OFF -DBUILD_FUZZ_BINARY=OFF \
    -DENABLE_WALLET=OFF -DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF -DWITH_USDT=OFF \
    -DAPPEND_LDFLAGS=-Wl,--no-insert-timestamp \
    "-DVOLEITH_CARGO=$RUST_PREFIX/bin/cargo" "-DVOLEITH_RUSTC=$RUST_PREFIX/bin/rustc" "-DVOLEITH_RUST_TARGET=$RUST_TARGET" \
    > "$OUT/configure.log" 2>&1 || { tail -40 "$OUT/configure.log"; exit 1; }
grep -E "^  (Build|Cross compiling|  wallet|  [a-z ]+\.\.\.+)|CMAKE_BUILD_TYPE|Preprocessor defined|C\+\+ compiler flags|Linker flags" "$OUT/configure.log" | head -40 || true
say "build"
cmake --build build -j"$JOBS" --target bitcoind bitcoin-cli test_bitcoin > "$OUT/build.log" 2>&1 \
    || { tail -60 "$OUT/build.log"; exit 1; }
say "configure and build done in $(since "$t0")"

for b in bitcoind bitcoin-cli test_bitcoin; do
    install -m 0644 "build/bin/$b.exe" "$OUT/$b.exe"
done
cd "$OUT"
sha256sum bitcoind.exe bitcoin-cli.exe test_bitcoin.exe | tee SHA256SUMS-win
ls -l bitcoind.exe bitcoin-cli.exe test_bitcoin.exe | awk '{print $5, $NF}' | tee sizes.txt
file bitcoind.exe bitcoin-cli.exe | tee file.txt
for b in bitcoind bitcoin-cli; do
    echo "--- $b.exe imports"; x86_64-w64-mingw32-objdump -p "$b.exe" | grep "DLL Name" | sort -u || true
done | tee imports.txt
# What a Windows package would ship: the two programs without symbols and debug sections. strip rewrites the
# PE header with the current time unless SOURCE_DATE_EPOCH is set (binutils' BFD reads it), as Guix sets it.
mkdir -p stripped
for b in bitcoind bitcoin-cli; do SOURCE_DATE_EPOCH=1 x86_64-w64-mingw32-strip -o "stripped/$b.exe" "$b.exe"; done
( cd stripped && sha256sum bitcoind.exe bitcoin-cli.exe && ls -l bitcoind.exe bitcoin-cli.exe | awk '{print $5, $NF}' ) \
    | tee SHA256SUMS-win-stripped

if [ "$PHASE" = full ]; then
    # From here a failed check is recorded, not fatal: the node started below must still be stopped.
    set +e
    # Wine maps Windows file names through the Unix locale; under the image's default C locale it cannot
    # create the non-ASCII names two unit tests use (fs_tests, dbwrapper_tests/unicodepath). Windows itself
    # keeps names in UTF-16, so UTF-8 is the closer stand-in.
    export LANG=C.UTF-8 LC_ALL=C.UTF-8
    export WINEPREFIX=$W/wine WINEDEBUG=-all WINEDLLOVERRIDES="mscoree,mshtml="
    WINE=$(command -v wine64 || echo /usr/lib/wine/wine64)
    say "wine prefix ($WINE)"
    "$WINE" wineboot --init > "$OUT/wineboot.log" 2>&1 || true
    "$WINE" "$OUT/stripped/bitcoind.exe" -version | head -1 | tr -d '\r' | tee version.txt

    SUITES=mldsa44_tests,p2mr_mldsa44_tests,signet_pqblock_tests,p2mr_tripwire_tests,slhdsa128s_tests,p2mr_slhdsa128s_tests,p2mr_ts_m4_tests,p2mr_rescue_tests,script_tests,transaction_tests,script_standard_tests,descriptor_tests,pow_tests
    say "unit tests of the patch sets under Wine: $SUITES"
    t0=$(date +%s)
    rc=0; "$WINE" "$OUT/test_bitcoin.exe" --run_test="$SUITES" --log_level=test_suite > unit-patches.log 2>&1 || rc=$?
    tr -d '\r' < unit-patches.log | grep -E "^\*\*\*|error|failure|Leaving test suite|No errors" | tail -30
    echo "patch suites exit $rc in $(since "$t0")" | tee unit-patches.result

    say "whole test_bitcoin.exe under Wine"
    t0=$(date +%s)
    rc=0; "$WINE" "$OUT/test_bitcoin.exe" --log_level=test_suite > unit-all.log 2>&1 || rc=$?
    tr -d '\r' < unit-all.log | grep -E "^\*\*\*|error:|failure" | tail -40
    echo "whole binary exit $rc in $(since "$t0")" | tee unit-all.result

    say "end to end: the stripped bitcoind.exe joins Ark-0 the way JOIN.md says"
    D=$WINEPREFIX/drive_c/ark0
    mkdir -p "$D"
    printf 'signet=1\nlisten=0\n\n[signet]\nsignetchallenge=5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae\nsignetpowtargetspacing=90@8064\naddnode=node.ark0.si:38433\n' > "$D/bitcoin.conf"
    cli() { "$WINE" "$OUT/stripped/bitcoin-cli.exe" -datadir='C:\ark0' "$@" | tr -d '\r'; }
    t0=$(date +%s)
    "$WINE" "$OUT/stripped/bitcoind.exe" -datadir='C:\ark0' > e2e-console.log 2>&1 &
    node=$!
    # However the script ends from here, the node does not outlive it.
    trap 'kill "$node" 2>/dev/null || true' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    cli -rpcwait -rpcwaittimeout=120 getblockcount > /dev/null
    synced=0
    for i in $(seq 1 120); do
        if cli getblockchaininfo | grep -q '"initialblockdownload": false'; then synced=1; break; fi
        sleep 5
    done
    echo "synced=$synced after $(since "$t0")" | tee e2e.result
    {
        echo "--- getblockchaininfo"; cli getblockchaininfo | grep -E '"(chain|blocks|headers|initialblockdownload)"'
        echo "--- startup lines"; tr -d '\r' < "$D/signet/debug.log" | grep -E "Signet |P2MR|tripwire" | cut -d' ' -f2- | head -10
        echo "--- getblockhash 1264"; cli getblockhash 1264
        echo "--- getdeploymentinfo"; cli getdeploymentinfo | grep -A4 -E '"(p2mr|p2mr_mldsa44|p2mr_tripwire|p2mr_slhdsa128s|p2mr_rescue)"'
        echo "--- gettripwireinfo"; cli gettripwireinfo
        echo "--- getpeerinfo"; cli getpeerinfo | grep -E '"(addr|connection_type|subver)"' | sed -E 's/"addr": "[^"]*"/"addr": "<redacted>"/'
        echo "--- testmempoolaccept (not a transaction)"; cli testmempoolaccept '["00"]' 2>&1 | head -3
        echo "--- datadir"; du -sh "$D"
    } | tee e2e.txt
    cli stop
    for i in $(seq 1 60); do kill -0 "$node" 2>/dev/null || break; sleep 1; done
    if kill -0 "$node" 2>/dev/null; then
        echo "bitcoind.exe still running 60 s after stop; ending it" | tee -a e2e.result
        kill "$node"
    fi
    rc=0; wait "$node" || rc=$?
    echo "bitcoind.exe exited $rc after stop" | tee -a e2e.result
    tr -d '\r' < "$D/signet/debug.log" | grep -E "Shutdown done|error|Error" | tail -5 | tee -a e2e.result
fi

say "done"
touch "$OUT/DONE"

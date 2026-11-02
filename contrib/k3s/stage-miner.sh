#!/usr/bin/env bash
#
# Assemble the build context for the block producer image on the work volume.
#
# Two sources come together: the built tree already on the volume supplies the
# signet miner module, the functional test framework it imports, and the three
# binaries the producer calls (bitcoin-cli, bitcoin-util and, since M4-B,
# signet-slh-sign); and this repository supplies everything else --
# the Dockerfile, the entry point, and the two producer scripts in
# ark0/producer/.
#
# The producer scripts used to be copied from an operator's home directory,
# which meant the public checkout could not build this image and nobody outside
# that machine could review what went into it. They are in the repository now,
# and this script stages those copies and no others.
#
#   KUBECTL="sudo k3s kubectl" ./stage-miner.sh
#   ./stage-miner.sh --self-test
#
# Then build it with ./build-image.sh miner.
#
# CONTEXT_ROOT, BUILT_TREE and BUILT_BIN change where the context goes and where
# the built tree and the published binaries are read from (imgctx, /work/src and
# /work/out/bin unless set): the M3 development images are staged in m3-imgctx
# from an M3 run (m3-dev/README.md), apart from everything the release images use.
#
# --self-test exercises the key material scanner below against files it makes
# itself and exits. It reaches no cluster, copies nothing, and does not need the
# producer scripts to be present.
set -euo pipefail
# shellcheck source=common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

PRODUCER_DIR="$HERE/ark0/producer"
CONTEXT_ROOT="${CONTEXT_ROOT:-imgctx}"
BUILT_TREE="${BUILT_TREE:-/work/src}"
BUILT_BIN="${BUILT_BIN:-/work/out/bin}"
case "$CONTEXT_ROOT" in
    img|-*|*[!a-z0-9-]*) die "CONTEXT_ROOT must be one directory name of lower-case letters, digits and dashes, not img" ;;
esac
MINER_CTX="/work/$CONTEXT_ROOT/miner"

# The signer that goes into the image: check-signer.sh, the one entry point of
# that check, which Dockerfile.miner runs too. It is Python; the shell pod (plain
# Ubuntu) has none, so it runs here, on the staged copy streamed out of the pod,
# and by --self-test: this machine needs python3.
[ -r "$HERE/check-signer.sh" ] || die "$HERE/check-signer.sh is not readable"
command -v python3 > /dev/null || die "python3 is needed here to run check-signer.sh"

# check_streamed <command...>: check-signer.sh on the bytes a command writes, as
# staging checks the copy it stages: streamed, so that what is checked is what
# that command reads. Under pipefail a command that fails part way fails the
# check even when the bytes that did arrive pass.
check_streamed() { "$@" | sh "$HERE/check-signer.sh" /dev/stdin; }

# The two scripts read their keys from files at run time and are expected to
# carry none themselves. They are about to be baked into an image other people
# are asked to reproduce, so check rather than assume.
#
# Every rule below is quiet, and that is the point. A detector that prints the
# line it matched copies the candidate secret into the terminal, the job log
# and the shell history of whoever ran it, which is the one place it was not
# already. A hit reports the file and the rule identifier and stops there. Both
# files are in this repository, so anyone who needs to see the line opens it.
#
#   keyword            a word that turns up only around key material
#   pem                a PEM private key header
#   wif-uncompressed   51 base58 characters opening 5 (mainnet) or 9 (test/signet)
#   wif-compressed     52 base58 characters opening K or L (mainnet) or c (test/signet)
#   xprv               an extended private key, any of the usual prefixes
#   hex64              a bare 64 character hex run, the shape of a raw key
#   hex96              a bare 96 character hex run, the shape of M4-B's 48-byte
#                      SLH-DSA key (SK.seed || SK.prf || PK.seed) written as hex
#
# base58 is case sensitive, so the three base58 rules are matched case
# sensitively: a lowercase c and an uppercase C do not mean the same thing.
# The old rule was case insensitive and covered only 5, K and L, so every
# testnet and signet encoding walked past it.
#
# hex64 and hex96 need a heuristic, because a 32 byte private key and a 32 byte
# hash are written identically (and a 48 byte key and a SHA-384 digest). Two
# filters make them usable:
#
#   - the run has to be exactly 64 (or 96) characters, with a non hex character
#     on each side, so quoted or standalone. Serialized transactions, blocks and scripts are hex too and far
#     longer; a bare key is what gets written at exactly this length.
#   - a 64 character run on a line that also names a hash (hash, txid, root,
#     merkle, digest, sha, checksum) is taken to be that hash and allowed. A 96
#     character run never is: no hash this image carries is written that way,
#     a key variable can easily name sha (SLH_SHA2_128S_SEED), and a false
#     positive only stops the build for a look.
#
# Both filters can be fooled, in both directions, which is why a hit stops the
# build rather than warning. A false positive costs one line of reading; a
# false negative ships a private key inside a published image.
B58='[1-9A-HJ-NP-Za-km-z]'
HEX='[0-9a-fA-F]'
HEX64_RE="(^|[^0-9a-fA-F])$HEX{64}([^0-9a-fA-F]|\$)"
HEX96_RE="(^|[^0-9a-fA-F])$HEX{96}([^0-9a-fA-F]|\$)"
HASH_WORDS='hash|txid|root|merkle|digest|sha|checksum'
secret_hit=0
scan_error=0

report_secret() {
    printf 'error: %s matches rule "%s"\n' "$1" "$2" >&2
    secret_hit=1
}

# A rule that could not be run is not a rule that found nothing, and the
# difference is the whole value of this check.
#
# `if grep -q ...` collapsed grep's three outcomes into two. grep exits 0 for a
# match, 1 for no match, and 2 -- or 128 plus a signal -- when it could not do
# the job: an unreadable file, a bad pattern, a signal. The `if` read every one
# of those as "nothing found" and staging continued. `set -e` does not help,
# because a command used as a condition is exempt from errexit by definition,
# which is exactly why the shape was easy to trust.
#
# So the status is captured and the three cases are separate. An error is its
# own kind of finding and aborts staging with the same force as a hit, because
# a scan that did not complete says nothing about the file it was pointed at.
report_scan_error() {
    printf 'error: rule "%s" could not be run against %s (status %s)\n' "$2" "$1" "$3" >&2
    scan_error=1
}

# Runs one grep and reads only its status. Output goes nowhere: these rules
# match candidate secrets, so the matched text must not reach a terminal, a job
# log or a shell history. A finding is the file name and the rule name.
scan_rule() {
    local file="$1" rule="$2" rc=0
    shift 2
    "$@" > /dev/null 2>&1 || rc=$?
    case "$rc" in
        0) report_secret "$file" "$rule" ;;
        1) ;;
        *) report_scan_error "$file" "$rule" "$rc" ;;
    esac
}

# Case insensitive: prose, not an encoding.
scan_nocase() { scan_rule "$1" "$2" grep -Ei -e "$3" -- "$1"; }

# Case sensitive: base58 and the extended key prefixes carry meaning in case.
scan_case() { scan_rule "$1" "$2" grep -E -e "$3" -- "$1"; }

# The hex rules hold no matched content in a shell variable, where `bash -x`
# would print it: a rule without the hash-word allowance asks grep -l for the
# file name only; the one with it pipes the matching lines straight into a
# counting grep, and the shell sees only the count. Both read to the end of
# their input, and each grep's exit status is read on its own (PIPESTATUS):
# 0 or 1 are answers, anything else is an error. (An earlier version ended in
# `grep -qvEi`, which closed the pipe at its first match, so that SIGPIPE and
# `pipefail` reported a large file as clean.)
#
# scan_hexrun <file> <rule> <regex> <exempt>: with exempt=1 a line naming a
# hash is allowed. A finding is the file name and the rule.
scan_hexrun() {
    local file="$1" rule="$2" re="$3" exempt="$4" count rc
    if [ "$exempt" != "1" ]; then
        rc=0
        grep -lE -e "$re" -- "$file" > /dev/null 2>&1 || rc=$?
        case "$rc" in
            0) report_secret "$file" "$rule" ;;
            1) ;;
            *) report_scan_error "$file" "$rule" "$rc" ;;
        esac
        return 0
    fi
    local -a status
    count="$(grep -E -e "$re" -- "$file" 2> /dev/null | grep -cvEi -e "$HASH_WORDS" 2> /dev/null; echo "${PIPESTATUS[*]}")"
    status=(${count##*$'\n'})
    count="${count%%$'\n'*}"
    case "${status[0]}" in
        0|1) ;;
        *) report_scan_error "$file" "$rule" "${status[0]}"; return 0 ;;
    esac
    case "${status[1]}" in
        0|1) ;;
        *) report_scan_error "$file" "$rule" "${status[1]}"; return 0 ;;
    esac
    [ "$count" = "0" ] || report_secret "$file" "$rule"
}
scan_hex64() { scan_hexrun "$1" hex64 "$HEX64_RE" 1; }
scan_hex96() { scan_hexrun "$1" hex96 "$HEX96_RE" 0; }

# without_xtrace <command...>: run a command with `set -x` off, and restore it
# however the command returns. The scanner and its self-test handle candidate
# key material, which a trace would copy to stderr.
without_xtrace() {
    local had_x=0 rc=0
    case "$-" in *x*) had_x=1 ;; esac
    { set +x; } 2> /dev/null
    "$@" || rc=$?
    if [ "$had_x" = "1" ]; then set -x; fi
    return "$rc"
}

scan_file() { without_xtrace scan_file_rules "$1"; }
scan_file_rules() {
    local f="$1"
    scan_nocase "$f" keyword 'privkey|private_key|privatekey|rpcpassword|secret_?key|(^|[^a-z])wif([^a-z]|$)'
    scan_nocase "$f" pem '-----BEGIN [A-Z ]*PRIVATE KEY-----'
    scan_case "$f" wif-uncompressed "(^|[^1-9A-HJ-NP-Za-km-z])[59]$B58{50}([^1-9A-HJ-NP-Za-km-z]|\$)"
    scan_case "$f" wif-compressed "(^|[^1-9A-HJ-NP-Za-km-z])[KLc]$B58{51}([^1-9A-HJ-NP-Za-km-z]|\$)"
    scan_case "$f" xprv "[xyztuv]prv$B58{50,}"
    scan_hex64 "$f"
    scan_hex96 "$f"
}

# `./stage-miner.sh --self-test` checks that the scanner reports what it is
# supposed to report, against files made here rather than against the producer
# scripts. It reaches no cluster and copies nothing, so it needs neither
# KUBECTL nor root.
#
# Nothing below is a key. The base58 strings have the right prefix and length
# and a deliberately wrong checksum, and the hex runs are a single repeated
# digit. They exist to be matched, and the two properties being checked are
# that a match aborts staging and that a file the scanner cannot read aborts it
# too -- the second being the case the old code let through.
self_test_scanner() {
    local dir failed=0 hex64 hex96 wif
    dir="$(mktemp -d)" || { printf 'error: no scratch directory\n' >&2; return 1; }
    hex64="$(printf 'a%.0s' $(seq 64))"
    hex96="$(printf '0f%.0s' $(seq 48))"
    wif="c$(printf 'Q%.0s' $(seq 51))"

    # expect_scan <label> <expected secret_hit> <expected scan_error> <file>
    expect_scan() {
        secret_hit=0
        scan_error=0
        scan_file "$4" 2> /dev/null
        if [ "$secret_hit" = "$2" ] && [ "$scan_error" = "$3" ]; then
            printf '    ok    %-34s hit=%s error=%s\n' "$1" "$secret_hit" "$scan_error"
        else
            printf '    FAIL  %-34s hit=%s error=%s (expected hit=%s error=%s)\n' \
                   "$1" "$secret_hit" "$scan_error" "$2" "$3"
            failed=1
        fi
    }

    printf 'nothing to see here\nheight = 42\n' > "$dir/clean"
    expect_scan "a clean file" 0 0 "$dir/clean"

    printf 'wallet = "%s"\n' "$wif" > "$dir/wif"
    expect_scan "a WIF-shaped base58 string" 1 0 "$dir/wif"

    printf -- '-----BEGIN EC PRIVATE KEY-----\n' > "$dir/pem"
    expect_scan "a PEM private key header" 1 0 "$dir/pem"

    printf 'value = %s\n' "$hex64" > "$dir/hex"
    expect_scan "a bare 64 character hex run" 1 0 "$dir/hex"

    printf 'txid = %s\n' "$hex64" > "$dir/hex-allowed"
    expect_scan "the same run named as a txid" 0 0 "$dir/hex-allowed"

    # M4-B's 48-byte SLH-DSA key written as hex: quoted, and standalone.
    printf 'seed = "%s"\n' "$hex96" > "$dir/hex96-quoted"
    expect_scan "a quoted 96 character hex run" 1 0 "$dir/hex96-quoted"

    printf '%s\n' "$hex96" > "$dir/hex96-bare"
    expect_scan "a standalone 96 character hex run" 1 0 "$dir/hex96-bare"

    printf 'SLH_SHA2_128S_SEED = "%s"\n' "$hex96" > "$dir/hex96-sha-name"
    expect_scan "96 hex named SLH_SHA2_128S_SEED" 1 0 "$dir/hex96-sha-name"

    printf 'sha256 = "%s"\n' "$hex96" > "$dir/hex96-sha256"
    expect_scan "96 hex next to sha256" 1 0 "$dir/hex96-sha256"

    printf 'key = %s0\n' "$hex96" > "$dir/hex97"
    expect_scan "97 hex characters (no key shape)" 0 0 "$dir/hex97"

    # Many matching lines, to outgrow a pipe buffer several times over. The old
    # hex64 filter reported nothing for this file and exited zero.
    awk -v n=200000 -v s="value = $hex64" 'BEGIN { while (n-- > 0) print s }' \
        > "$dir/hex-big"
    expect_scan "13 MB of matching lines" 1 0 "$dir/hex-big"

    printf 'nothing to see here\n' > "$dir/unreadable"
    chmod 000 "$dir/unreadable"
    if [ -r "$dir/unreadable" ]; then
        # Running as root, or on a filesystem without permissions: name a path
        # that cannot be read for a reason no privilege overrides.
        expect_scan "a file that does not exist" 0 1 "$dir/missing/none"
    else
        expect_scan "a file that cannot be read" 0 1 "$dir/unreadable"
        expect_scan "a file that does not exist" 0 1 "$dir/missing/none"
    fi

    # expect_signer <label> <expected exit> <file> [<reason>]: the image's signer
    # check, its exit status and, for a refusal, the reason it gives.
    expect_signer() {
        local rc=0 why
        why="$(sh "$HERE/check-signer.sh" "$3" 2>&1 > /dev/null)" || rc=$?
        if [ "$rc" = "$2" ] && { [ -z "${4:-}" ] || case "$why" in *"$4"*) true ;; *) false ;; esac; }; then
            printf '    ok    %-34s exit=%s\n' "$1" "$rc"
        else
            printf '    FAIL  %-34s exit=%s (expected %s%s)\n' "$1" "$rc" "$2" "${4:+, \"$4\"}"
            failed=1
        fi
    }
    # The signer check (check-signer.sh). The release signer is static, non-PIE.
    # The cases start from a minimal static ELF made here (header and one
    # program header, never run), and from the release signer when one is given
    # (--self-test <signer>); dd and appended bytes make the variants.
    le_hex() {  # le_hex <value> <bytes>: little-endian hex
        printf '%0*x' $(($2 * 2)) "$1" | sed 's/../& /g' | awk '{ for (i = NF; i > 0; i--) printf "%s", $i }'
    }
    mkelf() {   # mkelf <file> <class> <data> <e_type> <p_type> <e_phnum> <e_phoff> [<e_version> <p_filesz> <p_memsz>]
        local hex
        hex="7f454c46$(le_hex "$2" 1)$(le_hex "$3" 1)0100$(le_hex 0 8)"
        hex="$hex$(le_hex "$4" 2)$(le_hex 62 2)$(le_hex "${8:-1}" 4)$(le_hex 4194304 8)$(le_hex "$7" 8)$(le_hex 0 8)"
        hex="$hex$(le_hex 0 4)$(le_hex 64 2)$(le_hex 56 2)$(le_hex "$6" 2)$(le_hex 64 2)$(le_hex 0 2)$(le_hex 0 2)"
        hex="$hex$(le_hex "$5" 4)$(le_hex 5 4)$(le_hex 0 8)$(le_hex 4194304 8)$(le_hex 4194304 8)"
        hex="$hex$(le_hex "${9:-120}" 8)$(le_hex "${10:-120}" 8)$(le_hex 4096 8)"
        printf "$(printf '%s' "$hex" | sed 's/../\\x&/g')" > "$1"
    }
    mkelf "$dir/elf-static" 2 1 2 1 1 64
    expect_signer "signer: a minimal static ELF" 0 "$dir/elf-static"
    if [ -n "${SELF_TEST_SIGNER:-}" ]; then
        cp "$SELF_TEST_SIGNER" "$dir/release"
        expect_signer "signer: the release signer" 0 "$dir/release"
        base="$dir/release"
    else
        printf '    note  %s\n' "no release signer given (--self-test <signer>): its cases use the minimal ELF"
        base="$dir/elf-static"
    fi
    for mark in SIGNET_SLH_SIGN_SANITIZER_BUILD SIGNET_SLH_SIGN_TEST_FAULT __asan_init __hwasan_init \
                __ubsan_handle_add_overflow __tsan_init __msan_init __lsan_do_leak_check __sanitizer_print_stack_trace \
                "AddressSanitizer: heap-buffer-overflow" HWAddressSanitizer ThreadSanitizer MemorySanitizer \
                LeakSanitizer "UndefinedBehaviorSanitizer: undefined-behavior" "ubsan: add-overflow by 0x"; do
        { cat "$base"; printf 'x\0%s\0y' "$mark"; } > "$dir/signer-$mark"
        expect_signer "signer: static, with $mark" 1 "$dir/signer-$mark" "a sanitizer or fault test build"
    done
    # The runtimes that move stack frames out of the scrub's reach without a sanitizer's name.
    for mark in __safestack_init "safestack CHECK failed: %s:%d %s"; do
        { cat "$base"; printf 'x\0%s\0y' "$mark"; } > "$dir/signer-$mark"
        expect_signer "signer: static, with $mark" 1 "$dir/signer-$mark" "a SafeStack build"
    done
    for mark in __morestack SPLIT_STACK_GUARD; do
        { cat "$base"; printf 'x\0%s\0y' "$mark"; } > "$dir/signer-$mark"
        expect_signer "signer: static, with $mark" 1 "$dir/signer-$mark" "a split-stack build"
    done
    # A mark written into the release signer's bytes (dd, not appended).
    cp "$base" "$dir/signer-dd-asan"
    # Past the headers: at 4096 into the release signer, at the end of the minimal ELF.
    size="$(wc -c < "$base")"; [ "$size" -gt 8192 ] && at=4096 || at="$size"
    printf '__asan_report_load8' | dd of="$dir/signer-dd-asan" bs=1 seek="$at" conv=notrunc 2> /dev/null
    expect_signer "signer: __asan_ written in (dd)" 1 "$dir/signer-dd-asan" "contains '__asan_'"
    true_bin="$(command -v env)"
    expect_signer "signer: a dynamic executable" 1 "$true_bin" "not a non-PIE executable"
    mkelf "$dir/elf-interp" 2 1 2 3 1 64
    expect_signer "signer: a PT_INTERP" 1 "$dir/elf-interp" "PT_INTERP"
    mkelf "$dir/elf-dynamic" 2 1 2 2 1 64
    expect_signer "signer: a PT_DYNAMIC" 1 "$dir/elf-dynamic" "PT_DYNAMIC"
    head -c 100 "$dir/elf-static" > "$dir/elf-cut-phdr"
    expect_signer "signer: truncated in its phdrs" 1 "$dir/elf-cut-phdr" "program headers beyond the file"
    head -c 40 "$dir/elf-static" > "$dir/elf-cut-header"
    expect_signer "signer: truncated in its header" 1 "$dir/elf-cut-header" "truncated"
    mkelf "$dir/elf-phnum" 2 1 2 1 9 64
    expect_signer "signer: e_phnum beyond the file" 1 "$dir/elf-phnum" "program headers beyond the file"
    mkelf "$dir/elf-phoff" 2 1 2 1 1 4000000
    expect_signer "signer: e_phoff beyond the file" 1 "$dir/elf-phoff" "program headers beyond the file"
    mkelf "$dir/elf-32" 1 1 2 1 1 64
    expect_signer "signer: a 32-bit header" 1 "$dir/elf-32" "not a 64-bit ELF file"
    mkelf "$dir/elf-be" 2 2 2 1 1 64
    expect_signer "signer: a big-endian header" 1 "$dir/elf-be" "not a little-endian ELF file"
    mkelf "$dir/elf-pie" 2 1 3 1 1 64
    expect_signer "signer: ET_DYN (PIE, static-pie)" 1 "$dir/elf-pie" "not a non-PIE executable"
    mkelf "$dir/elf-version" 2 1 2 1 1 64 2
    expect_signer "signer: e_version 2" 1 "$dir/elf-version" "not ELF version 1 (e_version"
    mkelf "$dir/elf-load-beyond" 2 1 2 1 1 64 1 5000 5000
    expect_signer "signer: a PT_LOAD beyond the file" 1 "$dir/elf-load-beyond" "a PT_LOAD segment beyond the file"
    mkelf "$dir/elf-load-memsz" 2 1 2 1 1 64 1 120 100
    expect_signer "signer: PT_LOAD p_filesz > p_memsz" 1 "$dir/elf-load-memsz" "larger in the file than in memory"
    mkelf "$dir/elf-no-load" 2 1 2 4 1 64
    expect_signer "signer: no PT_LOAD" 1 "$dir/elf-no-load" "no PT_LOAD segment"
    printf 'not an executable\n' > "$dir/signer-text"
    expect_signer "signer: a non-ELF file" 1 "$dir/signer-text" "not an ELF file"
    expect_signer "signer: a file that does not exist" 1 "$dir/missing/signer" "cannot read it"

    # How the check is called. expect_status <label> <expected exit|nonzero> <command...>
    expect_status() {
        local label="$1" want="$2" rc=0
        shift 2
        "$@" > /dev/null 2>&1 || rc=$?
        if [ "$rc" = "$want" ] || { [ "$want" = nonzero ] && [ "$rc" != 0 ]; }; then
            printf '    ok    %-34s exit=%s\n' "$label" "$rc"
        else
            printf '    FAIL  %-34s exit=%s (expected %s)\n' "$label" "$rc" "$want"
            failed=1
        fi
    }
    # Streamed on standard input, as staging does it.
    expect_status "stream: the base passes" 0 check_streamed cat "$base"
    expect_status "stream: with a mark, refused" 1 check_streamed cat "$dir/signer-__asan_init"
    # A failing upstream command: nothing arrives, or the whole file and then a
    # failure (as a kubectl exec whose connection drops after the copy); both fail.
    expect_status "stream: upstream fails, no bytes" 1 check_streamed false
    expect_status "stream: upstream fails after bytes" nonzero check_streamed sh -c 'cat "$1"; exit 3' sh "$base"
    # Python's environment does not take part (python3 -I): a struct module and a
    # sitecustomize.py that would make every file pass, on PYTHONPATH and in the
    # current directory, and a PYTHONHOME that would stop Python from starting.
    mkdir -p "$dir/pyevil"
    printf 'import sys\nsys.exit(0)\n' > "$dir/pyevil/struct.py"
    printf 'import sys\nsys.exit(0)\n' > "$dir/pyevil/sitecustomize.py"
    expect_status "python: PYTHONPATH, sitecustomize" 1 env PYTHONPATH="$dir/pyevil" sh -c 'cd "$1" && sh "$2" "$3"' sh "$dir/pyevil" "$HERE/check-signer.sh" "$dir/signer-__asan_init"
    expect_status "python: PYTHONHOME" 1 env PYTHONHOME="$dir/pyevil" sh "$HERE/check-signer.sh" "$dir/signer-__asan_init"
    expect_status "python: the same, the base passes" 0 env PYTHONPATH="$dir/pyevil" PYTHONHOME="$dir/pyevil" sh -c 'cd "$1" && sh "$2" "$3"' sh "$dir/pyevil" "$HERE/check-signer.sh" "$base"
    # Staging checks the file it stages: the stream reads the copy in the image
    # context, not its source, so a staged copy that differs from a clean source
    # is refused (and Dockerfile.miner checks the image's own copy again).
    mkdir -p "$dir/src-bin" "$dir/ctx-bin"
    cp "$base" "$dir/src-bin/signet-slh-sign"
    cp "$dir/signer-__asan_init" "$dir/ctx-bin/signet-slh-sign"
    expect_status "staged copy differs from its source" 1 check_streamed cat "$dir/ctx-bin/signet-slh-sign"

    chmod 700 "$dir/unreadable" 2> /dev/null || true
    rm -rf "$dir"
    secret_hit=0
    scan_error=0
    return "$failed"
}

if [ "${1:-}" = "--self-test" ]; then
    SELF_TEST_SIGNER="${2:-}"
    log "key material scanner and signer check"
    without_xtrace self_test_scanner || die "the key material scanner does not behave as documented"
    log "self-test passed"
    exit 0
fi

for f in ark0.py miner_loop.sh; do
    [ -r "$PRODUCER_DIR/$f" ] || die "$PRODUCER_DIR/$f is not readable"
done

log "checking the producer scripts for key material"

for f in "$PRODUCER_DIR/ark0.py" "$PRODUCER_DIR/miner_loop.sh"; do
    scan_file "$f"
done

# An incomplete scan is reported before a hit, because it is the finding that
# makes the other one meaningless: if a rule did not run, "no key material was
# found" is not something this script is entitled to say.
[ "$scan_error" = "0" ] \
    || die "a rule could not be run, so this scan proves nothing;" \
           "not copying the producer scripts into an image"
[ "$secret_hit" = "0" ] \
    || die "the producer scripts look like they carry key material; not copying them into an image"

ensure_workspace

log "collecting the pieces already on the volume"
in_shell sh -c '
set -eu
src="$1" bin="$2" ctx="$3"
test -f "$src/contrib/signet/miner" || { echo "no built tree at $src" >&2; exit 1; }
test -x "$bin/bitcoin-cli" || { echo "no binaries at $bin" >&2; exit 1; }
# The SLH-DSA block signer of M4-B, published by build.sh from a build with the
# M4-B series (patches-m4b/, applied after patches-m4t/); an older set of
# binaries cannot make this image.
test -x "$bin/signet-slh-sign" || { echo "no signet-slh-sign at $bin: build with the M4-T and M4-B series (PATCH_SETS ending in m4r m4t m4b)" >&2; exit 1; }
# /work/out/bin is written only by a build that passed every check it was asked
# to run, and the provenance beside the binaries is what says so. Carry it into
# the image context so build-image.sh can require it here too.
test -f "$bin/PROVENANCE.txt" || {
    echo "no PROVENANCE.txt beside $bin: those binaries were not published by a passing build" >&2
    exit 1
}
rm -rf "$ctx"
mkdir -p "$ctx/bin" \
         "$ctx/bitcoin/contrib/signet" \
         "$ctx/bitcoin/test/functional" \
         "$ctx/demo"
cp "$src/contrib/signet/miner" "$ctx/bitcoin/contrib/signet/miner"
cp -r "$src/test/functional/test_framework" "$ctx/bitcoin/test/functional/"
find "$ctx/bitcoin" -name __pycache__ -type d -prune -exec rm -rf {} +
cp "$bin/bitcoin-cli" "$bin/bitcoin-util" "$bin/signet-slh-sign" "$ctx/bin/"
cp "$bin/PROVENANCE.txt" "$ctx/PROVENANCE.txt"
' sh "$BUILT_TREE" "$BUILT_BIN" "$MINER_CTX"

# The copy in the image context, streamed out of the pod into check-signer.sh
# (Python, which the shell pod lacks). Dockerfile.miner checks the same file again
# inside the image.
log "checking the staged signer"
check_streamed in_shell cat "$MINER_CTX/bin/signet-slh-sign" \
    || die "the staged signet-slh-sign is not a release signer; not staging it"

log "copying the producer scripts from ark0/producer"
push_file "$PRODUCER_DIR/ark0.py" "$MINER_CTX/demo"
push_file "$PRODUCER_DIR/miner_loop.sh" "$MINER_CTX/demo"

log "copying the Dockerfile and the entry point"
push_file "$HERE/Dockerfile.miner" "$MINER_CTX"
push_file "$HERE/miner-entrypoint.sh" "$MINER_CTX"
push_file "$HERE/check-signer.sh" "$MINER_CTX"

log "staged"
in_shell sh -c 'find "$1" -maxdepth 3 -not -path "*/test_framework/*" | sort; du -sh "$1"' sh "$MINER_CTX"

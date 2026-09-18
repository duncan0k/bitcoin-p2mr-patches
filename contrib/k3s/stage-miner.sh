#!/usr/bin/env bash
#
# Assemble the build context for the block producer image on the work volume.
#
# Two sources come together: the built tree already on the volume supplies the
# signet miner module, the functional test framework it imports, and the two
# binaries the producer calls; and this repository supplies everything else --
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
# --self-test exercises the key material scanner below against files it makes
# itself and exits. It reaches no cluster, copies nothing, and does not need the
# producer scripts to be present.
set -euo pipefail
# shellcheck source=common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

PRODUCER_DIR="$HERE/ark0/producer"

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
#
# base58 is case sensitive, so the three base58 rules are matched case
# sensitively: a lowercase c and an uppercase C do not mean the same thing.
# The old rule was case insensitive and covered only 5, K and L, so every
# testnet and signet encoding walked past it.
#
# hex64 needs a heuristic, because a 32 byte private key and a 32 byte hash are
# written identically. Two filters make it usable:
#
#   - the run has to be exactly 64 characters, with a non hex character on each
#     side. Serialized transactions, blocks and scripts are hex too and far
#     longer; a bare key is what gets written at exactly this length.
#   - a run on a line that also names a hash (hash, txid, root, merkle, digest,
#     sha, checksum) is taken to be that hash and allowed.
#
# Both filters can be fooled, in both directions, which is why a hit stops the
# build rather than warning. A false positive costs one line of reading; a
# false negative ships a private key inside a published image.
B58='[1-9A-HJ-NP-Za-km-z]'
HEX='[0-9a-fA-F]'
HEX64_RE="(^|[^0-9a-fA-F])$HEX{64}([^0-9a-fA-F]|\$)"
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

# Two filters, and both read to the end of their input.
#
# This used to end in `grep -qvEi`, which stops at its first match and closes
# the pipe. The listing grep then wrote into a closed pipe, took SIGPIPE, and
# `pipefail` reported 141 for the pipeline -- which the `if` read as "nothing
# found". A file small enough to fit in one pipe buffer was scanned correctly
# and a larger one had its detections suppressed by its own size, which is the
# worst possible direction for this to fail in.
#
# The listing is captured whole and the second filter counts rather than stops,
# so neither end can be cut short. `grep -c` exits 1 on a count of zero, so
# both 0 and 1 are ordinary answers here and only the rest is an error.
scan_hex64() {
    local file="$1" candidates count rc=0
    candidates="$(grep -nE -e "$HEX64_RE" -- "$file" 2> /dev/null)" || rc=$?
    case "$rc" in
        0) ;;
        1) return 0 ;;
        *) report_scan_error "$file" hex64 "$rc"; return 0 ;;
    esac
    rc=0
    count="$(printf '%s\n' "$candidates" \
        | grep -cvEi -e "$HASH_WORDS" 2> /dev/null)" || rc=$?
    case "$rc" in
        0|1) ;;
        *) report_scan_error "$file" hex64 "$rc"; return 0 ;;
    esac
    [ "$count" = "0" ] || report_secret "$file" hex64
}

scan_file() {
    local f="$1"
    scan_nocase "$f" keyword 'privkey|private_key|privatekey|rpcpassword|secret_?key|(^|[^a-z])wif([^a-z]|$)'
    scan_nocase "$f" pem '-----BEGIN [A-Z ]*PRIVATE KEY-----'
    scan_case "$f" wif-uncompressed "(^|[^1-9A-HJ-NP-Za-km-z])[59]$B58{50}([^1-9A-HJ-NP-Za-km-z]|\$)"
    scan_case "$f" wif-compressed "(^|[^1-9A-HJ-NP-Za-km-z])[KLc]$B58{51}([^1-9A-HJ-NP-Za-km-z]|\$)"
    scan_case "$f" xprv "[xyztuv]prv$B58{50,}"
    scan_hex64 "$f"
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
    local dir failed=0 hex64 wif
    dir="$(mktemp -d)" || { printf 'error: no scratch directory\n' >&2; return 1; }
    hex64="$(printf 'a%.0s' $(seq 64))"
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

    chmod 700 "$dir/unreadable" 2> /dev/null || true
    rm -rf "$dir"
    secret_hit=0
    scan_error=0
    return "$failed"
}

if [ "${1:-}" = "--self-test" ]; then
    log "key material scanner"
    self_test_scanner || die "the key material scanner does not behave as documented"
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
test -f /work/src/contrib/signet/miner || { echo "no built tree at /work/src" >&2; exit 1; }
test -x /work/out/bin/bitcoin-cli || { echo "no binaries at /work/out/bin" >&2; exit 1; }
# /work/out/bin is written only by a build that passed every check it was asked
# to run, and the provenance beside the binaries is what says so. Carry it into
# the image context so build-image.sh can require it here too.
test -f /work/out/bin/PROVENANCE.txt || {
    echo "no PROVENANCE.txt beside /work/out/bin: those binaries were not published by a passing build" >&2
    exit 1
}
rm -rf /work/imgctx/miner
mkdir -p /work/imgctx/miner/bin \
         /work/imgctx/miner/bitcoin/contrib/signet \
         /work/imgctx/miner/bitcoin/test/functional \
         /work/imgctx/miner/demo
cp /work/src/contrib/signet/miner /work/imgctx/miner/bitcoin/contrib/signet/miner
cp -r /work/src/test/functional/test_framework /work/imgctx/miner/bitcoin/test/functional/
find /work/imgctx/miner/bitcoin -name __pycache__ -type d -prune -exec rm -rf {} +
cp /work/out/bin/bitcoin-cli /work/out/bin/bitcoin-util /work/imgctx/miner/bin/
cp /work/out/bin/PROVENANCE.txt /work/imgctx/miner/PROVENANCE.txt
'

log "copying the producer scripts from ark0/producer"
push_file "$PRODUCER_DIR/ark0.py" /work/imgctx/miner/demo
push_file "$PRODUCER_DIR/miner_loop.sh" /work/imgctx/miner/demo

log "copying the Dockerfile and the entry point"
push_file "$HERE/Dockerfile.miner" /work/imgctx/miner
push_file "$HERE/miner-entrypoint.sh" /work/imgctx/miner

log "staged"
in_shell sh -c 'find /work/imgctx/miner -maxdepth 3 -not -path "*/test_framework/*" | sort; du -sh /work/imgctx/miner'

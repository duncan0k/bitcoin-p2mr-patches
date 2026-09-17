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
#
# Then build it with ./build-image.sh miner.
set -euo pipefail
# shellcheck source=common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

PRODUCER_DIR="$HERE/ark0/producer"

for f in ark0.py miner_loop.sh; do
    [ -r "$PRODUCER_DIR/$f" ] || die "$PRODUCER_DIR/$f is not readable"
done

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
log "checking the producer scripts for key material"

B58='[1-9A-HJ-NP-Za-km-z]'
HEX='[0-9a-fA-F]'
secret_hit=0

report_secret() {
    printf 'error: %s matches rule "%s"\n' "$1" "$2" >&2
    secret_hit=1
}

# Case insensitive: prose, not an encoding.
scan_nocase() {
    if grep -qEi -- "$3" "$1"; then report_secret "$1" "$2"; fi
}

# Case sensitive: base58 and the extended key prefixes carry meaning in case.
scan_case() {
    if grep -qE -- "$3" "$1"; then report_secret "$1" "$2"; fi
}

# The matching lines go into a pipe and never to a terminal, so this stays as
# quiet as the others.
scan_hex64() {
    if grep -nE "(^|[^0-9a-fA-F])$HEX{64}([^0-9a-fA-F]|\$)" "$1" \
            | grep -qvEi 'hash|txid|root|merkle|digest|sha|checksum'; then
        report_secret "$1" hex64
    fi
}

for f in "$PRODUCER_DIR/ark0.py" "$PRODUCER_DIR/miner_loop.sh"; do
    scan_nocase "$f" keyword 'privkey|private_key|privatekey|rpcpassword|secret_?key|(^|[^a-z])wif([^a-z]|$)'
    scan_nocase "$f" pem '-----BEGIN [A-Z ]*PRIVATE KEY-----'
    scan_case "$f" wif-uncompressed "(^|[^1-9A-HJ-NP-Za-km-z])[59]$B58{50}([^1-9A-HJ-NP-Za-km-z]|\$)"
    scan_case "$f" wif-compressed "(^|[^1-9A-HJ-NP-Za-km-z])[KLc]$B58{51}([^1-9A-HJ-NP-Za-km-z]|\$)"
    scan_case "$f" xprv "[xyztuv]prv$B58{50,}"
    scan_hex64 "$f"
done

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

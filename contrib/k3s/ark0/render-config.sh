#!/usr/bin/env bash
#
# Render 10-configmap.yaml into an applyable ConfigMap, and show what applying
# it would change.
#
# 10-configmap.yaml is a template. It carries REPLACE_WITH_SIGNET_CHALLENGE and
# REPLACE_WITH_REWARD_ADDRESS because both belong to one particular network and
# not to this repository. It used to be part of kustomization.yaml, which meant
# the advertised `kubectl apply -k` could replace both running nodes'
# configuration with those literal strings. A server-side dry run does not
# catch that: the API server checks that a ConfigMap holds strings, it does not
# read them as a bitcoin.conf. So the template is out of the kustomizations and
# this is the only supported way to apply it.
#
# It renders, refuses anything unfilled or implausible, and then shows
# `kubectl diff` against the live object. Nothing is applied unless --apply is
# given, and --apply still runs the diff first.
#
# No refusal ever quotes the value it refused, or any part of one. The value
# most likely to be refused is the one pasted into the wrong variable, and
# printing a private key in the error that caught it is how a check becomes the
# leak. A prefix counts: an early check sees an arbitrary substring of an
# arbitrary string, so "this is not network tb, it is <prefix>" can hand back
# most of a hex key whose last character happened to be a '1'.
#
# What errors may still contain: file paths the operator passed in, names this
# script chose itself, counts and lengths, and integers decoded from a value
# that has already passed its checksum. None of those is the value.
#
#   ./render-config.sh --from-live   -o ~/ark0-conf.yaml
#   ./render-config.sh --from-host ~/.ark0 -o ~/ark0-conf.yaml
#   ARK0_SIGNET_CHALLENGE=51...ae ARK0_REWARD_ADDRESS=tb1... \
#       ./render-config.sh -o ~/ark0-conf.yaml
#   ./render-config.sh --from-live -o ~/ark0-conf.yaml --apply
#
# Sources, in order of precedence:
#
#   the environment     ARK0_SIGNET_CHALLENGE, ARK0_REWARD_ADDRESS,
#                       ARK0_RPCALLOWIP
#   --from-live         the ConfigMap ark0-conf already on the cluster. This is
#                       the best source when the network is running: it is
#                       literally what the nodes are reading, so a zero diff
#                       proves the render reproduced it exactly.
#   --from-host DIR     a host layout: DIR/nodeA/bitcoin.conf for the challenge
#                       and DIR/reward_address.txt for the address.
#
# rpcallowip has a fourth source, the cluster's own pod CIDR, used when nothing
# else supplies one. The committed template says 10.42.0.0/16; this cluster
# runs /24, and applying the wider value would have loosened it.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="$HERE/10-configmap.yaml"
KUBECTL="${KUBECTL:-kubectl}"
NS="${ARK0_NS:-ark0}"
NAME="ark0-conf"

die() { printf 'render-config: %s\n' "$*" >&2; exit 1; }
log() { printf '==> %s\n' "$*"; }

OUT=""
FROM_LIVE=0
FROM_HOST=""
APPLY=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o|--out)     OUT="${2:-}"; shift 2 ;;
        --from-live)  FROM_LIVE=1; shift ;;
        --from-host)  FROM_HOST="${2:-}"; shift 2 ;;
        --apply)      APPLY=1; shift ;;
        -h|--help)    sed -n '2,40p' "$0"; exit 0 ;;
        # Not echoed, for the same reason the value checks do not echo: an
        # argument this script does not recognise is an argument it has not
        # looked at, and it has no idea what is in it.
        *)            die "unrecognised argument; see --help" ;;
    esac
done
[ -n "$OUT" ] || die "-o PATH is required; the rendered file must not go into the repository"
[ -r "$TEMPLATE" ] || die "$TEMPLATE is not readable"

CHALLENGE="${ARK0_SIGNET_CHALLENGE:-}"
REWARD="${ARK0_REWARD_ADDRESS:-}"
ALLOWIP="${ARK0_RPCALLOWIP:-}"

# Sourcing keeps the scalar as it was found, minus at most one trailing
# newline.
#
# It used to normalise first: `sed -n 's/^x=//p' | head -1` quietly discarded
# everything after the first match, and `tr -d '[:space:]'` deleted every space
# and line break anywhere in the value. Either would hand the validation below
# a value that is not the one in the file, and the check that refuses embedded
# line breaks would then never see the line breaks it exists to refuse: a
# configuration file with two `signetchallenge=` lines, or an address file with
# something after the address, would be silently trimmed into looking fine.
#
# So: read the whole thing, strip exactly one trailing newline because every
# text file ends with one and that is not content, and let refuse_multiline
# decide about anything else. That is the only whitespace this script removes
# before validating.
#
# Which cannot be done through `$(...)`, and the first version of this was
# wrong for that reason. Command substitution removes *every* trailing newline,
# so `$(conf_values ...)` handed back "abc" for a file holding both
# `signetchallenge=abc` and an empty `signetchallenge=`: the duplicate was gone
# before the refusal that exists to catch it ever ran, and the one trailing
# newline this script means to remove had already been removed several times
# over. capture() appends a sentinel byte inside the substitution and takes it
# off afterwards, so what comes back is what the producer wrote, to the byte;
# then one assignment removes at most one LF, and refuse_multiline decides
# about whatever is left. A CRLF file keeps its CR that way, and is refused.
#
# capture() also carries the producer's exit status out, which `$(...)` inside
# a larger expression loses: `x="$(a "$(b)")"` reports only a's. Every source
# below is a command that can fail -- a cluster that is not reachable, a file
# that is not readable -- and a failure that arrives as an empty value looks
# exactly like a value that is legitimately absent.
CAPTURED=""
capture() {
    local rc=0
    CAPTURED="$("$@"; rc=$?; printf x; exit "$rc")" || rc=$?
    CAPTURED="${CAPTURED%x}"
    return "$rc"
}

# Every value of a key in a configuration blob, newline separated, with nothing
# dropped. If a key appears twice, both appear here -- including a second
# assignment with an empty value, which prints as an empty line and survives
# capture() -- and the multiline refusal catches it rather than `head -1`
# hiding it.
conf_values() {
    awk -v k="$2" 'index($0, k "=") == 1 { print substr($0, length(k) + 2) }' <<< "$1"
}

if [ "$FROM_LIVE" = "1" ]; then
    log "reading the values the running nodes use from configmap/$NAME"
    capture $KUBECTL -n "$NS" get configmap "$NAME" -o jsonpath='{.data.nodea\.conf}' \
        || die "could not read configmap/$NAME from this cluster; use --from-host or the environment"
    live="$CAPTURED"
    [ -n "$live" ] || die "configmap/$NAME has no nodea.conf on this cluster; use --from-host or the environment"
    if [ -z "$CHALLENGE" ]; then
        capture conf_values "$live" signetchallenge \
            || die "could not read signetchallenge out of configmap/$NAME"
        CHALLENGE="${CAPTURED%$'\n'}"
    fi
    if [ -z "$ALLOWIP" ]; then
        capture conf_values "$live" rpcallowip \
            || die "could not read rpcallowip out of configmap/$NAME"
        ALLOWIP="${CAPTURED%$'\n'}"
    fi
    if [ -z "$REWARD" ]; then
        capture $KUBECTL -n "$NS" get configmap "$NAME" -o jsonpath='{.data.reward_address\.txt}' \
            || die "could not read reward_address.txt out of configmap/$NAME"
        REWARD="${CAPTURED%$'\n'}"
    fi
fi

if [ -n "$FROM_HOST" ]; then
    log "reading the values from $FROM_HOST"
    if [ -z "$CHALLENGE" ] && [ -r "$FROM_HOST/nodeA/bitcoin.conf" ]; then
        capture cat "$FROM_HOST/nodeA/bitcoin.conf" \
            || die "could not read $FROM_HOST/nodeA/bitcoin.conf"
        conf="$CAPTURED"
        capture conf_values "$conf" signetchallenge \
            || die "could not read signetchallenge out of $FROM_HOST/nodeA/bitcoin.conf"
        CHALLENGE="${CAPTURED%$'\n'}"
    fi
    if [ -z "$REWARD" ] && [ -r "$FROM_HOST/reward_address.txt" ]; then
        capture cat "$FROM_HOST/reward_address.txt" \
            || die "could not read $FROM_HOST/reward_address.txt"
        REWARD="${CAPTURED%$'\n'}"
    fi
fi

# The pod CIDR is a fallback rather than a source, so a cluster that cannot be
# asked is not fatal here: the value may still come from ARK0_RPCALLOWIP, and
# the rejection below says so if it does not. A failed lookup is logged rather
# than passed on as an empty answer.
if [ -z "$ALLOWIP" ]; then
    if capture $KUBECTL get node -o jsonpath='{.items[0].spec.podCIDR}'; then
        ALLOWIP="${CAPTURED%$'\n'}"
        if [ -n "$ALLOWIP" ]; then
            log "rpcallowip not supplied, using this cluster's pod CIDR"
        fi
    else
        log "rpcallowip not supplied and this cluster's pod CIDR could not be read"
    fi
fi

# --- validation -------------------------------------------------------------
# Every check below exists because the corresponding wrong value is one a
# person actually produces: an unset variable, a copied placeholder, a shell
# that swallowed the tail of a long hex string, the template's own /16 on a
# cluster that runs /24, or a key file read where an address file was meant.
#
# No rejection message ever contains the value it rejected. The value most
# likely to be refused is the one pasted into the wrong variable, and a refused
# private key printed as "that is not an address: <key>" has been copied into a
# terminal, a job log and a shell history by the very check meant to catch it.
# Errors name the field and the reason, and nothing else. Only values that have
# passed every check are printed, in the summary below.
reject() { die "$1: $2"; }

[ -n "$CHALLENGE" ] || reject "signet challenge" "not set; use --from-live, --from-host, or ARK0_SIGNET_CHALLENGE"
[ -n "$REWARD" ]    || reject "reward address"   "not set; use --from-live, --from-host, or ARK0_REWARD_ADDRESS"
[ -n "$ALLOWIP" ]   || reject "rpcallowip"       "not set and no pod CIDR to fall back on; set ARK0_RPCALLOWIP"

# Line breaks first, before anything looks at the content.
#
# Every format check below used to be `printf | grep -qE`, and grep matches a
# line at a time. A value made of a valid first line, a newline, and anything
# at all therefore passed: the shape check saw only the good line. What came
# after it stayed in the variable, so `${ALLOWIP#*/}` held "24", a newline and
# the rest, and handing that to `[ ... -le 32 ]` made Bash print the operand in
# its own error message before the constant one below could run. A check that
# rejects a value is exactly the wrong place to print it.
#
# A configuration value here is a single line by definition, so that is
# enforced once, at the top, and every check afterwards can assume it.
refuse_multiline() {
    case "$2" in
        *$'\n'*|*$'\r'*)
            reject "$1" "contains a line break; each of these values is a single line" ;;
    esac
}
refuse_multiline "signet challenge" "$CHALLENGE"
refuse_multiline "reward address"   "$REWARD"
refuse_multiline "rpcallowip"       "$ALLOWIP"

# Key material next, for every field, before any check that might describe what
# it saw. A WIF handed to --from-host as a reward address used to fail the
# address check, which printed it; the quiet private-key check below it was
# never reached.
#
# `[[ =~ ]]` rather than grep, here and everywhere below: it matches the whole
# value, so ^ and $ mean the ends of the value and not the ends of some line
# inside it.
# Both alternatives are anchored at both ends. The extended-key one used to end
# unanchored, so the comment above claimed a whole-value match the pattern did
# not make: `tprv...` followed by anything at all matched. That is the
# permissive direction for a detector, but a claim a regex does not keep is
# worth less than no claim.
key_material_re='^([59KLc][1-9A-HJ-NP-Za-km-z]{50,51}|[xyztuv]prv[1-9A-HJ-NP-Za-km-z]{20,})$'
refuse_key_material() {
    if [[ $2 =~ $key_material_re ]]; then
        reject "$1" "looks like a private key, not a configuration value; refusing, and not echoing it"
    fi
}
refuse_key_material "signet challenge" "$CHALLENGE"
refuse_key_material "reward address"   "$REWARD"
refuse_key_material "rpcallowip"       "$ALLOWIP"

# A bare 64 hex characters is the shape of a raw private key. That is a
# legitimate shape for the challenge, which is a hex script, and it is never a
# legitimate address or address range, so the two non-hex fields refuse it by
# name instead of falling through to a format error that has to describe what
# it saw.
raw_hex_key_re='^[0-9a-fA-F]{64}$'
refuse_raw_hex_key() {
    if [[ $2 =~ $raw_hex_key_re ]]; then
        reject "$1" "is 64 hex characters, the shape of a raw private key; refusing, and not echoing it"
    fi
}
refuse_raw_hex_key "reward address" "$REWARD"
refuse_raw_hex_key "rpcallowip"     "$ALLOWIP"

placeholder_check() {
    case "$2" in
        *REPLACE_WITH*) reject "$1" "still the template placeholder" ;;
    esac
}
placeholder_check "signet challenge" "$CHALLENGE"
placeholder_check "reward address"   "$REWARD"
placeholder_check "rpcallowip"       "$ALLOWIP"

# The challenge is checked structurally and not semantically: it is a
# serialised script, so it must be hex and an even number of digits, and it
# must be long enough to be a script at all. Nothing here decides whether it is
# the *right* challenge, because only the network knows that. --from-live reads
# it off the running nodes, and the diff at the end is what proves it matches
# the network in front of you.
[[ $CHALLENGE =~ ^[0-9a-fA-F]+$ ]] \
    || reject "signet challenge" "is not hexadecimal"
[ $(( ${#CHALLENGE} % 2 )) -eq 0 ] \
    || reject "signet challenge" "has an odd number of hex digits (${#CHALLENGE}), so it is truncated"
[ "${#CHALLENGE}" -ge 20 ] \
    || reject "signet challenge" "is ${#CHALLENGE} hex digits, too short to be a challenge script"

# The reward address is decoded rather than pattern-matched. A regex accepts
# tb1a, which is not an address at all, and accepts bc1... mainnet addresses,
# which are not addresses on this network. Both would be applied and then paid
# to by every coinbase. The decoder checks the bech32/bech32m checksum, the
# human-readable part, and that the witness version and program length are a
# pair BIP-173 and BIP-350 allow.
# Run it, do not just look for it on PATH: a name on PATH can be a shim that
# fails when invoked, and this check has to fail closed rather than be skipped.
python3 -c 'pass' > /dev/null 2>&1 \
    || reject "reward address" "python3 is needed to validate it and does not run here"
# The address is passed in the environment rather than as an argument, so it
# does not appear in the process list. The script writes its reason to stderr,
# quoting only what the address *is not*, never the address itself.
ARK0_ADDR="$REWARD" python3 - <<'PY' || reject "reward address" "rejected for the reason above"
import os, sys

CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
HRP = "tb"          # signet and testnet share this one


def polymod(values):
    gen = (0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3)
    chk = 1
    for v in values:
        top = chk >> 25
        chk = (chk & 0x1ffffff) << 5 ^ v
        for i in range(5):
            chk ^= gen[i] if ((top >> i) & 1) else 0
    return chk


def expand(hrp):
    return [ord(c) >> 5 for c in hrp] + [0] + [ord(c) & 31 for c in hrp]


def convertbits(data, frm, to):
    acc = bits = 0
    out = []
    maxv = (1 << to) - 1
    for value in data:
        acc = (acc << frm) | value
        bits += frm
        while bits >= to:
            bits -= to
            out.append((acc >> bits) & maxv)
    if bits >= frm or ((acc << (to - bits)) & maxv):
        return None                      # bad padding
    return out


def fail(reason):
    # The reason never quotes the address: this text is printed by the caller.
    sys.stderr.write(reason + "\n")
    sys.exit(1)


addr = os.environ["ARK0_ADDR"]
if addr != addr.lower() and addr != addr.upper():
    fail("mixes upper and lower case, which bech32 forbids")
addr = addr.lower()
if len(addr) > 90:
    fail("is longer than 90 characters, which bech32 forbids")
pos = addr.rfind("1")
if pos < 1 or pos + 7 > len(addr):
    fail("has no usable separator, so it is not a bech32 address")
hrp, body = addr[:pos], addr[pos + 1:]
if hrp != HRP:
    # Constant text. This check runs before the alphabet and checksum tests, so
    # `hrp` here is an arbitrary prefix of an arbitrary string: a hexadecimal
    # private key whose last character happens to be '1' reaches this line with
    # everything before that '1' sitting in `hrp`. Printing it would hand back
    # most of the key, from the check written to catch it.
    fail("has an unsupported network prefix; this is a signet and needs 'tb'")
try:
    data = [CHARSET.index(c) for c in body]
except ValueError:
    fail("contains a character that is not in the bech32 alphabet")
const = polymod(expand(hrp) + data)
if const == 1:
    spec = "bech32"
elif const == 0x2bc830a3:
    spec = "bech32m"
else:
    fail("has an invalid checksum, so it is mistyped or not an address")
version, program = data[0], convertbits(data[1:-6], 5, 8)
if program is None:
    fail("has a malformed witness program")
if version > 16:
    fail("has witness version %d, which does not exist" % version)
if version == 0:
    if spec != "bech32":
        fail("is witness version 0 but uses the bech32m checksum")
    if len(program) not in (20, 32):
        fail("is witness version 0 with a %d byte program; 20 or 32 are the only valid lengths" % len(program))
elif spec != "bech32m":
    fail("is witness version %d but uses the bech32 checksum" % version)
elif not 2 <= len(program) <= 40:
    fail("has a %d byte witness program, outside the 2 to 40 bytes BIP-350 allows" % len(program))
sys.exit(0)
PY

# Numeric bounds, not just shape: 999.999.999.999/99 matches a digits-and-dots
# pattern and is not an address range. Leading zeros are refused by the pattern
# rather than parsed, because 010 means different things to different tools.
#
# The five numbers come out of the match itself rather than out of the value.
# That is the point: `[ x -le 32 ]` prints x when x is not a number, so the
# only text this arithmetic is ever allowed to see is text the pattern has
# already proved is one to three digits. Splitting the raw value on '/' and
# hoping is what let a newline and a private key reach a Bash builtin.
cidr_re='^(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})/(0|[1-9][0-9]?)$'
[[ $ALLOWIP =~ $cidr_re ]] \
    || reject "rpcallowip" "is not an IPv4 CIDR in dotted-quad/prefix form"
cidr_octet_1="${BASH_REMATCH[1]}"
cidr_octet_2="${BASH_REMATCH[2]}"
cidr_octet_3="${BASH_REMATCH[3]}"
cidr_octet_4="${BASH_REMATCH[4]}"
cidr_prefix="${BASH_REMATCH[5]}"
for octet in "$cidr_octet_1" "$cidr_octet_2" "$cidr_octet_3" "$cidr_octet_4"; do
    [ "$octet" -le 255 ] || reject "rpcallowip" "has an octet above 255"
done
[ "$cidr_prefix" -le 32 ] \
    || reject "rpcallowip" "has a prefix length above 32, which IPv4 does not have"

# --- render -----------------------------------------------------------------
umask 077
sed -e "s|REPLACE_WITH_SIGNET_CHALLENGE|$CHALLENGE|g" \
    -e "s|REPLACE_WITH_REWARD_ADDRESS|$REWARD|g" \
    -e "s|^\( *\)rpcallowip=.*|\1rpcallowip=$ALLOWIP|" \
    "$TEMPLATE" > "$OUT"

grep -q REPLACE_WITH "$OUT" && die "a placeholder survived the substitution in $OUT"
# Both node configurations have to have been filled, not just the first.
n="$(grep -c "signetchallenge=$CHALLENGE" "$OUT" || true)"
[ "$n" = "2" ] || die "expected 2 filled signetchallenge lines in $OUT, found $n"

log "rendered $OUT"
printf '    challenge   %s...%s (%d hex digits)\n' \
    "$(printf '%s' "$CHALLENGE" | cut -c1-12)" \
    "$(printf '%s' "$CHALLENGE" | rev | cut -c1-6 | rev)" "${#CHALLENGE}"
printf '    reward      %s\n' "$REWARD"
printf '    rpcallowip  %s\n' "$ALLOWIP"

# --- diff -------------------------------------------------------------------
# kubectl diff exits 0 for no difference and 1 for a difference; anything above
# that is a real error. Both of the first two are results worth seeing, which
# is why this does not simply fail on a non-zero exit.
log "diff against the live configmap/$NAME"
set +e
$KUBECTL -n "$NS" diff -f "$OUT"
rc=$?
set -e
case "$rc" in
    0) log "no difference: the live configuration is exactly this render" ;;
    1) log "the lines above are what applying this would change" ;;
    *) die "kubectl diff failed (exit $rc); not applying anything" ;;
esac

if [ "$APPLY" != "1" ]; then
    echo
    log "nothing applied. If the diff above is what you intend:"
    printf '    %s -n %s apply -f %s\n' "$KUBECTL" "$NS" "$OUT"
    printf '    or re-run this with --apply\n'
    exit 0
fi

if [ "$rc" = "0" ]; then
    log "applying (a no-op: the diff was empty)"
else
    log "applying the change shown above"
fi
$KUBECTL -n "$NS" apply -f "$OUT"

# The nodes read this file at start-up. Changing it does not restart them, and
# that is deliberate: a node restart on this network is a decision, not a side
# effect. See ARK0-MIGRATION.md for the maintenance sequence that pauses the
# producer first.
log "applied. Running nodes keep their current configuration until they restart."

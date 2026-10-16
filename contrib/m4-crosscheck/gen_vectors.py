#!/usr/bin/env python3
"""Write the known-answer vector list the driver reads, from the files of a patched Bitcoin Core tree.

Usage: gen_vectors.py CORE_SRC_DIR OUTPUT

One line per vector: ID KIND EXPECTED PUBKEY MESSAGE CONTEXT SIGNATURE (hex; "-" is empty).
KIND is "wrap" (SLHDSA128SVerify as called by the script interpreter), "ext" (pure SLH-DSA with a
message of any length, the NIST ACVP external interface) or "int" (the internal interface).
EXPECTED is 1 for a valid signature and 0 for an invalid one.
"""
import json
import re
import sys

CTX = b"P2MR/SLHDSA128S/EXP0".hex()


def h(s):
    return s if s else "-"


def main(src, out):
    data = src + "/test/data/"
    lines = []

    rows = json.load(open(data + "slhdsa128s_acvp_sigver.json"))[1:]
    for r in rows:
        pk, msg, ctx, sig, ok, note = r
        tc = re.match(r"tcId (\d+)", note).group(1)
        lines.append((f"acvp-ext-{tc}", "ext", int(ok), pk, msg, ctx, sig))

    rows = json.load(open(data + "slhdsa128s_acvp_sigver_internal.json"))[1:]
    for r in rows:
        pk, msg, sig, ok, note = r
        tc = re.match(r"tcId (\d+)", note).group(1)
        lines.append((f"acvp-int-{tc}", "int", int(ok), pk, msg, "", sig))

    spend = json.load(open(data + "p2mr_ts_m4_spend.json"))
    for c in spend["cases"]:
        s = c["signer"]
        if s is None or s["scheme"] != "SLH-DSA-SHA2-128s":
            continue
        script = c["leafScript"]
        # The leaf is [OP_CODESEPARATOR] <32-byte key> OP_CHECKSLHDSA128S.
        assert script.endswith("f1")
        assert script[-68:-66] == "20"
        pk = script[-66:-2]
        sig = c["signature"][: 7856 * 2]  # a trailing hash type byte is not part of the signature
        lines.append((f"p2mr-ts-{c['id']}", "wrap", 1, pk, c["sigHash"], CTX, sig))

    fx = open(src + "/test/util/slhdsa128s_fixture.h").read()

    def literal(name):
        m = re.search(name + r"\{\s*((?:\"[^\"]*\"\s*)+)\}", fx)
        return "".join(re.findall(r"\"([^\"]*)\"", m.group(1)))

    ctx = literal("SLHDSA128S_FIXTURE_CONTEXT").encode().hex()
    lines.append(("fixture", "wrap", 1, literal("SLHDSA128S_FIXTURE_PUBKEY_HEX"),
                  literal("SLHDSA128S_FIXTURE_MSG_HEX"), ctx, literal("SLHDSA128S_FIXTURE_SIG_HEX")))

    with open(out, "w") as f:
        for i, k, ok, pk, msg, ctx, sig in lines:
            f.write(f"{i} {k} {ok} {h(pk)} {h(msg)} {h(ctx)} {h(sig)}\n")


main(sys.argv[1], sys.argv[2])

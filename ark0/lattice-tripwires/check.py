#!/usr/bin/env python3
"""Check the values in tripwires.json with Python's standard library alone.

    python3 check.py [path/to/tripwires.json]

For L1 it computes the NUMS ML-DSA-44 public key again from its domain separator and label. For each of
the three outputs it checks the public key's SHA-256, builds leaf A (S1 of the key, leaf version 0xc0)
and leaf B (OP_RETURN), the P2MR Merkle root (BIP 360: the TapLeaf and TapBranch tagged hashes of
BIP 341), the scriptPubKey (OP_2 <root>) and the address (bech32m, witness version 2, prefix tb), and
compares each with the file. Prints one line per output and exits 0 only when everything matches.
"""
import hashlib
import json
import pathlib
import sys

Q = 8380417   # FIPS 204: the modulus q
D = 13        # FIPS 204: bits dropped from t (Power2Round)
K = 4         # ML-DSA-44: t has k = 4 polynomials
N = 256       # coefficients per polynomial
LEAF_VERSION = 0xC0


def simple_bit_pack(coefficients, bits):
    """FIPS 204 SimpleBitPack (Algorithm 16): each coefficient as `bits` bits, least significant bit first."""
    acc = 0
    for i, c in enumerate(coefficients):
        acc |= c << (bits * i)
    return acc.to_bytes(bits * len(coefficients) // 8, "little")


def nums_public_key(dst, label):
    """The NUMS public key: X = SHAKE256(dst || 0x00 || label) read in order; rho = the first 32 bytes;
    each coefficient of t from the next three bytes, rejected unless below q (CoeffFromThreeBytes,
    Algorithm 14); t1 = the high part of Power2Round(t) (Algorithm 35); pk = pkEncode(rho, t1)
    (Algorithm 22)."""
    stream = hashlib.shake_256(dst + b"\x00" + label).digest(32 + K * N * 3 * 2)
    rho, pos = stream[:32], 32
    t1 = []
    for _ in range(K):
        poly = []
        while len(poly) < N:
            if pos + 3 > len(stream):
                raise SystemExit("the SHAKE256 stream ran out (it does not for the published inputs)")
            b0, b1, b2 = stream[pos], stream[pos + 1], stream[pos + 2]
            pos += 3
            z = b0 + 256 * b1 + 65536 * (b2 & 0x7F)
            if z < Q:
                poly.append((z + (1 << (D - 1)) - 1) >> D)
        t1.append(poly)
    return rho + b"".join(simple_bit_pack(poly, 10) for poly in t1)


def s1_leaf(pk):
    """Leaf A: the key in three pushes (OP_PUSHDATA2 520, 520, 272 bytes), then OP_CHECKMLDSA44 (0xf0)."""
    assert len(pk) == 1312
    out = b""
    for start, end in ((0, 520), (520, 1040), (1040, 1312)):
        out += b"\x4d" + (end - start).to_bytes(2, "little") + pk[start:end]
    return out + b"\xf0"


def tagged_hash(tag, msg):
    t = hashlib.sha256(tag.encode()).digest()
    return hashlib.sha256(t + t + msg).digest()


def compact_size(n):
    if n < 0xFD:
        return bytes([n])
    if n <= 0xFFFF:
        return b"\xfd" + n.to_bytes(2, "little")
    return b"\xfe" + n.to_bytes(4, "little")


def leaf_hash(script):
    return tagged_hash("TapLeaf", bytes([LEAF_VERSION]) + compact_size(len(script)) + script)


def merkle_root(script_a, script_b):
    """Two leaves at depth one: TapBranch of the two leaf hashes, the smaller first."""
    a, b = leaf_hash(script_a), leaf_hash(script_b)
    return tagged_hash("TapBranch", min(a, b) + max(a, b))


BECH32_CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
BECH32M_CONST = 0x2BC830A3


def _polymod(values):
    gen = (0x3B6A57B2, 0x26508E6D, 0x1EA119FA, 0x3D4233DD, 0x2A1462B3)
    chk = 1
    for v in values:
        top = chk >> 25
        chk = (chk & 0x1FFFFFF) << 5 ^ v
        for i in range(5):
            chk ^= gen[i] if (top >> i) & 1 else 0
    return chk


def segwit_address(hrp, version, program):
    """BIP 350 bech32m for witness version 1 and above."""
    acc, bits, data = 0, 0, [version]
    for byte in program:
        acc = (acc << 8) | byte
        bits += 8
        while bits >= 5:
            bits -= 5
            data.append((acc >> bits) & 31)
    if bits:
        data.append((acc << (5 - bits)) & 31)
    expanded = [ord(c) >> 5 for c in hrp] + [0] + [ord(c) & 31 for c in hrp]
    mod = _polymod(expanded + data + [0] * 6) ^ BECH32M_CONST
    checksum = [(mod >> 5 * (5 - i)) & 31 for i in range(6)]
    return hrp + "1" + "".join(BECH32_CHARSET[d] for d in data + checksum)


def check(path):
    doc = json.loads(pathlib.Path(path).read_bytes().decode("utf-8"))
    ok = True
    roles = []
    for out in doc["outputs"]:
        role = out["role"]
        roles.append(role)
        pk = bytes.fromhex(out["publicKey"])
        problems = []
        if role == "L1":
            nums = out["nums"]
            if nums_public_key(nums["dst"].encode(), nums["label"].encode()) != pk:
                problems.append("public key is not the NUMS key of its dst and label")
        if hashlib.sha256(pk).hexdigest() != out["publicKeySha256"]:
            problems.append("public key SHA-256")
        leaves = [s1_leaf(pk), b"\x6a"]
        if [x.hex() for x in leaves] != out["leafScripts"]:
            problems.append("leaf scripts")
        root = merkle_root(*leaves)
        if root.hex() != out["merkleRoot"]:
            problems.append("Merkle root")
        if (b"\x52\x20" + root).hex() != out["scriptPubKey"]:
            problems.append("scriptPubKey")
        if segwit_address("tb", 2, root) != out["address"]:
            problems.append("address")
        if out["amount"] != 10000:
            problems.append("amount")
        print(f"{role} {out['label']}: " + ("ok" if not problems else "MISMATCH: " + ", ".join(problems)))
        ok = ok and not problems
    if roles != ["L1", "L2", "L3"]:
        print("MISMATCH: the file does not list L1, L2 and L3 in that order")
        ok = False
    if len({out["publicKey"] for out in doc["outputs"]}) != len(doc["outputs"]):
        print("MISMATCH: two outputs share a key")
        ok = False
    return ok


if __name__ == "__main__":
    here = pathlib.Path(__file__).resolve().parent
    sys.exit(0 if check(sys.argv[1] if len(sys.argv) > 1 else here / "tripwires.json") else 1)

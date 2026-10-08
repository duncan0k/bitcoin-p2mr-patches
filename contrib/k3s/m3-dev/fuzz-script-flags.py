#!/usr/bin/env python3
"""Write inputs for the script_flags fuzz target that break its monotonicity check through one
of SCRIPT_VERIFY_P2MR, SCRIPT_VERIFY_P2MR_MLDSA44 and SCRIPT_VERIFY_P2MR_TRIPWIRE.

The target verifies an input under verify_flags, then removes fuzzed_flags from a pass or adds
them to a failure and asserts the result is the same. Each flag below makes a script execute that
without it meets an upgrade hook a DISCOURAGE flag refuses, so for each of them there are two
inputs: a pass with the flag that fails once it is removed (name ending in -drop) and a failure
without it that passes once it is added (-add). A fuzz binary whose target still fuzzes the flag
aborts on each input; one that leaves the flag alone runs all of them.

    python fuzz-script-flags.py <output directory>

Input layout (src/test/fuzz/script_flags.cpp): the transaction with witness, verify_flags and
fuzzed_flags as little-endian uint64, then one CTxOut per input (int64 value, compact-size
scriptPubKey).
"""
import hashlib
import struct
import sys
from pathlib import Path

# Bit positions of enum class script_verify_flag_name (src/script/interpreter.h).
P2SH = 1 << 0
WITNESS = 1 << 11
DISCOURAGE_UPGRADABLE_WITNESS_PROGRAM = 1 << 12
DISCOURAGE_UPGRADABLE_TAPROOT_VERSION = 1 << 18
DISCOURAGE_OP_SUCCESS = 1 << 19
P2MR = 1 << 21
P2MR_MLDSA44 = 1 << 22
P2MR_TRIPWIRE = 1 << 23

OP_0, OP_1, OP_2, OP_IF, OP_ENDIF, OP_CHECKSIG, OP_RETURN, OP_CHECKMLDSA44 = 0x00, 0x51, 0x52, 0x63, 0x68, 0xac, 0x6a, 0xf0


def tagged_hash(tag, data):
    t = hashlib.sha256(tag.encode()).digest()
    return hashlib.sha256(t + t + data).digest()


def compact_size(n):
    assert n < 0xfd
    return bytes([n])


def leaf_hash(version, script):
    return tagged_hash("TapLeaf", bytes([version]) + compact_size(len(script)) + script)


def branch_hash(a, b):
    return tagged_hash("TapBranch", min(a, b) + max(a, b))


def p2mr_spk(root):
    return bytes([OP_2, 32]) + root


def p2mr_spend(version, script, sibling=None):
    """scriptPubKey and witness of a P2MR output whose tree holds script at depth zero, or at
    depth one next to the node sibling."""
    leaf = leaf_hash(version, script)
    if sibling is None:
        return p2mr_spk(leaf), [script, bytes([version | 1])]
    return p2mr_spk(branch_hash(leaf, sibling)), [script, bytes([version | 1]) + sibling]


def serialize(spk, witness, verify_flags, fuzzed_flags):
    tx = struct.pack("<i", 2) + b"\x00\x01"
    tx += compact_size(1) + bytes(32) + struct.pack("<I", 0) + compact_size(0) + struct.pack("<I", 0xffffffff)
    tx += compact_size(1) + struct.pack("<q", 1000) + compact_size(1) + bytes([OP_1])
    tx += compact_size(len(witness)) + b"".join(compact_size(len(item)) + item for item in witness)
    tx += struct.pack("<I", 0)
    prevout = struct.pack("<q", 10000) + compact_size(len(spk)) + spk
    return tx + struct.pack("<QQ", verify_flags, fuzzed_flags) + prevout


def main():
    out = Path(sys.argv[1])
    out.mkdir(parents=True, exist_ok=True)

    # The Ark-0 NUMS tripwire output, as a check of the hashing above.
    nums = bytes.fromhex("50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0")
    nums_root = branch_hash(leaf_hash(0xc0, bytes([32]) + nums + bytes([OP_CHECKSIG])), leaf_hash(0xc0, bytes([OP_RETURN])))
    assert p2mr_spk(nums_root).hex() == "52202883f815a950fc2eb0d9d39f293f95e20d100a893c92d9122d958490b7a0d117"

    sibling = leaf_hash(0xc0, bytes([OP_RETURN]))
    base = P2SH | WITNESS
    cases = {
        # A P2MR output spent at depth zero: P2MR succeeds without executing, an unknown
        # witness program is refused by DISCOURAGE_UPGRADABLE_WITNESS_PROGRAM.
        "p2mr": (p2mr_spend(0xc0, bytes([OP_1])), base | DISCOURAGE_UPGRADABLE_WITNESS_PROGRAM, P2MR),
        # 0xf0 in a branch that is not taken: skipped by the OP_SUCCESSx pre-scan as
        # OP_CHECKMLDSA44, refused as OP_SUCCESS240 by DISCOURAGE_OP_SUCCESS.
        "mldsa44": (p2mr_spend(0xc0, bytes([OP_0, OP_IF, OP_CHECKMLDSA44, OP_ENDIF, OP_1]), sibling),
                    base | P2MR | DISCOURAGE_OP_SUCCESS, P2MR_MLDSA44),
        # A leaf of version 0xe0 at depth one: executed with P2MR_TRIPWIRE, an unknown leaf
        # version refused by DISCOURAGE_UPGRADABLE_TAPROOT_VERSION without it.
        "tripwire": (p2mr_spend(0xe0, bytes([OP_1]), sibling),
                     base | P2MR | DISCOURAGE_UPGRADABLE_TAPROOT_VERSION, P2MR_TRIPWIRE),
    }
    for name, ((spk, witness), flags, flag) in cases.items():
        (out / f"{name}-drop").write_bytes(serialize(spk, witness, flags | flag, flag))
        (out / f"{name}-add").write_bytes(serialize(spk, witness, flags, flag))
    for f in sorted(out.iterdir()):
        print(f.name, hashlib.sha256(f.read_bytes()).hexdigest())


if __name__ == "__main__":
    main()

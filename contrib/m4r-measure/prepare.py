#!/usr/bin/env python3
# Copyright (c) 2026 duncan0k
# Distributed under the MIT software license, see the accompanying
# file COPYING or http://www.opensource.org/licenses/mit-license.php.
"""Write the inputs of measure.cpp: the vector "r1" of p2mr_rescue_vectors.json (its K, suffix, y, 96-byte
m and proof), and a proof over a 65,535-byte message made with the test-only voleith-prove.

    prepare.py <Core tree> <voleith-prove> <output dir>
"""
import hashlib
import json
import os
import subprocess
import sys

core, prover, out = sys.argv[1:4]
os.makedirs(out, exist_ok=True)
with open(os.path.join(core, "src", "test", "data", "p2mr_rescue_vectors.json"), encoding="utf-8") as f:
    vec = next(v for v in json.load(f)["vectors"] if v["name"] == "r1")
inp = vec["inputs"][0]
tx = bytes.fromhex(vec["tx"])
# The annex is the last witness element of the only input: 0x50 0x01 and the proof, at the end of the
# witness, before the 4-byte nLockTime.
proof = tx[-4 - 130218:-4]
assert tx[-4 - 130220:-4 - 130218] == b"\x50\x01"
files = {"k": bytes.fromhex(inp["k"]), "suffix": bytes([inp["suffix"]]), "y": bytes.fromhex(inp["y"]),
         "m96": bytes.fromhex(inp["m"]), "proof96": proof}
# Any seed works for the long message: its own y, K and suffix are those of r1 only if the seed is r1's,
# which the vectors do not keep. Reuse K and suffix with a seed of our own, and write its y.
seed = hashlib.sha256(b"m4r-measure long message seed").digest()
m_long = bytes((i * 131 + 7) & 0xff for i in range(65535))
proof_path = os.path.join(out, "proof65535")
if os.path.exists(proof_path):
    os.remove(proof_path)
res = subprocess.run([prover, "--k", files["k"].decode(), "--suffix", str(inp["suffix"]), "--msg-hex", m_long.hex(), "--out", proof_path],
                     input=(seed.hex() + "\n").encode(), capture_output=True, check=True)
y_long = bytes.fromhex(res.stdout.decode().strip()[2:])
files["m65535"] = m_long
for name, data in files.items():
    with open(os.path.join(out, name), "wb") as f:
        f.write(data)
with open(os.path.join(out, "y65535"), "wb") as f:
    f.write(y_long)
print("inputs in", out)

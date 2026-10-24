"""Run the Go verifier of ark0-rescue f5a2bc0 and Core's (the vendored crate's voleith-verify) on every
proof of p2mr_rescue_vectors.json and p2mr_ts_rescue.json, with its own m, and on a copy with
chall3 = ff..ff; print each verdict and whether the two agree."""
import json, os, subprocess, sys, tempfile
core, go_bin, rust_bin = sys.argv[1:4]
sys.path.insert(0, os.path.join(core, "test", "functional"))
from test_framework.messages import CTransaction, from_hex  # noqa: E402

def verdict(binary, k, suffix, y, m, proof):
    with tempfile.NamedTemporaryFile(delete=False) as f:
        f.write(proof)
    try:
        r = subprocess.run([binary, "--k", k.decode(), "--suffix", str(suffix), "--y-hex", y.hex(), "--msg-hex", m.hex(), "--proof", f.name],
                           capture_output=True, text=True)
    finally:
        os.remove(f.name)
    assert r.returncode in (0, 1), (binary, r.returncode, r.stderr)
    return "valid" if r.returncode == 0 else "invalid"

cases = []
d = json.load(open(os.path.join(core, "src/test/data/p2mr_rescue_vectors.json")))
for v in d["vectors"]:
    tx = from_hex(CTransaction(), v["tx"])
    for i in v["inputs"]:
        annex = tx.wit.vtxinwit[i["index"]].scriptWitness.stack[-1]
        cases.append((f"core {v['name']} input {i['index']}", bytes.fromhex(i["k"]), i["suffix"], bytes.fromhex(i["y"]),
                      bytes.fromhex(i["m"]), annex[2:], "valid"))
t = json.load(open(os.path.join(core, "src/test/data/p2mr_ts_rescue.json")))
g, mid = t["transaction"]["given"], t["transaction"]["intermediate"]
for inp in t["transaction"]["inputs"]:
    annex = bytes.fromhex(inp["witness"][-1])
    for net in ("regtest", "signet"):
        cases.append((f"p2mr-ts input {inp['inputIndex']} m of {net}", g["K"].encode(), g["suffix"], bytes.fromhex(mid["y"]),
                      bytes.fromhex(inp["networks"][net]["m"]), annex[2:], "valid" if net == "regtest" else "invalid"))
disagree = 0
for name, k, suffix, y, m, proof, core_expected in cases:
    for label, p, expected in ((name, proof, core_expected), (name + ", chall3 = ff..ff", proof[:-16] + b"\xff" * 16, "invalid")):
        go, rust = verdict(go_bin, k, suffix, y, m, p), verdict(rust_bin, k, suffix, y, m, p)
        ok = go == rust == expected
        disagree += not ok
        print(f"{label}: Go {go}, Core {rust}, expected {expected}{'' if ok else '  <-- DIFFERS'}")
print(f"{2 * len(cases)} cases, {disagree} differing")

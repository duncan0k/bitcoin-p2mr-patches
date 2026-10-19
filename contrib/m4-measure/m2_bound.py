#!/usr/bin/env python3
"""An estimate, from measured figures with a margin, of what a regtest block leaves out of the connection
of an Ark-0 block (README.md, "What regtest leaves out"), and what adding it to every SLH-DSA block does to
the gate. It is an estimate under the assumptions below, not a mathematical bound on time.

Usage: m2_bound.py <out-dir of measure.sh> <results.json of wholeblock.py> [--margin M] [--coinbase-bytes B]

The model: an Ark-0 block above height 20000 differs from the gate's regtest blocks in
- one ML-DSA-44 verification of its block signature, taken as the highest median of the library-level
  VerifyMLDSA44 benchmark over the runs;
- one signet block solution check (every signet block): one signature check of the challenge, taken as
  the highest median of VerifySchnorrBIP340 (libsecp256k1 verifies ECDSA and Schnorr signatures at about
  the same cost);
- the hashing both need: each computes a modified Merkle root over the block's transactions, with the
  coinbase hashed again (its non-witness serialization, assumed to be at most B bytes with the signature
  push, default 3,000), plus a few tagged hashes; counted in SHA-256 compressions, each taken as the
  measured time of a worst-path SLH-DSA check divided by its 3,894 compressions (more than a compression
  costs: that time also holds the rest of the check's path);
- the about 2.4 KB of non-witness data the block signature adds to the coinbase, which leaves every block
  kind about 4 x B / 4,000,000 less weight for its checks.

The estimate is M (default 10) times the sum of the first three. The script then adds it to the maximum of
every gated block kind, adds nothing to the references, shrinks the references by the weight the
coinbase takes (so that they lose checks and the SLH-DSA blocks do not), and prints the largest ratio
before and after."""
import argparse, csv, glob, json, math, os, statistics

ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
ap.add_argument("single")
ap.add_argument("gate")
ap.add_argument("--margin", type=float, default=10.0)
ap.add_argument("--coinbase-bytes", type=int, default=3000)
args = ap.parse_args()

lib = {}
for fn in glob.glob(f"{args.single}/lib-*.json"):
    for r in json.load(open(fn))["results"]:
        lib.setdefault(r["name"], []).append(r["median(elapsed)"] / r.get("batch", 1) * 1e3)
mldsa_ms, schnorr_ms = max(lib["VerifyMLDSA44"]), max(lib["VerifySchnorrBIP340"])

worst = []
for d in glob.glob(f"{args.single}/samples-*-w1-*"):
    f = f"{d}/VerifyScriptP2MRSLHDSA128S.csv"
    if os.path.exists(f):
        worst.append(statistics.median(int(r["wall_ns"]) for r in csv.DictReader(open(f))) * 1e-6)
compression_ms = max(worst) / 3894

res = json.load(open(args.gate))
n_tx = max(b["inputs"] for b in res["blocks"].values()) + 1


def merkle_nodes(n):
    nodes = 0
    while n > 1:
        n = (n + 1) // 2
        nodes += n
    return nodes


coinbase_hash = math.ceil((args.coinbase_bytes + 9) / 64) + 1  # SHA256d of the coinbase
per_root = coinbase_hash + 3 * merkle_nodes(n_tx)  # SHA256d of 64 bytes per node: 2 + 1 compressions
compressions = 2 * per_root + 16  # two modified roots, and the tagged and challenge hashes
bound_ms = args.margin * (mldsa_ms + schnorr_ms + compressions * compression_ms)
shrink = 4 * args.coinbase_bytes / 4_000_000

print(f"ML-DSA-44 verification, taken as the highest median: {mldsa_ms:.4f} ms")
print(f"signature check of the signet solution (taken as the highest Schnorr verification median): {schnorr_ms:.4f} ms")
print(f"SHA-256 compressions: {compressions} ({n_tx} transactions, coinbase assumed at most {args.coinbase_bytes} bytes, "
      f"two modified Merkle roots), taken as {compression_ms * 1e3:.3f} us each: {compressions * compression_ms:.4f} ms")
print(f"estimate (margin {args.margin:g}): {args.margin:g} x {mldsa_ms + schnorr_ms + compressions * compression_ms:.4f} ms = {bound_ms:.3f} ms per block")
print(f"coinbase weight: {4 * args.coinbase_bytes} WU, {shrink:.2%} of the block, taken from the references only")
worst_before = worst_after = None
for e in res["summary"]:
    if e.get("gate_pass") is None:
        continue
    ref = next(x for x in res["summary"] if x["build"] == e["build"] and x["par"] == e["par"] and x["block"] == e.get("reference", "schnorr"))
    before = e["connect_ms_max"] / ref["connect_ms_median"]
    after = (e["connect_ms_max"] + bound_ms) / (ref["connect_ms_median"] * (1 - shrink))
    if worst_after is None or after > worst_after[0]:
        worst_after = (after, before, e["build"], e["par"], e["block"])
    print(f"{e['build']} | -par {e['par']} | {e['block']} | ratio {before:.3f} -> {after:.3f}")
after, before, b, p, k = worst_after
print(f"largest ratio with the estimate added: {after:.3f} ({b}, -par {p}, {k}; {before:.3f} without)")

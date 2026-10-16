#!/usr/bin/env python3
"""Whole-block gate of OP_CHECKSLHDSA128S on regtest (README.md, "Whole-block gate").

Builds, in Python with the functional test framework of the patched tree, a regtest chain and blocks
filled to the maximum block weight, every input of which uses up its validation budget (witness size
+ 50) to within one check's cost:

  slh               inputs that each check one valid SLH-DSA-SHA2-128s signature many times (copies of
                    its sixteen chunks taken with OP_PICK);
  schnorr           inputs that each check one Schnorr signature many times (OP_DUP ... OP_CHECKSIGVERIFY),
                    the most checks per unit of budget: the reference;
  mixed             a third of the weight each of SLH-DSA, ML-DSA-44 and Schnorr inputs of that kind;
  slh-invalid-last  the slh block with the signature of its last input made invalid: the time to reject.

Every block is connected by a fresh node with -maxsigcachesize=0 through `submitblock`, never through
the mempool, with -par=1 and with the default -par, in two builds:

  release  a normal bitcoind built from the series (--bitcoind-release): real verification;
  hook     bitcoind_m4_measure (build.sh) with the worst-case hook on (SLH_WORST=1): every SLH-DSA check
           takes the slowest path of the verification; any signature of the right length passes, so
           slh-invalid-last is measured in the release build only.

bitcoind_m4_measure also counts, per block, the complete SLH-DSA verifications and the SHA-256
compressions of the vendored library: in the hook runs, and in one run per block with the hook off
(-par=1, not timed) for the real path. The counters are plain counters, exact when one thread checks
the scripts, so the figures come from -par=1 runs; there the verifications counted must equal the
checks planned.

Gate: for each build and -par, the maximum connect time of every block kind is at most the median
connect time of the schnorr block of the same build and -par. Connect time is the time `submitblock`
takes, which is also what a rejected block can be measured by; the -debug=bench "Connect block" time
of accepted blocks is reported next to it.

Usage:
  python3 wholeblock.py --core <patched tree> --bitcoind-release <bitcoind> \\
      --bitcoind-measure <build-node>/bin/bitcoind_m4_measure --workdir <new directory> [--rounds 5]
"""

import argparse
import copy
import hashlib
import json
import os
import re
import statistics
import subprocess
import sys
import time
from pathlib import Path


def parse_args():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--core", required=True, help="the patched Core tree (for test/functional/test_framework)")
    ap.add_argument("--bitcoind-release", required=True, help="a normal bitcoind built from the series")
    ap.add_argument("--bitcoind-measure", required=True, help="bitcoind_m4_measure from build.sh")
    ap.add_argument("--workdir", required=True, help="a new directory for node data directories and results")
    ap.add_argument("--rounds", type=int, default=5, help="fresh nodes per block, build and -par (default 5)")
    ap.add_argument("--par", default="1,default", help="-par settings, 'default' for none (default 1,default)")
    ap.add_argument("--kinds", default="slh,schnorr,mixed,slh-invalid-last", help="block kinds")
    ap.add_argument("--builds", default="release,hook", help="builds to time (default release,hook)")
    ap.add_argument("--slh-checks", type=int, default=100, help="SLH-DSA checks of a full input (default 100)")
    ap.add_argument("--mldsa-checks", type=int, default=1000, help="ML-DSA-44 checks of a full input (default 1000)")
    ap.add_argument("--schnorr-checks", type=int, default=2000, help="Schnorr checks of a full input (default 2000)")
    ap.add_argument("--min-fill", type=float, default=0.995, help="least fill of what the coinbase leaves (default 0.995)")
    return ap.parse_args()


ARGS = parse_args()
sys.path.insert(0, str(Path(ARGS.core) / "test" / "functional"))

from test_framework.authproxy import AuthServiceProxy  # noqa: E402
from test_framework.blocktools import add_witness_commitment, create_block, create_coinbase  # noqa: E402
from test_framework.key import compute_xonly_pubkey, sign_schnorr  # noqa: E402
from test_framework.messages import (  # noqa: E402
    COutPoint,
    CTransaction,
    CTxIn,
    CTxInWitness,
    CTxOut,
    MAX_BLOCK_WEIGHT,
    ser_compact_size,
)
from test_framework.p2mr import p2mr_construct  # noqa: E402
from test_framework.p2mr_mldsa44 import (  # noqa: E402
    LEAF_FAIL,
    MLDSA44_PUBKEY_CHUNK_SIZES,
    MLDSA44_SIGNATURE_SIZE,
    NO_CODESEPARATOR,
    OP_CHECKMLDSA44,
    mldsa44_keygen,
    mldsa44_sig_chunks,
    mldsa44_sign_input,
    split_chunks,
)
from test_framework.p2mr_slhdsa128s import (  # noqa: E402
    OP_CHECKSLHDSA128S,
    SLHDSA128S_SIGNATURE_SIZE,
    VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK,
    slhdsa128s_keygen,
    slhdsa128s_sig_chunks,
    slhdsa128s_sign_input,
)
from test_framework.script import (  # noqa: E402
    CScript,
    LEAF_VERSION_TAPSCRIPT,
    OP_15,
    OP_7,
    OP_CHECKSIG,
    OP_CHECKSIGVERIFY,
    OP_DROP,
    OP_DUP,
    OP_PICK,
    OP_TRUE,
    OP_VERIFY,
    SIGHASH_DEFAULT,
    TaprootSignatureHash,
)

COINBASE_MATURITY = 100
VALIDATION_WEIGHT_PER_MLDSA44_CHECK = 250
SLH_PUB, SLH_SEC = slhdsa128s_keygen(hashlib.sha384(b"m4-measure slh-dsa key").digest())
MLDSA_PUB, MLDSA_SEC = mldsa44_keygen(hashlib.sha256(b"m4-measure ml-dsa-44 key").digest())
MLDSA_PUB_CHUNKS = split_chunks(MLDSA_PUB, MLDSA44_PUBKEY_CHUNK_SIZES)
SCHNORR_SEC = hashlib.sha256(b"m4-measure schnorr key").digest()
SCHNORR_PUB = compute_xonly_pubkey(SCHNORR_SEC)[0]


def witness_size(stack):
    return len(ser_compact_size(len(stack))) + sum(len(ser_compact_size(len(e))) + len(e) for e in stack)


def padding_ops(padding):
    ops = []
    while padding > 0:
        size = min(padding, 400)
        ops += [bytes(size), OP_DROP]
        padding -= size
    return ops


class Kind:
    """One kind of budget-filling input: its leaf for a number of checks, its signature stack, and the
    validation weight of one check."""

    def __init__(self, name, cost, sig_stack_sizes, leaf_ops):
        self.name, self.cost, self.sig_stack_sizes, self.leaf_ops = name, cost, sig_stack_sizes, leaf_ops
        self._trees = {}

    def tree(self, checks):
        """The tree of the leaf with the least padding whose spend has a budget of at least
        checks x cost, or None if even the unpadded leaf has a budget a whole check too large."""
        if checks not in self._trees:
            need = checks * self.cost - 50
            dummy = [bytes(size) for size in self.sig_stack_sizes]

            def build(padding):
                return p2mr_construct([("m", CScript(padding_ops(padding) + self.leaf_ops(checks)), LEAF_VERSION_TAPSCRIPT),
                                       ("fail", LEAF_FAIL, LEAF_VERSION_TAPSCRIPT)])

            def size(padding):
                leaf = build(padding).leaves["m"]
                return witness_size(dummy + [bytes(leaf.script), leaf.control_block])
            if size(0) > need + self.cost - 1:
                self._trees[checks] = None
            else:
                lo, hi = 0, max(0, need - size(0)) + 8
                while lo < hi:
                    mid = (lo + hi) // 2
                    if size(mid) >= need:
                        hi = mid
                    else:
                        lo = mid + 1
                budget = size(lo) + 50
                assert checks * self.cost <= budget < (checks + 1) * self.cost, (self.name, checks, budget)
                self._trees[checks] = build(lo)
        return self._trees[checks]

    def min_checks(self):
        checks = 1
        while self.tree(checks) is None:
            checks += 1
        return checks

    def weight(self, checks):
        return spend_tx(self, checks, (COutPoint(0, 0), 10**8), sign=False).get_weight()


KINDS = {
    "slh": Kind("slh", VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK, [520] * 15 + [56],
                lambda n: ([OP_15, OP_PICK] * 16 + [SLH_PUB, OP_CHECKSLHDSA128S, OP_VERIFY]) * (n - 1) + [SLH_PUB, OP_CHECKSLHDSA128S]),
    "mldsa": Kind("mldsa", VALIDATION_WEIGHT_PER_MLDSA44_CHECK, [520] * 4 + [340],
                  lambda n: list(MLDSA_PUB_CHUNKS) + ([OP_7, OP_PICK] * 8 + [OP_CHECKMLDSA44, OP_VERIFY]) * (n - 1) + [OP_CHECKMLDSA44]),
    "schnorr": Kind("schnorr", 50, [64],
                    lambda n: [OP_DUP, SCHNORR_PUB, OP_CHECKSIGVERIFY] * (n - 1) + [SCHNORR_PUB, OP_CHECKSIG]),
}
assert sum(KINDS["slh"].sig_stack_sizes) == SLHDSA128S_SIGNATURE_SIZE
assert sum(KINDS["mldsa"].sig_stack_sizes) == MLDSA44_SIGNATURE_SIZE


def spend_tx(kind, checks, utxo, sign=True):
    """A one-input, one-output transaction spending `utxo` (outpoint, value) through the leaf of `kind`
    with `checks` checks; without `sign`, a stack of the right sizes for weighing."""
    outpoint, value = utxo
    tree = kind.tree(checks)
    leaf = tree.leaves["m"]
    tx = CTransaction()
    tx.vin = [CTxIn(outpoint, b"", 0xffffffff)]
    tx.vout = [CTxOut(value - 1000, CScript([OP_TRUE]))]
    tx.wit.vtxinwit = [CTxInWitness()]
    spent = [CTxOut(value, tree.scriptPubKey)]
    if not sign:
        stack = [bytes(size) for size in kind.sig_stack_sizes]
    elif kind.name == "slh":
        stack = slhdsa128s_sig_chunks(slhdsa128s_sign_input(SLH_SEC, tx, spent, 0, leaf.script))
    elif kind.name == "mldsa":
        stack = mldsa44_sig_chunks(mldsa44_sign_input(MLDSA_SEC, tx, spent, 0, leaf.script))
    else:
        msg = TaprootSignatureHash(tx, spent, SIGHASH_DEFAULT, 0, scriptpath=True, leaf_script=leaf.script,
                                   leaf_ver=LEAF_VERSION_TAPSCRIPT, codeseparator_pos=NO_CODESEPARATOR)
        stack = [sign_schnorr(SCHNORR_SEC, msg)]
    tx.wit.vtxinwit[0].scriptWitness.stack = stack + [bytes(leaf.script), leaf.control_block]
    budget = witness_size(tx.wit.vtxinwit[0].scriptWitness.stack) + 50
    assert checks * kind.cost <= budget < (checks + 1) * kind.cost, (kind.name, checks, budget)
    return tx


def plan(kind, weight_left, full_checks):
    """Inputs of `kind` (their numbers of checks) that fill weight_left: full inputs, then the largest
    that fit the rest, down to the smallest input that uses up its budget."""
    plan_checks = []
    full_weight = kind.weight(full_checks)
    while weight_left >= full_weight:
        plan_checks.append(full_checks)
        weight_left -= full_weight
    smallest = kind.min_checks()
    while weight_left >= kind.weight(smallest):
        lo, hi = smallest, full_checks
        while lo < hi:
            mid = (lo + hi + 1) // 2
            if kind.weight(mid) <= weight_left:
                lo = mid
            else:
                hi = mid - 1
        plan_checks.append(lo)
        weight_left -= kind.weight(lo)
    return plan_checks


def make_block(prev_hash, height, ntime, txs):
    block = create_block(int(prev_hash, 16), create_coinbase(height, script_pubkey=CScript([OP_TRUE])), ntime, txlist=txs)
    add_witness_commitment(block)
    block.solve()
    return block


class Node:
    def __init__(self, binary, datadir, par, hook=None):
        self.datadir = Path(datadir)
        self.datadir.mkdir(parents=True)
        self.port = 30000 + (os.getpid() % 2000) * 2
        args = [binary, "-regtest", f"-datadir={self.datadir}", "-maxsigcachesize=0", "-debug=bench", "-listen=0",
                "-connect=0", "-dnsseed=0", "-server", "-rpcuser=m", "-rpcpassword=m", f"-rpcport={self.port}",
                "-printtoconsole=0"]
        if par != "default":
            args.append(f"-par={par}")
        env = dict(os.environ)
        if hook is not None:
            env["SLH_WORST"] = "1" if hook else "0"
        self.hook = hook
        self.proc = subprocess.Popen(args, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        url = f"http://m:m@127.0.0.1:{self.port}"
        for _ in range(1200):
            try:
                AuthServiceProxy(url, timeout=10).getblockcount()
                break
            except Exception:
                if self.proc.poll() is not None:
                    raise RuntimeError(f"{binary} exited: {self.proc.stderr.read().decode()}")
                time.sleep(0.1)
        else:
            raise RuntimeError(f"{binary} did not answer RPC")
        self.rpc = AuthServiceProxy(url, timeout=3600)

    def stop(self):
        self.rpc.stop()
        stderr = self.proc.communicate(timeout=600)[1].decode()
        counters = {}
        if self.hook is not None:
            if f"SLH worst-case hook: {1 if self.hook else 0}" not in stderr:
                raise RuntimeError(f"the measurement node did not report the hook {'on' if self.hook else 'off'}: {stderr!r}")
            m = re.search(r"counters: slh_verifications=(\d+) sha256_compressions=(\d+)", stderr)
            counters = {"slh_verifications": int(m.group(1)), "sha256_compressions": int(m.group(2))}
        return counters

    def bench_connect_ms(self):
        lines = (self.datadir / "regtest" / "debug.log").read_text().splitlines()
        connect = [float(m.group(1)) for line in lines if (m := re.search(r"- Connect block: ([0-9.]+)ms", line))]
        return connect[-1] if connect else None


def run_block(binary, workdir, chain, block, par, hook, expect_valid):
    node = Node(binary, workdir, par, hook)
    for b in chain:
        assert node.rpc.submitblock(b.serialize().hex()) is None
    tip = node.rpc.getbestblockhash()
    start = time.perf_counter()
    result = node.rpc.submitblock(block.serialize().hex())
    elapsed_ms = (time.perf_counter() - start) * 1000
    best = node.rpc.getbestblockhash()
    bench = node.bench_connect_ms() if expect_valid else None
    counters = node.stop()
    if expect_valid and not (result is None and best == block.hash_hex):
        raise RuntimeError(f"block not connected: {result}")
    if not expect_valid and not (result is not None and "Invalid SLH-DSA-SHA2-128s signature" in result and best == tip):
        raise RuntimeError(f"invalid block not rejected as expected: {result}")
    return elapsed_ms, bench, counters


def main():
    work = Path(ARGS.workdir)
    work.mkdir(parents=False, exist_ok=False)
    kinds = ARGS.kinds.split(",")
    pars = ARGS.par.split(",")
    builds = ARGS.builds.split(",")
    full = {"slh": ARGS.slh_checks, "mldsa": ARGS.mldsa_checks, "schnorr": ARGS.schnorr_checks}

    # What a block with only its coinbase leaves of the maximum weight.
    empty = make_block("01" * 32, COINBASE_MATURITY + 3, 1, [])
    available = MAX_BLOCK_WEIGHT - empty.get_weight() - 4  # room for a larger transaction count
    plans = {}
    for name in ("slh", "schnorr", "mixed"):
        if name == "mixed":
            third = available // 3
            p_slh = plan(KINDS["slh"], third, full["slh"])
            used = sum(KINDS["slh"].weight(c) for c in p_slh)
            p_ml = plan(KINDS["mldsa"], 2 * third - used, full["mldsa"])
            used += sum(KINDS["mldsa"].weight(c) for c in p_ml)
            p_sch = plan(KINDS["schnorr"], available - used, full["schnorr"])
            plans[name] = [("slh", c) for c in p_slh] + [("mldsa", c) for c in p_ml] + [("schnorr", c) for c in p_sch]
        else:
            plans[name] = [(name, c) for c in plan(KINDS[name], available, full[name])]

    # The chain: blocks paying to OP_TRUE, then one funding an output for every planned input.
    print("building the chain and signing", flush=True)
    t0 = 1296688602 + 1
    prev = "0f9188f13cb7b2c71f2a335e3a4fc328bf5beb436012afca590b1a11466e2206"  # regtest genesis
    chain = []
    for height in range(1, COINBASE_MATURITY + 3):
        block = make_block(prev, height, t0 + height, [])
        chain.append(block)
        prev = block.hash_hex
    fund = CTransaction()
    fund.vin = [CTxIn(COutPoint(chain[i].vtx[0].txid_int, 0), b"", 0xffffffff) for i in range(2)]
    entries = [(name, kind, checks) for name in ("slh", "schnorr", "mixed") for kind, checks in plans[name]]
    value = (2 * chain[0].vtx[0].vout[0].nValue - 100_000) // len(entries)
    fund.vout = [CTxOut(value, KINDS[kind].tree(checks).scriptPubKey) for _, kind, checks in entries]
    height = COINBASE_MATURITY + 3
    chain.append(make_block(prev, height, t0 + height, [fund]))
    prev = chain[-1].hash_hex
    height += 1
    outputs = {name: [] for name in plans}
    for i, (name, kind, checks) in enumerate(entries):
        outputs[name].append((COutPoint(fund.txid_int, i), value))

    blocks = {}
    for name in ("slh", "schnorr", "mixed"):
        txs = [spend_tx(KINDS[kind], checks, outputs[name][i]) for i, (kind, checks) in enumerate(plans[name])]
        blocks[name] = make_block(prev, height, t0 + height, txs)
    # slh-invalid-last: the slh block with one byte of its last input's signature changed (its last
    # chunk, in the last hypertree layer), so that the check runs to the end and then fails.
    bad = copy.deepcopy(blocks["slh"].vtx[-1])
    stack = list(bad.wit.vtxinwit[0].scriptWitness.stack)
    stack[15] = stack[15][:-1] + bytes([stack[15][-1] ^ 1])
    bad.wit.vtxinwit[0].scriptWitness.stack = stack
    blocks["slh-invalid-last"] = make_block(prev, height, t0 + height, blocks["slh"].vtx[1:-1] + [bad])
    plans["slh-invalid-last"] = plans["slh"]

    info = {}
    for name in kinds:
        block = blocks[name]
        planned = {k: sum(c for kk, c in plans[name] if kk == k) for k in KINDS}
        fill = (block.get_weight() - empty.get_weight()) / (MAX_BLOCK_WEIGHT - empty.get_weight())
        assert block.get_weight() <= MAX_BLOCK_WEIGHT
        assert fill >= ARGS.min_fill, (name, fill)
        info[name] = {"weight": block.get_weight(), "fill": fill, "inputs": len(block.vtx) - 1, "planned": planned}
        if name == "slh-invalid-last":
            info[name]["planned_slh_to_failure"] = planned["slh"] - plans[name][-1][1] + 1
        print(f"block {name}: weight {block.get_weight()}, fill {fill:.4%}, {len(block.vtx) - 1} inputs, "
              f"checks planned {planned}", flush=True)

    runs = []
    # The real path's counters: bitcoind_m4_measure with the hook off, -par=1, once per block.
    for name in kinds:
        valid = name != "slh-invalid-last"
        _, _, counters = run_block(ARGS.bitcoind_measure, work / f"count-{name}", chain, blocks[name], "1", False, valid)
        info[name]["counted_real"] = counters
        print(f"counted, hook off, {name}: {counters}", flush=True)
    for build in builds:
        for name in kinds:
            valid = name != "slh-invalid-last"
            if build == "hook" and not valid:
                continue
            for par in pars:
                for rnd in range(ARGS.rounds):
                    binary = ARGS.bitcoind_release if build == "release" else ARGS.bitcoind_measure
                    hook = None if build == "release" else True
                    ms, bench, counters = run_block(binary, work / f"{build}-{name}-par{par}-r{rnd + 1}", chain, blocks[name], par, hook, valid)
                    row = {"build": build, "block": name, "par": par, "round": rnd + 1, "connect_ms": ms,
                           "bench_connect_ms": bench, **counters}
                    runs.append(row)
                    print(json.dumps(row), flush=True)

    # Counted verifications against the plan.
    for name in kinds:
        expected = info[name].get("planned_slh_to_failure", info[name]["planned"]["slh"])
        counted = info[name]["counted_real"]["slh_verifications"]
        assert counted == expected, (name, "hook off", counted, expected)
        for row in runs:
            if row["build"] == "hook" and row["block"] == name:
                assert row["slh_verifications"] == expected or row["par"] != "1", (name, "hook", row)

    summary = []
    print("\nbuild | -par | block | weight | fill | inputs | checks SLH/ML-DSA/Schnorr planned | SLH counted (hook / real) | "
          "compressions per SLH check (hook / real) | connect ms median | max | bench median | max/schnorr median | gate")
    gate_ok = True
    for build in builds:
        for par in pars:
            ref = [r["connect_ms"] for r in runs if r["build"] == build and r["par"] == par and r["block"] == "schnorr"]
            ref_median = statistics.median(ref) if ref else None
            for name in kinds:
                rows = [r for r in runs if r["build"] == build and r["par"] == par and r["block"] == name]
                if not rows:
                    continue
                times = [r["connect_ms"] for r in rows]
                benches = [r["bench_connect_ms"] for r in rows if r["bench_connect_ms"] is not None]
                # The counters are plain counters, exact only when one thread checks the scripts: they
                # come from the -par=1 runs of the same build and block.
                single = [r for r in runs if r["build"] == build and r["par"] == "1" and r["block"] == name]
                hook_counts = [r.get("slh_verifications") for r in single if "slh_verifications" in r]
                hook_comp = [r["sha256_compressions"] / r["slh_verifications"] for r in single if r.get("slh_verifications")]
                real = info[name]["counted_real"]
                real_comp = real["sha256_compressions"] / real["slh_verifications"] if real["slh_verifications"] else None
                ratio = max(times) / ref_median if ref_median else None
                passed = None if name == "schnorr" or ratio is None else ratio <= 1
                if passed is False:
                    gate_ok = False
                p = info[name]["planned"]
                entry = {"build": build, "par": par, "block": name, "weight": info[name]["weight"], "fill": info[name]["fill"],
                         "inputs": info[name]["inputs"], "planned": p, "slh_counted_hook": hook_counts[0] if hook_counts else None,
                         "slh_counted_real": real["slh_verifications"],
                         "compressions_per_check_hook": statistics.median(hook_comp) if hook_comp else None,
                         "compressions_per_check_real": real_comp, "connect_ms_median": statistics.median(times),
                         "connect_ms_max": max(times), "bench_connect_ms_median": statistics.median(benches) if benches else None,
                         "ratio_max_to_schnorr_median": ratio, "gate_pass": passed}
                summary.append(entry)
                fmt = lambda x, f="{:.1f}": "-" if x is None else f.format(x)  # noqa: E731
                print(f"{build} | {par} | {name} | {entry['weight']} | {entry['fill']:.4%} | {entry['inputs']} | "
                      f"{p['slh']}/{p['mldsa']}/{p['schnorr']} | {fmt(entry['slh_counted_hook'], '{}')} / {real['slh_verifications']} | "
                      f"{fmt(entry['compressions_per_check_hook'])} / {fmt(real_comp)} | {entry['connect_ms_median']:.1f} | "
                      f"{entry['connect_ms_max']:.1f} | {fmt(entry['bench_connect_ms_median'])} | {fmt(ratio, '{:.3f}')} | "
                      f"{'-' if passed is None else ('pass' if passed else 'FAIL')}")
    (work / "results.json").write_text(json.dumps({"blocks": info, "runs": runs, "summary": summary}, indent=1))
    print(f"\ngate (every block kind's maximum <= the schnorr median, per build and -par): {'PASS' if gate_ok else 'FAIL'}")
    return 0 if gate_ok else 1


if __name__ == "__main__":
    sys.exit(main())

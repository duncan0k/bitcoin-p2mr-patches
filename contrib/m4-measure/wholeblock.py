#!/usr/bin/env python3
"""Whole-block measurement of OP_CHECKSLHDSA128S on regtest (README.md, "Whole-block test").

Builds, in Python, a regtest chain and three blocks on top of it that use up the block weight with
spends whose validation budget is used up by their checks:

  slh      every input checks one SLH-DSA-SHA2-128s signature many times (copies of its sixteen
           chunks taken with OP_PICK), with as much padding in the leaf as the budget needs;
  schnorr  every input checks one Schnorr signature many times (OP_DUP ... OP_CHECKSIGVERIFY), the
           most checks per unit of budget (50 each);
  mixed    about half the block weight of each.

Each block is connected with `submitblock` by a fresh node, the measurement-only bitcoind_m4_measure
(build.sh), started with -maxsigcachesize=0 and once with -par=1, once with the default -par, and
with the worst-case hook on (SLH_WORST=1): every WOTS+ chain at its longest, the hypertree judged
valid, so the SLH-DSA checks take the slowest path of the verification, and any signature of the
right length passes. The script reports the time to connect each block (from -debug=bench) and the
ratio of the SLH-DSA block to the Schnorr block.

Usage:
  python3 wholeblock.py --core <patched Core tree> --bitcoind <build-node>/bin/bitcoind_m4_measure \\
                        --workdir <new directory> [--rounds 3] [--fill 0.95]
"""

import argparse
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
    ap.add_argument("--bitcoind", required=True, help="bitcoind_m4_measure from build.sh")
    ap.add_argument("--workdir", required=True, help="a new directory for the node data directories and the results")
    ap.add_argument("--rounds", type=int, default=3, help="fresh nodes per block and -par setting (default 3)")
    ap.add_argument("--fill", type=float, default=0.95, help="fraction of the maximum block weight to use (default 0.95)")
    ap.add_argument("--slh-checks", type=int, default=40, help="SLH-DSA checks per input (default 40)")
    ap.add_argument("--schnorr-checks", type=int, default=2000, help="Schnorr checks per input (default 2000)")
    ap.add_argument("--kinds", default="slh,schnorr,mixed", help="blocks to measure (default slh,schnorr,mixed)")
    ap.add_argument("--par", default="1,default", help="-par settings, 'default' for none (default 1,default)")
    return ap.parse_args()


ARGS = parse_args()
sys.path.insert(0, str(Path(ARGS.core) / "test" / "functional"))

from test_framework.authproxy import AuthServiceProxy  # noqa: E402
from test_framework.blocktools import add_witness_commitment, create_block, create_coinbase  # noqa: E402
from test_framework.key import compute_xonly_pubkey, sign_schnorr  # noqa: E402
from test_framework.messages import (  # noqa: E402
    COIN,
    COutPoint,
    CTransaction,
    CTxIn,
    CTxInWitness,
    CTxOut,
    MAX_BLOCK_WEIGHT,
    ser_compact_size,
)
from test_framework.p2mr_slhdsa128s import (  # noqa: E402
    NO_CODESEPARATOR,
    OP_CHECKSLHDSA128S,
    SLHDSA128S_SIGNATURE_SIZE,
    VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK,
    slhdsa128s_sig_chunks,
    slhdsa128s_tree,
)
from test_framework.script import (  # noqa: E402
    CScript,
    LEAF_VERSION_TAPSCRIPT,
    OP_15,
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

C_SLH = VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK
C_SCHNORR = 50
COINBASE_MATURITY = 100
# With the hook on, the public key and the signature need not match: any 32-byte key and any
# signature of the right length take the worst-case path and pass.
SLH_PUB = hashlib.sha256(b"m4-measure slh public key").digest()
SLH_SIG = b"".join(hashlib.sha256(b"m4-measure slh signature %d" % i).digest() for i in range(246))[:SLHDSA128S_SIGNATURE_SIZE]
SCHNORR_SEC = bytes.fromhex("0101010101010101010101010101010101010101010101010101010101010101")
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


def slh_leaf(checks, padding):
    """Checks one SLH-DSA signature `checks` times, all but the last on copies of its chunks."""
    ops = padding_ops(padding)
    for _ in range(checks - 1):
        ops += [OP_15, OP_PICK] * 16 + [SLH_PUB, OP_CHECKSLHDSA128S, OP_VERIFY]
    return CScript(ops + [SLH_PUB, OP_CHECKSLHDSA128S])


def schnorr_leaf(checks, padding):
    """Checks one Schnorr signature `checks` times."""
    ops = padding_ops(padding)
    ops += [OP_DUP, SCHNORR_PUB, OP_CHECKSIGVERIFY] * (checks - 1)
    return CScript(ops + [SCHNORR_PUB, OP_CHECKSIG])


def smallest_padding(make_leaf, checks, sig_stack, cost):
    """The tree with the least padding whose spend has a budget (witness size + 50) of at least
    checks x cost."""
    def spend_size(padding):
        leaf = slhdsa128s_tree(make_leaf(checks, padding)).leaves["slh"]
        return witness_size(sig_stack + [bytes(leaf.script), leaf.control_block])
    need = checks * cost - 50
    lo, hi = 0, max(0, need - spend_size(0)) + 8
    while lo < hi:
        mid = (lo + hi) // 2
        if spend_size(mid) >= need:
            hi = mid
        else:
            lo = mid + 1
    return slhdsa128s_tree(make_leaf(checks, lo))


class Kind:
    def __init__(self, name, tree, checks, cost):
        self.name, self.tree, self.checks, self.cost = name, tree, checks, cost
        self.leaf = tree.leaves["slh"]


def spend_tx(kind, utxo):
    """A one-input, one-output transaction spending `utxo` (outpoint, value) of `kind`'s tree."""
    outpoint, value = utxo
    tx = CTransaction()
    tx.vin = [CTxIn(outpoint, b"", 0xffffffff)]
    tx.vout = [CTxOut(value - 10_000, CScript([OP_TRUE]))]
    tx.wit.vtxinwit = [CTxInWitness()]
    if kind.name == "slh":
        stack = slhdsa128s_sig_chunks(SLH_SIG)
    else:
        spent = [CTxOut(value, kind.tree.scriptPubKey)]
        msg = TaprootSignatureHash(tx, spent, SIGHASH_DEFAULT, 0, scriptpath=True, leaf_script=kind.leaf.script,
                                   leaf_ver=LEAF_VERSION_TAPSCRIPT, codeseparator_pos=NO_CODESEPARATOR)
        stack = [sign_schnorr(SCHNORR_SEC, msg)]
    tx.wit.vtxinwit[0].scriptWitness.stack = stack + [bytes(kind.leaf.script), kind.leaf.control_block]
    return tx


def make_block(prev_hash, height, ntime, txs):
    block = create_block(int(prev_hash, 16), create_coinbase(height, script_pubkey=CScript([OP_TRUE])), ntime, txlist=txs)
    add_witness_commitment(block)
    block.solve()
    return block


class Node:
    def __init__(self, datadir, par):
        self.datadir = Path(datadir)
        self.datadir.mkdir(parents=True)
        self.port = 30000 + (os.getpid() % 2000) * 2
        args = [ARGS.bitcoind, "-regtest", f"-datadir={self.datadir}", "-maxsigcachesize=0", "-debug=bench",
                "-listen=0", "-server", "-rpcuser=m", "-rpcpassword=m", f"-rpcport={self.port}", "-printtoconsole=0"]
        if par != "default":
            args.append(f"-par={par}")
        self.proc = subprocess.Popen(args, env=dict(os.environ, SLH_WORST="1"),
                                     stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        url = f"http://m:m@127.0.0.1:{self.port}"
        for _ in range(1200):
            try:
                AuthServiceProxy(url, timeout=10).getblockcount()
                break
            except Exception:
                if self.proc.poll() is not None:
                    raise RuntimeError(f"bitcoind_m4_measure exited: {self.proc.stderr.read().decode()}")
                time.sleep(0.1)
        else:
            raise RuntimeError("bitcoind_m4_measure did not answer RPC")
        self.rpc = AuthServiceProxy(url, timeout=3600)
        banner = self.proc.stderr.readline().decode()
        if "SLH worst-case hook: 1" not in banner:
            raise RuntimeError(f"the node did not report the worst-case hook on: {banner!r}")

    def stop(self):
        self.rpc.stop()
        self.proc.wait(timeout=600)

    def bench_lines(self):
        return (self.datadir / "regtest" / "debug.log").read_text().splitlines()


def main():
    work = Path(ARGS.workdir)
    work.mkdir(parents=False, exist_ok=False)
    kinds = {}
    slh_tree = smallest_padding(slh_leaf, ARGS.slh_checks, slhdsa128s_sig_chunks(SLH_SIG), C_SLH)
    kinds["slh"] = Kind("slh", slh_tree, ARGS.slh_checks, C_SLH)
    sch_tree = smallest_padding(schnorr_leaf, ARGS.schnorr_checks, [bytes(64)], C_SCHNORR)
    kinds["schnorr"] = Kind("schnorr", sch_tree, ARGS.schnorr_checks, C_SCHNORR)

    # How many inputs of each kind fit the block weight.
    limit = int(MAX_BLOCK_WEIGHT * ARGS.fill)
    dummy_utxo = (COutPoint(0, 0), 50 * COIN)
    weight = {k: spend_tx(kinds[k], dummy_utxo).get_weight() for k in kinds}
    plan = {
        "slh": [("slh", (limit - 4000) // weight["slh"])],
        "schnorr": [("schnorr", (limit - 4000) // weight["schnorr"])],
        "mixed": [("slh", (limit - 4000) // 2 // weight["slh"]), ("schnorr", (limit - 4000) // 2 // weight["schnorr"])],
    }
    need = {k: max(n for block in plan.values() for kk, n in block if kk == k) for k in kinds}

    # The chain: COINBASE_MATURITY + 1 blocks paying to OP_TRUE, then one block funding the outputs.
    print("building the chain", flush=True)
    setup_node = Node(work / "setup", "default")
    prev = setup_node.rpc.getbestblockhash()
    t0 = setup_node.rpc.getblockheader(prev)["time"] + 1
    setup_node.stop()
    chain = []
    for height in range(1, COINBASE_MATURITY + 2):
        block = make_block(prev, height, t0 + height, [])
        chain.append(block)
        prev = block.hash_hex
    coinbase = chain[0].vtx[0]
    fund = CTransaction()
    fund.vin = [CTxIn(COutPoint(coinbase.txid_int, 0), b"", 0xffffffff)]
    per_output = (coinbase.vout[0].nValue - 100_000) // (need["slh"] + need["schnorr"])
    for k in ("slh", "schnorr"):
        fund.vout += [CTxOut(per_output, kinds[k].tree.scriptPubKey)] * need[k]
    height = COINBASE_MATURITY + 2
    funding_block = make_block(prev, height, t0 + height, [fund])
    chain.append(funding_block)
    prev = funding_block.hash_hex
    utxos = {"slh": [], "schnorr": []}
    for i, out in enumerate(fund.vout):
        utxos["slh" if i < need["slh"] else "schnorr"].append((COutPoint(fund.txid_int, i), out.nValue))

    blocks = {}
    height += 1
    for name in ARGS.kinds.split(","):
        txs = []
        checks = {"slh": 0, "schnorr": 0}
        for k, n in plan[name]:
            txs += [spend_tx(kinds[k], utxos[k][i]) for i in range(n)]
            checks[k] += n * kinds[k].checks
        block = make_block(prev, height, t0 + height, txs)
        assert block.get_weight() <= MAX_BLOCK_WEIGHT
        blocks[name] = (block, checks)
        print(f"block {name}: {len(txs)} transactions, weight {block.get_weight()}, "
              f"{checks['slh']} SLH-DSA checks, {checks['schnorr']} Schnorr checks", flush=True)

    results = []
    for name, (block, checks) in blocks.items():
        for par in ARGS.par.split(","):
            for rnd in range(ARGS.rounds):
                node = Node(work / f"{name}-par{par}-r{rnd + 1}", par)
                for b in chain:
                    assert node.rpc.submitblock(b.serialize().hex()) is None
                start = time.perf_counter()
                result = node.rpc.submitblock(block.serialize().hex())
                elapsed = time.perf_counter() - start
                ok = result is None and node.rpc.getbestblockhash() == block.hash_hex
                lines = node.bench_lines()
                node.stop()
                if not ok:
                    raise RuntimeError(f"block {name} was not connected: {result}")
                connect = [float(m.group(1)) for line in lines if (m := re.search(r"- Connect block: ([0-9.]+)ms", line))]
                verify = [float(m.group(1)) for line in lines if (m := re.search(r"- Verify [0-9]+ txins: ([0-9.]+)ms", line))]
                row = {"block": name, "par": par, "round": rnd + 1, "submitblock_ms": elapsed * 1000,
                       "connect_block_ms": connect[-1], "verify_txins_ms": verify[-1], "weight": block.get_weight(), **checks}
                results.append(row)
                print(json.dumps(row), flush=True)

    (work / "results.json").write_text(json.dumps(results, indent=1))
    print("\nblock | -par | connect block ms (median) | verify txins ms (median) | SLH-DSA checks | Schnorr checks")
    medians = {}
    for name in blocks:
        for par in ARGS.par.split(","):
            rows = [r for r in results if r["block"] == name and r["par"] == par]
            medians[(name, par)] = statistics.median(r["connect_block_ms"] for r in rows)
            print(f"{name} | {par} | {medians[(name, par)]:.1f} | {statistics.median(r['verify_txins_ms'] for r in rows):.1f} | "
                  f"{rows[0]['slh']} | {rows[0]['schnorr']}")
    if "slh" in blocks and "schnorr" in blocks:
        for par in ARGS.par.split(","):
            print(f"ratio SLH-DSA block / Schnorr block, -par={par}: {medians[('slh', par)] / medians[('schnorr', par)]:.3f}")


if __name__ == "__main__":
    main()

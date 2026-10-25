#!/usr/bin/env python3
# Copyright (c) 2026 duncan0k
# Distributed under the MIT software license, see the accompanying
# file COPYING or http://www.opensource.org/licenses/mit-license.php.
"""Whole-block gate of the P2MR rescue lane on regtest (`doc/p2mr-rescue.md`, "Validation weight";
README.md, "Whole-block gate").

Builds, in Python with the functional test framework of the patched tree, a regtest chain and blocks
filled to the maximum block weight by their actual weight:

  rescue                     rescue spends in template R1, one rescue check per input (rule R3 allows
                             no more), as many as fit (kind 1);
  rescue-invalid-last        that block with the last input's proof made the most expensive invalid
                             proof (chall3 = ff..ff), failing R7 (kind 3, release control);
  rescue-tail                inputs whose 0xe0 leaf first makes as many Schnorr checks as the rest of the
                             input's budget pays for and then one rescue check, as many as fit, the rest
                             of the block filled with Schnorr inputs (the layout of kind 3);
  rescue-tail-invalid-last   that block with the last input's rescue check failing R7, every other check
                             valid (kind 3, release control);
  mixed                      a quarter of the weight each of SLH-DSA-SHA2-128s, ML-DSA-44 and R1 rescue
                             inputs, the rest Schnorr inputs (kind 2);
  schnorr                    the reference: inputs that use their whole budget on Schnorr checks, the
                             most checks per unit of budget, filled down to the smallest such input.

Every block is connected by a new node with -maxsigcachesize=0 through `submitblock`, never through the
mempool, at -par=1 and the default -par, in three builds:

  release   a normal bitcoind built from the series (--bitcoind-release): every kind;
  hook      bitcoind_m4r_measure (build.sh) with M4R_WORST=1: every rescue proof is preceded by a
            verification of the same proof with chall3 = ff..ff, the most expensive invalid proof for
            its statement and message, and keeps its own result. Its all-valid blocks bound every
            invalid block of the same layout (README.md): rescue, rescue-tail, mixed, schnorr;
  portable  the same with M4R_PORTABLE=1 as well, every GF(2^128) product on the portable path, as on a
            CPU without PCLMULQDQ: rescue, rescue-tail, mixed, schnorr. On a CPU without PCLMULQDQ the
            hook build is portable already; the results record the CPU's own capability and the forced
            switch apart.

Counts: each block once more, by bitcoind_m4r_measure at -par=1 (one thread, in order) with its counters
on (M4R_COUNT=1) and the hook off (and on, for the hook kinds); its counts of complete verifications
(rescue proofs, worst-case hook proofs, Schnorr, ML-DSA-44, SLH-DSA) must equal what the layout gives.
The timed runs leave the counters off, so that they cost nothing there; the series' own count of rescue
proofs, which a release node keeps too, is still checked in them.

Gate: for each build and -par, the maximum connect time (submitblock) of every block kind is at most
--max-ratio (1 by default) times the median connect time of the release build's schnorr block at the
same -par: the Schnorr worst block of a node of the series on this machine. The median of each build's
own schnorr block is reported as well. The gate needs the whole matrix (28 cells) with --rounds samples
each, at least 2, every time finite and positive; anything less, and any run that fails (a node that does
not start or exits, a timeout, an exception), prints INCOMPLETE with the reason and exits with 2; a
failing ratio prints FAIL with the figures and exits with 1.

Usage:
  python3 wholeblock.py --core <patched tree> --bitcoind-release <bitcoind> \\
      --bitcoind-measure <work>/build/bin/bitcoind_m4r_measure --prover <voleith-prove> \\
      --workdir <new directory> [--rounds 2]
"""

import argparse
import copy
import hashlib
import json
import math
import os
import re
import socket
import statistics
import subprocess
import sys
import time
from pathlib import Path


ALL_KINDS = ["rescue", "rescue-invalid-last", "rescue-tail", "rescue-tail-invalid-last", "mixed", "schnorr"]
INVALID_KINDS = {"rescue-invalid-last", "rescue-tail-invalid-last"}
BUILD_KINDS = {
    "release": ALL_KINDS,
    "hook": ["rescue", "rescue-tail", "mixed", "schnorr"],
    "portable": ["rescue", "rescue-tail", "mixed", "schnorr"],
}
ALL_BUILDS = list(BUILD_KINDS)
ALL_PARS = ["1", "default"]
MIN_ROUNDS = 2
REFERENCE = "schnorr"
REFERENCE_BUILD = "release"
# Symbols every bitcoind of the series has, to know that `nm` reads a symbol table at all.
KNOWN_SYMBOLS = ("AppInitMain", "VerifyP2MRRescueProof")
H3 = 50  # the tripwire height, which the rescue lane follows on regtest
HOOK_SYMBOLS = re.compile(r"M4RMeasure|m4r_measure|voleith_m4r")


def parse_args():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--core", required=True, help="the patched Core tree (for test/functional/test_framework)")
    ap.add_argument("--bitcoind-release", required=True, help="a normal bitcoind built from the series")
    ap.add_argument("--bitcoind-measure", required=True, help="bitcoind_m4r_measure of build.sh")
    ap.add_argument("--prover", required=True, help="the test-only voleith-prove of a build with -DBUILD_TESTS=ON")
    ap.add_argument("--workdir", required=True, help="a new directory for node data directories and results")
    ap.add_argument("--rounds", type=int, default=2, help="new nodes per block, build and -par (default 2, the least the gate takes)")
    ap.add_argument("--par", default="1,default", help="-par settings, 'default' for none (default 1,default)")
    ap.add_argument("--builds", default=",".join(ALL_BUILDS), help="builds to time (default release,hook,portable; fewer is INCOMPLETE)")
    ap.add_argument("--max-ratio", type=float, default=1.0,
                    help="the gate's bound on max/reference median (default 1; a lower value only to see the failure branch fire)")
    ap.add_argument("--slh-checks", type=int, default=100, help="SLH-DSA checks of a full input (default 100)")
    ap.add_argument("--mldsa-checks", type=int, default=1000, help="ML-DSA-44 checks of a full input (default 1000)")
    ap.add_argument("--schnorr-checks", type=int, default=2000, help="Schnorr checks of a full input (default 2000)")
    return ap.parse_args()


ARGS = parse_args()
COUNTERS = ("rescue", "rescue_worst", "schnorr", "mldsa44", "slhdsa128s")
# The series' own counters (P2MRRescueProofVerifications, SLHDSA128SVerifyLibraryCalls), which count in
# every build; the others are the measurement build's, on only in its counting runs (M4R_COUNT=1).
SERIES_COUNTERS = ("rescue", "slhdsa128s")


def witness_size(stack):
    return len(ser_compact_size(len(stack))) + sum(len(ser_compact_size(len(e))) + len(e) for e in stack)


def padding_ops(padding):
    ops = []
    while padding > 0:
        size = min(padding, 400)
        ops += [bytes(size), OP_DROP]
        padding -= size
    return ops


def push_len(size):
    return size + (1 if size < 0x4c else 2 if size <= 0xff else 3)


def padding_len(padding):
    full, rest = divmod(padding, 400)
    return full * (push_len(400) + 1) + (push_len(rest) + 1 if rest else 0)


class Kind:
    """One kind of budget-filling input of BIP 342 or post-quantum checks, as in contrib/m4-measure: its
    leaf for a number of checks (prefix + unit x (checks - 1) + suffix, after the padding), its signature
    stack, and the validation weight of one check."""

    def __init__(self, name, cost, sig_stack_sizes, prefix, unit, suffix):
        self.name, self.cost, self.sig_stack_sizes = name, cost, sig_stack_sizes
        self.prefix, self.unit, self.suffix = prefix, unit, suffix
        self.fixed_len = len(CScript(prefix + suffix))
        self.unit_len = len(CScript(unit))
        self.control_block_len = len(p2mr_construct([("m", CScript([OP_TRUE]), LEAF_VERSION_TAPSCRIPT),
                                                     ("fail", LEAF_FAIL, LEAF_VERSION_TAPSCRIPT)]).leaves["m"].control_block)
        self._trees, self._padding, self._base_weight = {}, {}, None

    def leaf_ops(self, checks):
        return self.prefix + self.unit * (checks - 1) + self.suffix

    def script_len(self, padding, checks):
        return padding_len(padding) + self.fixed_len + self.unit_len * (checks - 1)

    def witness_size(self, padding, checks):
        sizes = self.sig_stack_sizes + [self.script_len(padding, checks), self.control_block_len]
        return len(ser_compact_size(len(sizes))) + sum(len(ser_compact_size(n)) + n for n in sizes)

    def padding(self, checks):
        if checks not in self._padding:
            need = checks * self.cost - 50
            if self.witness_size(0, checks) > need + self.cost - 1:
                self._padding[checks] = None
            else:
                lo, hi = 0, max(0, need - self.witness_size(0, checks)) + 8
                while lo < hi:
                    mid = (lo + hi) // 2
                    if self.witness_size(mid, checks) >= need:
                        hi = mid
                    else:
                        lo = mid + 1
                budget = self.witness_size(lo, checks) + 50
                assert checks * self.cost <= budget < (checks + 1) * self.cost, (self.name, checks, budget)
                self._padding[checks] = lo
        return self._padding[checks]

    def tree(self, checks):
        if checks not in self._trees:
            padding = self.padding(checks)
            assert padding is not None, (self.name, checks)
            script = CScript(padding_ops(padding) + self.leaf_ops(checks))
            assert len(script) == self.script_len(padding, checks), (self.name, checks)
            self._trees[checks] = p2mr_construct([("m", script, LEAF_VERSION_TAPSCRIPT), ("fail", LEAF_FAIL, LEAF_VERSION_TAPSCRIPT)])
        return self._trees[checks]

    def min_checks(self):
        checks = 1
        while self.padding(checks) is None:
            checks += 1
        return checks

    def weight(self, checks):
        if self._base_weight is None:
            smallest = self.min_checks()
            self._base_weight = (self.spend(smallest, (COutPoint(0, 0), 10**8), sign=False).get_weight()
                                 - self.witness_size(self.padding(smallest), smallest))
        return self._base_weight + self.witness_size(self.padding(checks), checks)

    def spend(self, checks, utxo, sign=True):
        outpoint, value = utxo
        tree = self.tree(checks)
        leaf = tree.leaves["m"]
        tx = CTransaction()
        tx.vin = [CTxIn(outpoint, b"", 0xffffffff)]
        tx.vout = [CTxOut(value - 1000, CScript([OP_TRUE]))]
        tx.wit.vtxinwit = [CTxInWitness()]
        spent = [CTxOut(value, tree.scriptPubKey)]
        if not sign:
            stack = [bytes(size) for size in self.sig_stack_sizes]
        elif self.name == "slh":
            stack = slhdsa128s_sig_chunks(slhdsa128s_sign_input(SLH_SEC, tx, spent, 0, leaf.script))
        elif self.name == "mldsa":
            stack = mldsa44_sig_chunks(mldsa44_sign_input(MLDSA_SEC, tx, spent, 0, leaf.script))
        else:
            msg = TaprootSignatureHash(tx, spent, SIGHASH_DEFAULT, 0, scriptpath=True, leaf_script=leaf.script,
                                       leaf_ver=LEAF_VERSION_TAPSCRIPT, codeseparator_pos=NO_CODESEPARATOR)
            stack = [sign_schnorr(SCHNORR_SEC, msg)]
        tx.wit.vtxinwit[0].scriptWitness.stack = stack + [bytes(leaf.script), leaf.control_block]
        budget = witness_size(tx.wit.vtxinwit[0].scriptWitness.stack) + 50
        assert checks * self.cost <= budget < (checks + 1) * self.cost, (self.name, checks, budget)
        return tx


class Rescue:
    """A rescue input: a leaf of version 0xe0 at depth one that makes `schnorr` Schnorr checks
    (OP_DUP <key> OP_CHECKSIGVERIFY, on one signature) and then one rescue check (<P> OP_CHECKSIG on the
    token); with schnorr = 0 it is template R1. The witness is the token, the Schnorr signature (if any),
    the leaf, the control block and the rescue record."""

    def __init__(self, schnorr):
        self.schnorr = schnorr
        ops = [OP_DUP, SCHNORR_PUB, OP_CHECKSIGVERIFY] * schnorr + ([OP_DROP] if schnorr else []) + [RESCUE_P, OP_CHECKSIG]
        self.tree = p2mr_construct([("r", CScript(ops), P2MR_LEAF_TRIPWIRED), ("fail", LEAF_FAIL, LEAF_VERSION_TAPSCRIPT)])
        self.leaf = self.tree.leaves["r"]
        self.weight = self.spend((COutPoint(0, 0), 10**8), prover=None).get_weight()

    def stack_sizes(self):
        return [len(RESCUE_TOKEN)] + ([64] if self.schnorr else []) + [len(self.leaf.script), len(self.leaf.control_block),
                                                                        2 + RESCUE_S1_PROOF_SIZE]

    def budget(self):
        sizes = self.stack_sizes()
        return len(ser_compact_size(len(sizes))) + sum(len(ser_compact_size(n)) + n for n in sizes) + 50

    def spend(self, utxo, prover, invalid=False):
        """The spend of utxo; with a prover, a real proof (the most expensive invalid one, chall3 =
        ff..ff, if `invalid`) and a real Schnorr signature over the annex that holds it."""
        outpoint, value = utxo
        tx = CTransaction()
        tx.vin = [CTxIn(outpoint, b"", 0xffffffff)]
        tx.vout = [CTxOut(value - 1000, CScript([OP_TRUE]))]
        tx.wit.vtxinwit = [CTxInWitness()]
        spent = [CTxOut(value, self.tree.scriptPubKey)]
        if prover is None:
            proof = bytes(RESCUE_S1_PROOF_SIZE)
        else:
            sighash = rescue_sighash(tx, 0, spent, self.leaf.script, network_id_regtest(), RESCUE_STATEMENT_HMAC, RESCUE_PUB)
            _, proof = prove(prover, WALLET_V1_SCHNORR_K, 0, RESCUE_X, rescue_message(sighash, network_id_regtest(), RESCUE_P))
            if invalid:
                proof = proof[:-16] + b"\xff" * 16
        annex = rescue_annex(proof)
        sig = []
        if self.schnorr:
            if prover is None:
                sig = [bytes(64)]
            else:
                msg = TaprootSignatureHash(tx, spent, SIGHASH_DEFAULT, 0, scriptpath=True, leaf_script=self.leaf.script,
                                           leaf_ver=P2MR_LEAF_TRIPWIRED, codeseparator_pos=NO_CODESEPARATOR, annex=annex)
                sig = [sign_schnorr(SCHNORR_SEC, msg)]
        tx.wit.vtxinwit[0].scriptWitness.stack = [RESCUE_TOKEN] + sig + [bytes(self.leaf.script), self.leaf.control_block, annex]
        return tx


def rescue_tail_checks():
    """The most Schnorr checks a rescue input's budget pays for next to its rescue check: the budget
    (witness size + 50) less 50 + RESCUE_WEIGHT for the rescue check, in checks of 50; each check adds
    35 bytes to the leaf, so to the budget too."""
    n = 0
    while _tail_budget(n + 1) - 50 - RESCUE_WEIGHT - 50 * (n + 1) >= 0:
        n += 1
    return n


def _tail_budget(n):
    script_len = len(CScript([OP_DUP, SCHNORR_PUB, OP_CHECKSIGVERIFY] * n + [OP_DROP, RESCUE_P, OP_CHECKSIG]))
    sizes = [len(RESCUE_TOKEN), 64, script_len, 33, 2 + RESCUE_S1_PROOF_SIZE]
    return len(ser_compact_size(len(sizes))) + sum(len(ser_compact_size(s)) + s for s in sizes) + 50


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


class RunFailure(Exception):
    """A run that cannot give a figure: the gate ends INCOMPLETE with this reason."""


LIVE_NODES = []


def free_port():
    """A TCP port on 127.0.0.1 that nothing listens on now; the node is then checked to be the one that
    answers on it (Node.__init__)."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class Node:
    def __init__(self, binary, datadir, par, tripwire, worst=None, portable=None, count=False):
        self.datadir = Path(datadir)
        self.datadir.mkdir(parents=True)
        self.port = free_port()
        self.p2p_port = free_port()
        args = [binary, "-regtest", f"-datadir={self.datadir}", "-maxsigcachesize=0", "-debug=bench", "-listen=0",
                "-connect=0", "-dnsseed=0", "-server", "-rpcuser=m", "-rpcpassword=m", f"-rpcport={self.port}",
                f"-port={self.p2p_port}", "-rpcbind=127.0.0.1", "-rpcallowip=127.0.0.1", "-printtoconsole=0",
                f"-signettripwire={H3}@{tripwire}"]
        if par != "default":
            args.append(f"-par={par}")
        env = dict(os.environ)
        for name in ("M4R_WORST", "M4R_PORTABLE", "M4R_COUNT"):
            env.pop(name, None)
        if worst is not None:
            env["M4R_WORST"] = "1" if worst else "0"
            env["M4R_PORTABLE"] = "1" if portable else "0"
            env["M4R_COUNT"] = "1" if count else "0"
        self.measure, self.worst, self.portable, self.count = worst is not None, worst, portable, count
        self.binary = binary
        self.proc = subprocess.Popen(args, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        LIVE_NODES.append(self)
        url = f"http://m:m@127.0.0.1:{self.port}"
        logpath = None
        for _ in range(1200):
            if self.proc.poll() is not None:
                self.kill()
                raise RunFailure(f"{binary} exited ({self.proc.returncode}) while starting: {self.proc.stderr.read().decode()!r}")
            try:
                logpath = AuthServiceProxy(url, timeout=10).getrpcinfo()["logpath"]
                break
            except Exception:
                time.sleep(0.1)
        else:
            self.kill()
            raise RunFailure(f"{binary} did not answer RPC on port {self.port} within 120 s")
        # The node that answers must be this process: its log is in this node's own data directory.
        if Path(logpath).resolve() != (self.datadir / "regtest" / "debug.log").resolve():
            self.kill()
            raise RunFailure(f"the RPC on port {self.port} is not the node just started (log {logpath}, expected under {self.datadir})")
        self.rpc = AuthServiceProxy(url, timeout=3600)

    def alive(self, what):
        if self.proc.poll() is not None:
            self.kill()
            raise RunFailure(f"{self.binary} exited ({self.proc.returncode}) {what}: {self.proc.stderr.read().decode()!r}")

    def kill(self):
        if self.proc.poll() is None:
            self.proc.kill()
            self.proc.wait(timeout=60)
        if self in LIVE_NODES:
            LIVE_NODES.remove(self)

    def stop(self):
        self.alive("before it was stopped")
        self.rpc.stop()
        try:
            stderr = self.proc.communicate(timeout=600)[1].decode()
        except subprocess.TimeoutExpired:
            self.kill()
            raise RunFailure(f"{self.binary} did not stop within 600 s")
        LIVE_NODES.remove(self)
        if self.proc.returncode != 0:
            raise RunFailure(f"{self.binary} stopped with status {self.proc.returncode}: {stderr!r}")
        if not self.measure:
            return None, None
        m = re.search(r"rescue worst-case hook: (\d); portable multiplication forced: (\d); CPU has PCLMULQDQ: (\d); "
                      r"portable multiplication in use: (\d); counting: (\d)", stderr)
        if not m:
            raise RunFailure(f"the measurement node did not report its switches: {stderr!r}")
        worst, forced, cpu_clmul, in_use, count = map(int, m.groups())
        # The portable path is in use when forced, and on a CPU without PCLMULQDQ whatever the switch says.
        if (worst, forced, in_use, count) != (int(self.worst), int(self.portable), int(self.portable or not cpu_clmul),
                                              int(self.count)):
            raise RunFailure(f"the measurement node reported hook {worst}, forced {forced}, CPU PCLMULQDQ {cpu_clmul}, "
                             f"portable in use {in_use}, counting {count}; expected hook {int(self.worst)}, forced "
                             f"{int(self.portable)}, counting {int(self.count)}")
        m = re.search(r"counters: rescue=(\d+) rescue_worst=(\d+) schnorr=(\d+) mldsa44=(\d+) slhdsa128s=(\d+)", stderr)
        if not m:
            raise RunFailure(f"the measurement node did not report its counters: {stderr!r}")
        switches = {"hook": worst, "portable_forced": forced, "cpu_pclmulqdq": cpu_clmul, "portable_in_use": in_use}
        return dict(zip(COUNTERS, map(int, m.groups()))), switches

    def bench_connect_ms(self):
        lines = (self.datadir / "regtest" / "debug.log").read_text().splitlines()
        connect = [float(m.group(1)) for line in lines if (m := re.search(r"- Connect block: ([0-9.]+)ms", line))]
        return connect[-1] if connect else None


def run_block(binary, workdir, chain, block, par, tripwire, expect_valid, worst=None, portable=None, count=False):
    node = Node(binary, workdir, par, tripwire, worst, portable, count)
    for b in chain:
        res = node.rpc.submitblock(b.serialize().hex())
        if res is not None:
            raise RunFailure(f"chain block {b.hash_hex} not accepted: {res}")
    tip = node.rpc.getbestblockhash()
    start = time.perf_counter()
    result = node.rpc.submitblock(block.serialize().hex())
    elapsed_ms = (time.perf_counter() - start) * 1000
    node.alive("while connecting the block")
    best = node.rpc.getbestblockhash()
    bench = node.bench_connect_ms() if expect_valid else None
    counters, switches = node.stop()
    if expect_valid and not (result is None and best == block.hash_hex):
        raise RunFailure(f"block not connected: {result}")
    if not expect_valid and not (result is not None and "Invalid rescue proof" in result and best == tip):
        raise RunFailure(f"invalid block not rejected as expected: {result}")
    return elapsed_ms, bench, counters, switches


def expected_cells(builds, pars):
    return [(b, p, k) for b in builds for p in pars for k in BUILD_KINDS[b]]


def check_binaries():
    """The release node must hold no symbol of the measurement build, the measurement node must. A
    stripped binary would pass the first check by having no symbols at all, so each must first show the
    symbols every node of the series has."""
    for binary, want in ((ARGS.bitcoind_release, False), (ARGS.bitcoind_measure, True)):
        res = subprocess.run(["nm", "-C", binary], capture_output=True, text=True)
        missing = [sym for sym in KNOWN_SYMBOLS if sym not in res.stdout]
        if res.returncode != 0 or missing:
            raise RunFailure(f"{binary}: nm finds no usable symbol table (exit {res.returncode}, missing {missing}); "
                             "the measurement symbols cannot be checked")
        found = sorted({m.group(0) for m in HOOK_SYMBOLS.finditer(res.stdout)})
        if bool(found) != want:
            raise RunFailure(f"{binary}: measurement symbols {found or 'none'}; expected {'some' if want else 'none'}")
        print(f"{binary}: symbol table read ({', '.join(KNOWN_SYMBOLS)} present); measurement symbols: "
              f"{', '.join(found) if found else 'none'}", flush=True)


def good(x):
    return isinstance(x, (int, float)) and math.isfinite(x) and x > 0


def summarize(runs, info, builds, pars, missing, rounds, max_ratio):
    """The gate's verdict from the timed runs: every cell's largest connect time over the median of the
    release build's schnorr block at the same -par. Returns (verdict, summary, failures, incomplete)."""
    def median_of(build, par, name):
        """The median of a cell's connect times, if every sample and the median itself are finite and
        positive (two finite samples near the largest float give an infinite median)."""
        times = [r["connect_ms"] for r in runs if r["build"] == build and r["par"] == par and r["block"] == name]
        if not times or not all(good(t) for t in times):
            return None
        median = statistics.median(times)
        return median if good(median) else None

    summary = []
    print("\nbuild | -par | block | weight | inputs | checks rescue/Schnorr/ML-DSA-44/SLH-DSA planned | counted (hook off) | "
          "connect ms median | max | bench median | release schnorr median | max/release schnorr (gate) | "
          "own schnorr median | max/own schnorr | gate")
    gate_ok, complete = True, not missing and rounds >= MIN_ROUNDS
    incomplete = []
    failures = []
    for build in builds:
        for par in pars:
            ref_median = median_of(REFERENCE_BUILD, par, REFERENCE)
            own_median = median_of(build, par, REFERENCE)
            if ref_median is None:
                complete = False
                incomplete.append(f"no finite, positive median of the {REFERENCE_BUILD} {REFERENCE} block at -par={par}")
            for name in BUILD_KINDS[build]:
                rows = [r for r in runs if r["build"] == build and r["par"] == par and r["block"] == name]
                if len(rows) != rounds:
                    complete = False
                if not rows:
                    continue
                times = [r["connect_ms"] for r in rows]
                if not all(good(t) for t in times):
                    complete = False
                    incomplete.append(f"{build}/-par={par}/{name}: connect times {times} are not all finite and positive")
                    continue
                benches = [r["bench_connect_ms"] for r in rows if r["bench_connect_ms"] is not None]
                ratio = max(times) / ref_median if ref_median else None
                own_ratio = max(times) / own_median if own_median else None
                if (not good(statistics.median(times)) or (ref_median and not good(ratio))
                        or (own_median and not good(own_ratio))):
                    complete = False
                    incomplete.append(f"{build}/-par={par}/{name}: median or ratio not finite and positive "
                                      f"(median {statistics.median(times)}, ratio {ratio}, own ratio {own_ratio})")
                    ratio = None
                # The schnorr blocks are the reference, not a kind the gate bounds; the hook and portable
                # builds' own schnorr blocks are reported only.
                passed = None if name == REFERENCE or ratio is None else ratio <= max_ratio
                p, c = info[name]["planned"], info[name]["counted"]
                entry = {"build": build, "par": par, "block": name, "weight": info[name]["weight"], "inputs": info[name]["inputs"],
                         "planned": p, "counted": c, "counted_hook": info[name].get("counted_hook"),
                         "connect_ms_median": statistics.median(times), "connect_ms_max": max(times),
                         "bench_connect_ms_median": statistics.median(benches) if benches else None,
                         "reference_median_ms": ref_median, "ratio_max_to_reference_median": ratio,
                         "own_schnorr_median_ms": own_median, "ratio_max_to_own_schnorr_median": own_ratio, "gate_pass": passed}
                summary.append(entry)
                fmt = lambda x, f="{:.1f}": "-" if x is None else f.format(x)  # noqa: E731
                print(f"{build} | {par} | {name} | {entry['weight']} | {entry['inputs']} | "
                      f"{p['rescue']}/{p['schnorr']}/{p['mldsa44']}/{p['slhdsa128s']} | "
                      f"{c['rescue']}/{c['schnorr']}/{c['mldsa44']}/{c['slhdsa128s']} | {entry['connect_ms_median']:.1f} | "
                      f"{entry['connect_ms_max']:.1f} | {fmt(entry['bench_connect_ms_median'])} | {fmt(ref_median)} | "
                      f"{fmt(ratio, '{:.3f}')} | {fmt(own_median)} | {fmt(own_ratio, '{:.3f}')} | "
                      f"{'-' if passed is None else ('pass' if passed else 'FAIL')}")
                if passed is False:
                    gate_ok = False
                    failures.append(f"{build}/-par={par}/{name}: max {max(times):.1f} ms, {REFERENCE_BUILD} schnorr median "
                                    f"{ref_median:.1f} ms, ratio {ratio:.3f} > {max_ratio}")
    # Completeness first: a run without a valid measurement of every cell is INCOMPLETE, whatever its
    # ratios; its FAIL lines are still printed.
    verdict = "INCOMPLETE" if not complete else ("PASS" if gate_ok else "FAIL")
    return verdict, summary, failures, incomplete


def setup():
    """Everything that reads the Core tree: its test framework, the series' weight from
    src/script/script.h, the keys and the input kinds. It runs inside main()'s handler, so that a wrong
    --core ends INCOMPLETE with the reason too."""
    sys.path.insert(0, str(Path(ARGS.core) / "test" / "functional"))

    from test_framework.authproxy import AuthServiceProxy
    from test_framework.blocktools import add_witness_commitment, create_block, create_coinbase
    from test_framework.key import compute_xonly_pubkey, sign_schnorr
    from test_framework.messages import (
        COutPoint,
        CTransaction,
        CTxIn,
        CTxInWitness,
        CTxOut,
        MAX_BLOCK_WEIGHT,
        ser_compact_size,
    )
    from test_framework.p2mr import p2mr_construct
    from test_framework.p2mr_mldsa44 import (
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
    from test_framework.p2mr_rescue import (
        P2MR_LEAF_TRIPWIRED,
        RESCUE_S1_PROOF_SIZE,
        RESCUE_STATEMENT_HMAC,
        WALLET_V1_SCHNORR_K,
        network_id_regtest,
        prove,
        rescue_annex,
        rescue_key,
        rescue_message,
        rescue_pub,
        rescue_sighash,
        rescue_token,
        rescue_y,
    )
    from test_framework.p2mr_slhdsa128s import (
        OP_CHECKSLHDSA128S,
        SLHDSA128S_SIGNATURE_SIZE,
        VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK,
        slhdsa128s_keygen,
        slhdsa128s_sig_chunks,
        slhdsa128s_sign_input,
    )
    from test_framework.script import (
        CScript,
        LEAF_VERSION_TAPSCRIPT,
        OP_7,
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

    COINBASE_MATURITY = 100
    VALIDATION_WEIGHT_PER_MLDSA44_CHECK = 250
    # The validation weight of a rescue check (src/script/script.h), on top of the 50 every non-empty
    # signature pays; read from the tree, so that the gate measures the value the series has.
    RESCUE_WEIGHT = int(re.search(r"VALIDATION_WEIGHT_PER_P2MR_RESCUE_CHECK\{(\d+)\}",
                                  (Path(ARGS.core) / "src" / "script" / "script.h").read_text()).group(1))
    SLH_PUB, SLH_SEC = slhdsa128s_keygen(hashlib.sha384(b"m4r-measure slh-dsa key").digest())
    MLDSA_PUB, MLDSA_SEC = mldsa44_keygen(hashlib.sha256(b"m4r-measure ml-dsa-44 key").digest())
    MLDSA_PUB_CHUNKS = split_chunks(MLDSA_PUB, MLDSA44_PUBKEY_CHUNK_SIZES)
    SCHNORR_SEC = hashlib.sha256(b"m4r-measure schnorr key").digest()
    SCHNORR_PUB = compute_xonly_pubkey(SCHNORR_SEC)[0]
    RESCUE_X = hashlib.sha256(b"m4r-measure rescue seed").digest()
    RESCUE_YV = rescue_y(WALLET_V1_SCHNORR_K, RESCUE_X, 0)
    RESCUE_P = rescue_key(RESCUE_YV)
    RESCUE_TOKEN = rescue_token(WALLET_V1_SCHNORR_K, 0, RESCUE_YV)
    RESCUE_PUB = rescue_pub(WALLET_V1_SCHNORR_K, 0, RESCUE_YV)
    # The input kinds below look these names up as globals.
    globals().update({name: value for name, value in locals().items() if not name.startswith("_")})
    SLH_CHECK = [OP_15, OP_PICK] * 16 + [SLH_PUB, OP_CHECKSLHDSA128S, OP_VERIFY]
    KINDS = {
        "slh": Kind("slh", VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK, [520] * 15 + [56], [], SLH_CHECK, [SLH_PUB, OP_CHECKSLHDSA128S]),
        "mldsa": Kind("mldsa", VALIDATION_WEIGHT_PER_MLDSA44_CHECK, [520] * 4 + [340], list(MLDSA_PUB_CHUNKS),
                      [OP_7, OP_PICK] * 8 + [OP_CHECKMLDSA44, OP_VERIFY], [OP_CHECKMLDSA44]),
        "schnorr": Kind("schnorr", 50, [64], [], [OP_DUP, SCHNORR_PUB, OP_CHECKSIGVERIFY], [SCHNORR_PUB, OP_CHECKSIG]),
    }
    assert sum(KINDS["slh"].sig_stack_sizes) == SLHDSA128S_SIGNATURE_SIZE
    assert sum(KINDS["mldsa"].sig_stack_sizes) == MLDSA44_SIGNATURE_SIZE
    globals().update({name: value for name, value in locals().items() if not name.startswith("_")})


def gate():
    pars = ARGS.par.split(",")
    builds = ARGS.builds.split(",")
    unknown = [b for b in builds if b not in ALL_BUILDS] + [p for p in pars if p not in ALL_PARS]
    if unknown or ARGS.rounds < 1:
        print(f"usage: unknown builds or -par settings {unknown}, or --rounds {ARGS.rounds} below 1", file=sys.stderr)
        return 2
    missing = sorted(set(expected_cells(ALL_BUILDS, ALL_PARS)) - set(expected_cells(builds, pars)))
    if missing:
        print(f"note: this run leaves out {len(missing)} of the gate's {len(expected_cells(ALL_BUILDS, ALL_PARS))} cells, "
              "so its result will be INCOMPLETE", flush=True)
    if ARGS.rounds < MIN_ROUNDS:
        print(f"note: --rounds {ARGS.rounds} is a trial run; the gate needs at least {MIN_ROUNDS}", flush=True)
    check_binaries()
    work = Path(ARGS.workdir)
    work.mkdir(parents=False, exist_ok=False)
    full = {"slh": ARGS.slh_checks, "mldsa": ARGS.mldsa_checks, "schnorr": ARGS.schnorr_checks}

    # What a block with only its coinbase leaves of the maximum weight.
    empty = make_block("01" * 32, COINBASE_MATURITY + 3, 1, [])
    available = MAX_BLOCK_WEIGHT - empty.get_weight() - 4  # room for a larger transaction count
    r1 = Rescue(0)
    tail = Rescue(rescue_tail_checks())
    assert tail.budget() - 50 - RESCUE_WEIGHT - 50 * tail.schnorr in range(0, 50), tail.budget()
    print(f"rescue weight {RESCUE_WEIGHT}; R1 input {r1.weight} WU, budget {r1.budget()} (unused "
          f"{r1.budget() - 50 - RESCUE_WEIGHT}); tail input {tail.weight} WU with {tail.schnorr} Schnorr checks, unused budget "
          f"{tail.budget() - 50 - RESCUE_WEIGHT - 50 * tail.schnorr}", flush=True)

    # Plans: lists of (input kind, checks); "r1" and "tail" are rescue inputs.
    plans = {}
    plans["rescue"] = [("r1", 0)] * (available // r1.weight)
    n_tail = available // tail.weight
    plans["rescue-tail"] = [("schnorr", c) for c in plan(KINDS["schnorr"], available - n_tail * tail.weight, full["schnorr"])] \
        + [("tail", tail.schnorr)] * n_tail
    quarter = available // 4
    p_slh = plan(KINDS["slh"], quarter, full["slh"])
    used = sum(KINDS["slh"].weight(c) for c in p_slh)
    p_ml = plan(KINDS["mldsa"], 2 * quarter - used, full["mldsa"])
    used += sum(KINDS["mldsa"].weight(c) for c in p_ml)
    n_r1 = (3 * quarter - used) // r1.weight
    used += n_r1 * r1.weight
    p_sch = plan(KINDS["schnorr"], available - used, full["schnorr"])
    plans["mixed"] = [("slh", c) for c in p_slh] + [("mldsa", c) for c in p_ml] + [("r1", 0)] * n_r1 + [("schnorr", c) for c in p_sch]
    plans["schnorr"] = [("schnorr", c) for c in plan(KINDS["schnorr"], available, full["schnorr"])]

    # The chain: blocks paying to OP_TRUE; the coinbase of block 3, never spent, is the tripwire output,
    # so that elliptic curve checks work in leaves of version 0xe0 (the rescue-tail inputs need them) and
    # the rescue lane is open from H3; then one block funding an output for every planned input.
    print("building the chain, signing and proving", flush=True)
    t0 = 1296688602 + 1
    prev = "0f9188f13cb7b2c71f2a335e3a4fc328bf5beb436012afca590b1a11466e2206"  # regtest genesis
    chain = []
    for height in range(1, COINBASE_MATURITY + 3):
        block = make_block(prev, height, t0 + height, [])
        chain.append(block)
        prev = block.hash_hex
    tripwire = f"{chain[2].vtx[0].txid_hex}:0"
    fund = CTransaction()
    fund.vin = [CTxIn(COutPoint(chain[i].vtx[0].txid_int, 0), b"", 0xffffffff) for i in range(2)]
    entries = [(name, kind, checks) for name in plans for kind, checks in plans[name]]
    value = (2 * chain[0].vtx[0].vout[0].nValue - 100_000) // len(entries)

    def script_pubkey(kind, checks):
        if kind == "r1":
            return r1.tree.scriptPubKey
        if kind == "tail":
            return tail.tree.scriptPubKey
        return KINDS[kind].tree(checks).scriptPubKey
    fund.vout = [CTxOut(value, script_pubkey(kind, checks)) for _, kind, checks in entries]
    height = COINBASE_MATURITY + 3
    chain.append(make_block(prev, height, t0 + height, [fund]))
    prev = chain[-1].hash_hex
    height += 1
    assert height > H3
    outputs = {name: [] for name in plans}
    for i, (name, kind, checks) in enumerate(entries):
        outputs[name].append((COutPoint(fund.txid_int, i), value))

    def spend(kind, checks, utxo, invalid=False):
        if kind == "r1":
            return r1.spend(utxo, ARGS.prover, invalid)
        if kind == "tail":
            return tail.spend(utxo, ARGS.prover, invalid)
        return KINDS[kind].spend(checks, utxo)

    blocks = {}
    for name in list(plans):
        txs = [spend(kind, checks, outputs[name][i]) for i, (kind, checks) in enumerate(plans[name])]
        blocks[name] = make_block(prev, height, t0 + height, txs)
        if name in ("rescue", "rescue-tail"):
            # The same block with its last input's rescue check failing R7 on the most expensive invalid
            # proof; in rescue-tail, with the Schnorr signature of that input over the changed annex, so
            # that every other check stays valid.
            kind, checks = plans[name][-1]
            bad = spend(kind, checks, outputs[name][-1], invalid=True)
            blocks[f"{name}-invalid-last"] = make_block(prev, height, t0 + height, txs[:-1] + [bad])
            plans[f"{name}-invalid-last"] = plans[name]

    info = {}
    for name in ALL_KINDS:
        block = blocks[name]
        p = plans[name]
        planned = {"rescue": sum(1 for k, _ in p if k in ("r1", "tail")),
                   "schnorr": sum(c for k, c in p if k == "schnorr") + sum(c for k, c in p if k == "tail"),
                   "mldsa44": sum(c for k, c in p if k == "mldsa"), "slhdsa128s": sum(c for k, c in p if k == "slh")}
        assert block.get_weight() <= MAX_BLOCK_WEIGHT, name
        info[name] = {"weight": block.get_weight(), "unused_weight": MAX_BLOCK_WEIGHT - block.get_weight(),
                      "inputs": len(block.vtx) - 1, "planned": planned}
        print(f"block {name}: weight {block.get_weight()} ({MAX_BLOCK_WEIGHT - block.get_weight()} WU unused), "
              f"{len(block.vtx) - 1} inputs, checks planned {planned}", flush=True)

    def check_counts(name, counters, hook, exact, counting=True):
        """The counters of a run against the plan: exact at -par=1 (one thread, in order: an invalid
        block's failing check is its last, so every check runs) and for valid blocks; at most the plan for
        an invalid block checked in parallel, which stops at the first failure. A timed run counts nothing
        (counting=False) but the series' own counts, of rescue proofs and of SLH-DSA library calls, which run
        in every build."""
        expected = dict(info[name]["planned"])
        expected["rescue_worst"] = expected["rescue"] if hook else 0
        if not counting:
            expected = {key: (expected[key] if key in SERIES_COUNTERS else 0) for key in COUNTERS}
        for key in COUNTERS:
            ok = counters[key] == expected[key] if exact or expected[key] == 0 else counters[key] <= expected[key]
            if not ok:
                raise RunFailure(f"{name}: counted {counters}, planned {expected} ({'exact' if exact else 'at most'})")

    runs = []
    switches_seen = []
    for name in ALL_KINDS:
        valid = name not in INVALID_KINDS
        for hook in (False, True):
            if hook and name not in BUILD_KINDS["hook"]:
                continue
            _, _, counters, switches = run_block(ARGS.bitcoind_measure, work / f"count-{name}-hook{int(hook)}", chain,
                                                 blocks[name], "1", tripwire, valid, worst=hook, portable=False, count=True)
            check_counts(name, counters, hook, exact=True)
            switches_seen.append(switches)
            info[name]["counted_hook" if hook else "counted"] = counters
            print(f"counted, hook {'on' if hook else 'off'}, {name}: {counters}", flush=True)
    for build, par, name in expected_cells(builds, pars):
        valid = name not in INVALID_KINDS
        for rnd in range(ARGS.rounds):
            if build == "release":
                ms, bench, counters, switches = run_block(ARGS.bitcoind_release, work / f"{build}-{name}-par{par}-r{rnd + 1}",
                                                          chain, blocks[name], par, tripwire, valid)
            else:
                ms, bench, counters, switches = run_block(ARGS.bitcoind_measure, work / f"{build}-{name}-par{par}-r{rnd + 1}",
                                                          chain, blocks[name], par, tripwire, valid, worst=True,
                                                          portable=build == "portable")
                check_counts(name, counters, True, exact=valid or par == "1", counting=False)
                switches_seen.append(switches)
            row = {"build": build, "block": name, "par": par, "round": rnd + 1, "connect_ms": ms, "bench_connect_ms": bench,
                   "counters": counters, "switches": switches}
            runs.append(row)
            print(json.dumps(row), flush=True)
    cpu_clmul = sorted({sw["cpu_pclmulqdq"] for sw in switches_seen})

    verdict, summary, failures, incomplete = summarize(runs, info, builds, pars, missing, ARGS.rounds, ARGS.max_ratio)
    (work / "results.json").write_text(json.dumps({"rescue_weight": RESCUE_WEIGHT, "max_ratio": ARGS.max_ratio, "blocks": info,
                                                   "cpu_pclmulqdq": cpu_clmul, "runs": runs, "summary": summary,
                                                   "rounds": ARGS.rounds, "missing_cells": missing, "incomplete": incomplete,
                                                   "gate": verdict}, indent=1))
    print(f"\nCPU has PCLMULQDQ: {cpu_clmul} (with none, the hook build is portable already)")
    print(f"gate (every block kind's maximum <= {ARGS.max_ratio} x the median of the {REFERENCE_BUILD} build's schnorr block "
          f"at the same -par): {verdict}")
    for line in failures:
        print("FAIL: " + line)
    for line in incomplete:
        print("INCOMPLETE: " + line)
    if missing:
        print("INCOMPLETE: not measured: " + ", ".join(f"{b}/-par={p}/{k}" for b, p, k in missing))
    if ARGS.rounds < MIN_ROUNDS:
        print(f"INCOMPLETE: --rounds {ARGS.rounds} is a trial run; the gate needs at least {MIN_ROUNDS}")
    return {"PASS": 0, "FAIL": 1, "INCOMPLETE": 2}[verdict]


def main():
    """The gate; every run that cannot finish (a node that does not start or exits, a timeout, a count
    that differs, an exception) stops the nodes still running and ends INCOMPLETE (2) with the reason."""
    try:
        setup()
        return gate()
    except BaseException as e:  # noqa: B036 (KeyboardInterrupt too: stop the nodes first)
        reason = f"{type(e).__name__}: {e}"
        for node in list(LIVE_NODES):
            try:
                node.kill()
            except Exception as kill_error:
                print(f"note: could not stop {node.binary} ({kill_error})", file=sys.stderr)
        print(f"\ngate: INCOMPLETE\nINCOMPLETE: the run failed: {reason}", flush=True)
        if not isinstance(e, (RunFailure, Exception)):
            raise
        return 2


if __name__ == "__main__":
    sys.exit(main())

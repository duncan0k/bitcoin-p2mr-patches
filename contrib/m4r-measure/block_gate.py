#!/usr/bin/env python3
# Copyright (c) 2026 duncan0k
# Distributed under the MIT software license, see the accompanying
# file COPYING or http://www.opensource.org/licenses/mit-license.php.
"""A reduced whole-block gate of the M4-R validation weight (design section 2.5), measurement only.

Run it as a functional test of the Core tree it measures:

    CORE=<Core source dir> python3 contrib/m4r-measure/block_gate.py --configfile=<Core build>/test/config.ini [--rounds=N]

It times submitblock, on a node started fresh for each block with -maxsigcachesize=0, the block not
seen before in the mempool (cold), at -par=1 and at the default, of three blocks that build on one
tip and fill it:

- rescue: rescue spends in template R1, one input each, as many as fit under the block weight limit
  next to the coinbase (counted by the weight of the block built, not estimated), all valid (kind 1
  of the design);
- rescue-last-fails: the same with the last input's proof failing R7 (kind 3, release build only:
  the design's upper bound needs a hook that defers the failure, which this kit does not have);
- schnorr: the worst block of BIP 342 checks, OP_2DUP OP_CHECKSIGVERIFY repeated in P2MR leaves of
  version 0xc0 so that every 50 units of budget buy one full Schnorr verification.

The condition is that the rescue blocks take no longer than the Schnorr block: a ratio above 1 at
either setting fails the run, with the figures. Not covered (see README.md): the mixed block of kind
2, and kind 3 with the failure deferred to the slowest place.
"""

import hashlib
import os
import sys
import time

# The test framework of the Core tree being measured: CORE=<Core source dir>.
sys.path.insert(0, os.path.join(os.environ["CORE"], "test", "functional"))

from test_framework.blocktools import add_witness_commitment, create_block, create_coinbase  # noqa: E402
from test_framework.key import compute_xonly_pubkey, sign_schnorr  # noqa: E402
from test_framework.messages import COIN, MAX_BLOCK_WEIGHT, COutPoint, CTransaction, CTxIn, CTxInWitness, CTxOut  # noqa: E402
from test_framework.p2mr import p2mr_construct  # noqa: E402
from test_framework.p2mr_rescue import (  # noqa: E402
    P2MR_LEAF_TRIPWIRED, RESCUE_STATEMENT_HMAC, WALLET_V1_SCHNORR_K, find_prover, network_id_regtest, prove,
    rescue_annex, rescue_key, rescue_message, rescue_pub, rescue_sighash, rescue_token, rescue_y,
)
from test_framework.script import (  # noqa: E402
    CScript, LEAF_VERSION_TAPSCRIPT, OP_2DROP, OP_2DUP, OP_CHECKSIG, OP_CHECKSIGVERIFY, OP_RETURN, SIGHASH_DEFAULT,
    TaprootSignatureMsg,
)
from test_framework.key import TaggedHash  # noqa: E402
from test_framework.test_framework import BitcoinTestFramework  # noqa: E402
from test_framework.util import assert_equal  # noqa: E402
from test_framework.wallet import MiniWallet  # noqa: E402

H3 = 150
RESCUE_OUTPUTS = 34         # funded; the block takes as many as fit (30 of about 130.5 kWU)
SCHNORR_PADS = 990          # 520-byte elements per Schnorr input
SCHNORR_INPUTS = 7
AMOUNT = 1_000_000
ARGS = [f"-signettripwire={H3}@{'22' * 32}:0", "-maxsigcachesize=0"]


class BlockGate(BitcoinTestFramework):
    def add_options(self, parser):
        parser.add_argument("--rounds", type=int, default=2)

    def set_test_params(self):
        self.num_nodes = 2
        self.setup_clean_chain = False
        self.rpc_timeout = 600
        self.extra_args = [ARGS, ARGS]

    def setup_network(self):
        self.setup_nodes()

    def run_test(self):
        builder = self.nodes[0]
        self.prover = find_prover(self.config)
        wallet = MiniWallet(builder)
        wallet.rescan_utxos()
        self.generate(builder, H3 + 5 - builder.getblockcount(), sync_fun=self.no_op)

        # Rescue outputs of one key, and Schnorr outputs of the worst leaf.
        x = hashlib.sha256(b"m4r block gate").digest()
        y = rescue_y(WALLET_V1_SCHNORR_K, x, 0)
        p_rescue = rescue_key(y)
        rescue_tree = p2mr_construct([("r", CScript([p_rescue, OP_CHECKSIG]), P2MR_LEAF_TRIPWIRED), ("f", CScript([OP_RETURN]))])
        sk = hashlib.sha256(b"m4r block gate schnorr").digest()
        p_schnorr = compute_xonly_pubkey(sk)[0]
        # Per input: the budget is the witness size + 50; the pads make it, and each check costs 50.
        witness_estimate = 65 + SCHNORR_PADS * 523 + SCHNORR_PADS // 2 + 34 + 34
        checks = (witness_estimate + 50) // 49
        leaf = CScript([OP_2DROP] * (SCHNORR_PADS // 2) + [p_schnorr] + [OP_2DUP, OP_CHECKSIGVERIFY] * (checks - 1) + [OP_CHECKSIG])
        schnorr_tree = p2mr_construct([("s", leaf, LEAF_VERSION_TAPSCRIPT), ("f", CScript([OP_RETURN]))])
        self.log.info(f"Schnorr leaf: {checks} checks per input, {SCHNORR_INPUTS} inputs")

        utxo = wallet.get_utxo(confirmed_only=True)
        fund = CTransaction()
        fund.vin = [CTxIn(COutPoint(int(utxo["txid"], 16), utxo["vout"]))]
        fund.vout = [CTxOut(AMOUNT, rescue_tree.scriptPubKey) for _ in range(RESCUE_OUTPUTS)]
        fund.vout += [CTxOut(AMOUNT, schnorr_tree.scriptPubKey) for _ in range(SCHNORR_INPUTS)]
        fund.vout.append(CTxOut(int(utxo["value"] * COIN) - (RESCUE_OUTPUTS + SCHNORR_INPUTS) * AMOUNT - 100_000, wallet.get_output_script()))
        wallet.sign_tx(fund)
        builder.sendrawtransaction(fund.serialize().hex())
        self.generate(builder, 1, sync_fun=self.no_op)

        def spend(n, tree, stack_fn):
            tx = CTransaction()
            tx.vin = [CTxIn(COutPoint(fund.txid_int, n))]
            tx.vout = [CTxOut(0, CScript([OP_RETURN]))]
            spent = [CTxOut(AMOUNT, tree.scriptPubKey)]
            tx.wit.vtxinwit = [CTxInWitness()]
            tx.wit.vtxinwit[0].scriptWitness.stack = stack_fn(tx, spent)
            return tx

        def rescue_stack(tx, spent):
            lf = rescue_tree.leaves["r"]
            pub = rescue_pub(WALLET_V1_SCHNORR_K, 0, y)
            sh = rescue_sighash(tx, 0, spent, lf.script, network_id_regtest(), RESCUE_STATEMENT_HMAC, pub)
            _, proof = prove(self.prover, WALLET_V1_SCHNORR_K, 0, x, rescue_message(sh, network_id_regtest(), p_rescue))
            return [rescue_token(WALLET_V1_SCHNORR_K, 0, y), bytes(lf.script), lf.control_block, rescue_annex(proof)]

        def schnorr_stack(tx, spent):
            lf = schnorr_tree.leaves["s"]
            msg = TaprootSignatureMsg(tx, spent, SIGHASH_DEFAULT, 0, scriptpath=True, leaf_script=lf.script, codeseparator_pos=0xffffffff, leaf_ver=LEAF_VERSION_TAPSCRIPT)
            sig = sign_schnorr(sk, TaggedHash("TapSighash", msg))
            return [sig] + [b"\x5a" * 520] * SCHNORR_PADS + [bytes(lf.script), lf.control_block]

        tip = builder.getbestblockhash()
        height = builder.getblockcount()
        mtp = builder.getblockheader(tip)["mediantime"]

        def block(txs, nudge):
            b = create_block(int(tip, 16), create_coinbase(height + 1), mtp + 1 + nudge, txlist=txs)
            add_witness_commitment(b)
            b.solve()
            return b

        # As many rescue spends as the block weight limit takes, by the weight of the block itself.
        rescue_txs = []
        for n in range(RESCUE_OUTPUTS):
            tx = spend(n, rescue_tree, rescue_stack)
            if block(rescue_txs + [tx], 0).get_weight() > MAX_BLOCK_WEIGHT:
                break
            rescue_txs.append(tx)
        else:
            raise AssertionError(f"{RESCUE_OUTPUTS} funded rescue outputs do not fill a block")
        self.log.info(f"rescue: {len(rescue_txs)} inputs fit, one more would weigh {block(rescue_txs + [tx], 0).get_weight()}")
        # The same transactions but for a copy of the last, whose proof has one bit flipped.
        bad = CTransaction(rescue_txs[-1])
        bad.wit.vtxinwit = [CTxInWitness()]
        bad.wit.vtxinwit[0].scriptWitness.stack = list(rescue_txs[-1].wit.vtxinwit[0].scriptWitness.stack)
        failing = rescue_txs[:-1] + [bad]
        wit = bad.wit.vtxinwit[0].scriptWitness.stack
        wit[-1] = wit[-1][:1000] + bytes([wit[-1][1000] ^ 1]) + wit[-1][1001:]
        schnorr_txs = [spend(RESCUE_OUTPUTS + n, schnorr_tree, schnorr_stack) for n in range(SCHNORR_INPUTS)]

        def blocks_of_round(rnd):
            return {"rescue": block(rescue_txs, 3 * rnd), "rescue-last-fails": block(failing, 3 * rnd + 1), "schnorr": block(schnorr_txs, 3 * rnd + 2)}
        for name, b in blocks_of_round(0).items():
            assert b.get_weight() <= MAX_BLOCK_WEIGHT
            self.log.info(f"{name}: weight {b.get_weight()}, {len(b.vtx) - 1} transactions")
        chain = [builder.getblock(builder.getblockhash(h), 0) for h in range(1, height + 1)]

        results = {}
        for rnd in range(self.options.rounds):
            for par in ("1", "0"):
                # Blocks no node has seen: another time stamp each round and setting.
                blocks = blocks_of_round(2 * rnd + (par == "0"))
                for name, b in blocks.items():
                    # A restarted node for each block, without a signature cache, and a block it has never seen.
                    self.restart_node(1, extra_args=ARGS + [f"-par={par}"])
                    node = self.nodes[1]
                    if node.getblockcount() < height:
                        for raw in chain[node.getblockcount():]:
                            node.submitblock(raw)
                    assert_equal(node.getbestblockhash(), tip)
                    t0 = time.perf_counter()
                    res = node.submitblock(b.serialize().hex())
                    dt = time.perf_counter() - t0
                    if name == "rescue-last-fails":
                        assert res is not None and "Invalid rescue proof" in res, res
                    else:
                        assert res is None, res
                        node.invalidateblock(b.hash_hex)
                    results.setdefault((par, name), []).append(dt)
                    self.log.info(f"round {rnd + 1} -par={par} {name}: {dt * 1000:.0f} ms ({res})")
        failures = []
        for par in ("1", "0"):
            s = max(results[(par, "schnorr")])
            for name in ("rescue", "rescue-last-fails"):
                r = max(results[(par, name)])
                line = f"-par={par}: {name} max {r * 1000:.0f} ms, schnorr max {s * 1000:.0f} ms, ratio {r / s:.3f}"
                self.log.info(line)
                if r > s:
                    failures.append(line)
        # A rescue block slower than the Schnorr worst case fails the gate: the run exits non-zero.
        assert not failures, "the rescue block is slower than the Schnorr worst block: " + "; ".join(failures)


if __name__ == "__main__":
    BlockGate(__file__).main()

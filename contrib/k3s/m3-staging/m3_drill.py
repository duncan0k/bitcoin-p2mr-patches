#!/usr/bin/env python3
"""Steps and measurements on the M3 staging network (README.md in this directory).

Runs inside the network's producer pod, which has the RPC credentials, node A's
wallet for the signet solution, and ark0.py:

    kubectl -n ark0-m3 exec -i deploy/m3-miner -- env PYTHONPATH=/opt/ark0/demo:/opt/ark0/bitcoin/test/functional \\
        python3 - <step> [options] < m3_drill.py

Steps, in the order README.md runs them:

  tripwires  the two tripwire outputs, NUMS and drill, and the arguments of m3_arm.sh
  fund       outputs of template S3: a Schnorr leaf and an ML-DSA-44 leaf, both of
             leaf version 0xe0 at depth one
  paths      spend S3 outputs through each of their leaves
  negative   blocks with an invalid signature in a 0xe0 leaf: both M3 nodes refuse
             them, the M2 node takes them
  emptypool  on node B, cut off from its peers, before the trigger: a reorganization
             whose first block repeats the transactions of the block it replaces,
             coinbase included, and whose second spends the drill tripwire
  trigger    spend the drill tripwire on the network, with spends of S3 outputs waiting
             in the mempools
  reorg      on node B: invalidateblock and reconsiderblock across the trigger
  recheck    on node B, cut off from its peers: time a reorganization across the
             trigger that ends with the checks disabled, with a mempool as it is and
             with a full one
  rejoin     put node B back on the network, with what its mempool took while cut off
  status     the three nodes at a glance
  times      height, time, weight and transaction count of each block

Each step checks what it is there to show and stops at the first difference, and
prints its results as JSON. Every key is derived from a public string: this is a
test network and nothing else. Coins come from the producer's coinbases, which pay
P2WSH(OP_TRUE).
"""
import argparse
import copy
import hashlib
import json
import os
import struct
import subprocess
import sys
import time

import ark0  # noqa: E402  (the producer; loads contrib/signet/miner)
from test_framework.key import compute_xonly_pubkey  # noqa: E402
from test_framework.messages import (  # noqa: E402
    COutPoint, CBlockHeader, CTransaction, CTxIn, CTxInWitness, CTxOut,
)
from test_framework.p2mr_mldsa44 import mldsa44_keygen  # noqa: E402
from test_framework.p2mr_tripwire import (  # noqa: E402
    EC_DISABLED_ERROR, NUMS_H, hybrid_tree, mldsa44_leaf_witness, schnorr_leaf_witness, tripwire_tree,
)
from test_framework.script import CScript, OP_0, OP_RETURN, OP_TRUE  # noqa: E402


def derive(text):
    return hashlib.sha256(text.encode()).digest()


DRILL_KEY = derive("ark0-m3 staging drill tripwire key, public, test only")
USER_KEY = derive("ark0-m3 staging hybrid Schnorr key, public, test only")
PQ_PUB, PQ_KEY = mldsa44_keygen(derive("ark0-m3 staging hybrid ML-DSA-44 key, public, test only"))
TREE_NUMS = tripwire_tree(NUMS_H)
TREE_DRILL = tripwire_tree(compute_xonly_pubkey(DRILL_KEY)[0])
HYBRID = hybrid_tree(compute_xonly_pubkey(USER_KEY)[0], PQ_PUB)

OP_TRUE_SCRIPT = bytes(CScript([OP_TRUE]))
P2WSH_TRUE = bytes(CScript([OP_0, hashlib.sha256(OP_TRUE_SCRIPT).digest()]))
TRIPWIRE_SATS = 10_000
S3_SATS = 100_000
FEE = {"ec": 2_000, "pq": 20_000, "plain": 2_000}
FILLER_DATA = 98_000
FILLER_FEE = 300_000
# S3 outputs per funding transaction, and the most recheck spends from one. When recheck
# invalidates X, a funding transaction above X goes back to the mempool with the spends of its
# outputs as descendants, and the mempool keeps a cluster to 64 transactions and 101 kvB.
FUND_BATCH = 60
# invalidateblock puts back in the mempool only the transactions of the first ten blocks it
# disconnects (InvalidateBlock in validation.cpp); the deeper ones go, and their spends with them.
REORG_DEPTH = 10

REJECT_MLDSA44 = "block-script-verify-flag-failed (Invalid ML-DSA-44 signature)"
REJECT_SCHNORR = "block-script-verify-flag-failed (Invalid Schnorr signature)"
REJECT_EC = f"block-script-verify-flag-failed ({EC_DISABLED_ERROR})"
MEMPOOL_REJECT_EC = f"mempool-script-verify-flag-failed ({EC_DISABLED_ERROR})"

NODE_A, NODE_B = ark0.NODE_A, ark0.NODE_B
NODE_M2 = os.path.join(ark0.ARK0, "nodeM2")
NODES = {"nodea": NODE_A, "nodeb": NODE_B, "nodem2": NODE_M2}
M3_NODES = ("nodea", "nodeb")
STATE = os.path.join(ark0.ARK0, "m3_state.json")


# ----------------------------------------------------------------- RPC ----

def control_client():
    """A bitcoin-cli data directory for the M2 node, next to the two the entry point writes."""
    os.makedirs(NODE_M2, exist_ok=True)
    conf = open(os.path.join(NODE_A, "bitcoin.conf")).read()
    conf = conf.replace(f"rpcconnect={os.environ['ARK0_RPC_HOST_A']}", f"rpcconnect={os.environ['M3_CONTROL_HOST']}")
    conf = conf.replace(f"rpcport={os.environ['ARK0_RPC_PORT_A']}", f"rpcport={os.environ['M3_CONTROL_PORT']}")
    fd = os.open(os.path.join(NODE_M2, "bitcoin.conf"), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(conf)


def rpc(node, *args):
    return ark0.cli(*args, wallet=False, node=node)


def rpc_json(node, *args):
    return json.loads(rpc(node, *args))


def rpc_try(node, method, *args):
    """A call that may fail: (exit code, stdout, stderr). The arguments go through stdin, as ark0.py
    passes them, unless there are none: -stdin would pass an empty line as an argument."""
    if args:
        return ark0.cli_try(method, *args, wallet=False, node=node)
    p = subprocess.run([f"{ark0.BIN}/bitcoin-cli", f"-datadir={node}", "-signet", method],
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    return p.returncode, p.stdout.decode().strip(), p.stderr.decode().strip()


def send(node, tx, *, known_ok=False):
    """sendrawtransaction; with known_ok, a transaction the node has already is fine too."""
    rc, out, err = rpc_try(node, "sendrawtransaction", tx.serialize().hex())
    ark0.require(rc == 0 or (known_ok and "already" in err), f"sendrawtransaction {tx.txid_hex}: {err}")


def refused(node, tx):
    rc, out, err = rpc_try(node, "sendrawtransaction", tx.serialize().hex())
    ark0.require(rc != 0, f"the mempool took {tx.txid_hex}")
    return err.splitlines()[-1]


def test_accept(node, tx):
    return json.loads(ark0.cli_stdin("testmempoolaccept", json.dumps([tx.serialize().hex()]),
                                     wallet=False, node=node))[0]


def mempool(node):
    return set(rpc_json(node, "getrawmempool"))


def tip(node):
    return rpc(node, "getbestblockhash")


def height(node):
    return int(rpc(node, "getblockcount"))


def confirmations(node, txid):
    rc, out, _ = rpc_try(node, "getrawtransaction", txid, "1")
    return json.loads(out).get("confirmations", 0) if rc == 0 else 0


def wait_for(condition, what, timeout):
    deadline = time.time() + timeout
    while not condition():
        ark0.require(time.time() < deadline, f"{what} within {timeout} s")
        time.sleep(2)


def wait_confirmed(txids, *, nodes=(NODE_A,), depth=1, timeout=900):
    wait_for(lambda: all(confirmations(n, t) >= depth for n in nodes for t in txids),
             f"{len(txids)} transactions {depth} deep", timeout)


def wait_fresh_tip():
    """Wait for the producer's next block, so that what follows has its whole interval."""
    start = tip(NODE_A)
    wait_for(lambda: tip(NODE_A) != start, "a new block from the producer", 180)


def wait_same_tip(names, timeout=300):
    wait_for(lambda: len({tip(NODES[name]) for name in names}) == 1, f"{', '.join(names)} on the same tip", timeout)


def isolate(node):
    rpc(node, "setnetworkactive", "false")
    wait_for(lambda: int(rpc(node, "getconnectioncount")) == 0, "no connections", 60)


def rejoin(node):
    """Back on the network after isolate(). A node does not announce what its mempool took while it
    was cut off, so those transactions go to node A, parents first, for the producer to mine."""
    rpc(node, "setnetworkactive", "true")
    pool = rpc_json(node, "getrawmempool", "true")
    for txid in sorted(pool, key=lambda t: pool[t]["ancestorcount"]):
        rc, raw, err = rpc_try(node, "getrawtransaction", txid)
        if rc == 0:
            rc, _, err = rpc_try(NODE_A, "sendrawtransaction", raw)
            ark0.require(rc == 0 or "already" in err, f"node A refused {txid} from node B's mempool: {err}")
    return len(pool)


def state():
    try:
        with open(STATE) as f:
            return json.load(f)
    except FileNotFoundError:
        return {}


def remember(**fields):
    current = state()
    current.update(fields)
    with open(STATE, "w") as f:
        json.dump(current, f, indent=1)


def sats(btc):
    return round(btc * 100_000_000)


# ------------------------------------------------------- tripwire state ----

def tripwire_outputs(node=NODE_A):
    """gettripwireinfo, and the two tripwire outputs by role ("nums", "drill"), each with its state."""
    info = rpc_json(node, "gettripwireinfo")
    roles = {TREE_NUMS.scriptPubKey.hex(): "nums", TREE_DRILL.scriptPubKey.hex(): "drill"}
    found = {}
    for tripwire in info["tripwires"]:
        txid, n = tripwire["outpoint"].split(":")
        out = rpc_json(node, "getrawtransaction", txid, "1")["vout"][int(n)]
        role = roles.get(out["scriptPubKey"]["hex"])
        ark0.require(role is not None, f"tripwire output {tripwire['outpoint']} is not one of this network's")
        found[role] = {"txid": txid, "vout": int(n), "value": sats(out["value"]), "state": tripwire["state"]}
    ark0.require(set(found) == {"nums", "drill"}, f"tripwire outputs {sorted(found)}, not nums and drill")
    return info, found


def require_active():
    """The rules apply to the next block on node A."""
    info = rpc_json(NODE_A, "gettripwireinfo")
    ark0.require(info["tip_height"] + 1 >= info["height"],
                 f"the rules apply from {info['height']} and the tip is at {info['tip_height']}")
    return info


def require_state(node, *, drill, disabled):
    info, found = tripwire_outputs(node)
    ark0.require(found["nums"]["state"] == "unspent", f"the NUMS tripwire is {found['nums']['state']}")
    ark0.require(found["drill"]["state"] == drill and info["ec_disabled_next_block"] == disabled,
                 f"drill tripwire {found['drill']['state']}, checks disabled {info['ec_disabled_next_block']}; "
                 f"expected {drill} and {disabled}")
    return info


# -------------------------------------------------------- transactions ----

def coins(spk, count, *, node=NODE_A):
    """count confirmed outputs paying spk, mature if from a coinbase and not spent in node's mempool, oldest first."""
    scan = rpc_json(node, "scantxoutset", "start", json.dumps([f"raw({spk.hex()})"]))
    found = []
    for u in sorted(scan["unspents"], key=lambda u: (u["height"], u["txid"], u["vout"])):
        if u["coinbase"] and u["confirmations"] <= 100:
            continue
        if not rpc(node, "gettxout", u["txid"], str(u["vout"]), "true"):
            continue
        found.append({"txid": u["txid"], "vout": u["vout"], "value": sats(u["amount"])})
        if len(found) == count:
            return found
    raise RuntimeError(f"{len(found)} of {count} spendable outputs paying {spk.hex()}")


def build(inputs, outputs, *, version=2, sequence=0xfffffffd):
    """inputs: (coin, the scriptPubKey it pays, witness function(tx, spent outputs, index) -> stack)."""
    tx = CTransaction()
    tx.version = version
    tx.vin = [CTxIn(COutPoint(int(coin["txid"], 16), coin["vout"]), b"", sequence) for coin, _, _ in inputs]
    tx.vout = [CTxOut(value, spk) for value, spk in outputs]
    spent = [CTxOut(coin["value"], spk) for coin, spk, _ in inputs]
    tx.wit.vtxinwit = [CTxInWitness() for _ in inputs]
    for index, (_, _, witness) in enumerate(inputs):
        tx.wit.vtxinwit[index].scriptWitness.stack = witness(tx, spent, index)
    return tx


def recheck_coins(count, x_height):
    """S3 outputs recheck can spend on both sides of X, oldest first. Invalidating X puts every
    transaction above X back in node B's mempool, a funding transaction with the spends of its
    outputs, and the mempool keeps a cluster to 64 transactions and 101 kvB: so the outputs of a
    funding transaction below X, and of one above X at most FUND_BATCH, and only if none of its
    outputs paying S3 has been spent."""
    spk = HYBRID.scriptPubKey.hex()
    scan = rpc_json(NODE_A, "scantxoutset", "start", json.dumps([f"raw({spk})"]))
    by_tx = {}
    for u in scan["unspents"]:
        by_tx.setdefault(u["txid"], []).append(u)
    found = []
    for txid, outs in sorted(by_tx.items(), key=lambda item: (item[1][0]["height"], item[0])):
        outs.sort(key=lambda u: u["vout"])
        if outs[0]["height"] >= x_height:
            vout = rpc_json(NODE_A, "getrawtransaction", txid, "1")["vout"]
            if sum(o["scriptPubKey"]["hex"] == spk for o in vout) != len(outs):
                continue
            outs = outs[:FUND_BATCH]
        for u in outs:
            if not rpc(NODE_A, "gettxout", txid, str(u["vout"]), "true"):
                continue
            found.append({"txid": txid, "vout": u["vout"], "value": sats(u["amount"])})
            if len(found) == count:
                return found
    raise RuntimeError(f"{len(found)} of {count} S3 outputs that recheck can spend; fund more")


def output(tx, n=0):
    return {"txid": tx.txid_hex, "vout": n, "value": tx.vout[n].nValue}


def op_true(tx, spent, index):
    return [OP_TRUE_SCRIPT]


def drill_witness(tx, spent, index):
    return schnorr_leaf_witness(DRILL_KEY, TREE_DRILL.leaves["csig"], tx, spent, index)


def s3_spend(coin, path, *, to=P2WSH_TRUE, flip=False):
    """Spend an S3 output through its Schnorr leaf ("ec") or its ML-DSA-44 leaf ("pq"); flip turns
    one bit of the signature, which leaves the transaction well formed and the signature invalid."""
    leaf = HYBRID.leaves[path]

    def witness(tx, spent, index):
        if path == "ec":
            return schnorr_leaf_witness(USER_KEY, leaf, tx, spent, index)
        return mldsa44_leaf_witness(PQ_KEY, leaf, tx, spent, index)
    tx = build([(coin, HYBRID.scriptPubKey, witness)], [(coin["value"] - FEE[path], to)])
    if flip:
        stack = tx.wit.vtxinwit[0].scriptWitness.stack
        chunk = 0 if path == "ec" else 2
        stack[chunk] = bytes([stack[chunk][0] ^ 1]) + stack[chunk][1:]
    return tx


def spend_drill(drill, *, version=2):
    return build([(drill, TREE_DRILL.scriptPubKey, drill_witness)], [(drill["value"] - FEE["plain"], P2WSH_TRUE)],
                 version=version)


def fund(outputs):
    """One transaction from a mature coinbase to outputs (value, scriptPubKey), sent to node A."""
    coin = coins(P2WSH_TRUE, 1)[0]
    change = coin["value"] - sum(value for value, _ in outputs) - 10_000 - 200 * len(outputs)
    ark0.require(change > 100_000, "one coinbase does not cover these outputs")
    tx = build([(coin, P2WSH_TRUE, op_true)], list(outputs) + [(change, P2WSH_TRUE)])
    send(NODE_A, tx)
    return tx


def filler(coin):
    """A spend of a mature coinbase with an OP_RETURN of FILLER_DATA bytes, as m2_fill.py makes them."""
    return build([(coin, P2WSH_TRUE, op_true)],
                 [(0, bytes(CScript([OP_RETURN, bytes(FILLER_DATA)]))), (coin["value"] - FILLER_FEE, P2WSH_TRUE)])


# -------------------------------------------------------------- blocks ----

def block_with(txs, *, node=NODE_A, prev=None):
    """A block on node's tip, or at the same height on prev, that holds txs and nothing from the
    mempool, with the signet solution node A's wallet signs."""
    tmpl = rpc_json(node, "getblocktemplate", '{"rules":["signet","segwit"]}')
    tmpl["coinbasevalue"] -= sum(t["fee"] for t in tmpl["transactions"])
    tmpl["transactions"] = [{"data": tx.serialize().hex()} for tx in txs]
    if prev is not None:
        tmpl["previousblockhash"] = prev
    block = ark0.miner.new_block(tmpl, ark0.reward_spk())
    psbt = ark0.miner.generate_psbt(block, tmpl["signet_challenge"])
    processed = json.loads(ark0.cli_stdin("walletprocesspsbt", psbt))
    ark0.require(processed["complete"], f"node A's wallet could not sign the signet solution: {processed}")
    decoded = ark0.miner.decode_challenge_psbt(processed["psbt"])
    return ark0.miner.finish_block(ark0.miner.get_block_from_psbt(decoded),
                                   ark0.miner.get_solution_from_psbt(decoded), ark0.GRIND)


def submit(block, names=tuple(NODES)):
    """submitblock on each node: None where it connected the block, else what the node said."""
    blockhex = block.serialize().hex()
    return {name: ark0.cli_stdin("submitblock", blockhex, wallet=False, node=NODES[name]) or None for name in names}


def other_nonce(block):
    """Another nonce that solves the block's header. The signet solution does not sign the nonce, so
    the block with it holds the same transactions, coinbase included, under another hash."""
    header = bytearray(CBlockHeader(block).serialize())
    target = ark0.miner.nbits_to_target(block.nBits)
    nonce = block.nNonce
    for _ in range(1 << 32):
        nonce = (nonce + 1) & 0xffffffff
        struct.pack_into("<I", header, 76, nonce)
        if int.from_bytes(hashlib.sha256(hashlib.sha256(header).digest()).digest(), "little") <= target:
            return nonce
    raise RuntimeError("no other nonce solves this header")


def tip_status(node, blockhash):
    """What getchaintips says of the block, or None when it is no tip there."""
    return next((t["status"] for t in rpc_json(node, "getchaintips") if t["hash"] == blockhash), None)


def m3_refuse_m2_takes(txs, reason, attempts=5):
    """A block on the tip holding txs: both M3 nodes refuse it for reason, the M2 node connects it.
    Node A passes a new block on to its peers once its header and proof of work check out, before
    its scripts are checked, so node B and the M2 node are cut off while the block is submitted to
    each node in turn: each answers for itself. A block that lost the race with the producer's next
    one is checked by nobody ("inconclusive"), so it is tried again on the new tip."""
    others = (NODE_B, NODE_M2)
    for _ in range(attempts):
        wait_fresh_tip()
        wait_same_tip(tuple(NODES), timeout=60)
        at = height(NODE_A) + 1
        block = block_with(txs)
        try:
            for node in others:
                isolate(node)
            answers = submit(block)
            tips = {name: tip_status(node, block.hash_hex) for name, node in NODES.items()}
        finally:
            for node in others:
                rpc(node, "setnetworkactive", "true")
        if "inconclusive" in answers.values():
            continue
        ark0.require(all(answers[name] == reason for name in M3_NODES),
                     f"the M3 nodes did not refuse the block for {reason}: {answers}")
        ark0.require(answers["nodem2"] is None, f"the M2 node did not take the block: {answers}")
        ark0.require(all(tips[name] == "invalid" for name in M3_NODES), f"the M3 nodes do not hold the block invalid: {tips}")
        # The M2 node goes back to the producer's chain once that is longer.
        wait_same_tip(tuple(NODES))
        return {"height": at, "block": block.hash_hex, "answers": answers, "tips": tips}
    raise RuntimeError(f"{attempts} blocks in a row lost the race with the producer")


def reorg_across(x, before_reconsider=lambda: None):
    """On node B: invalidateblock x, a block of node B's own on x's parent, then reconsiderblock x.
    The last call disconnects that block and connects x and its successors in one step of
    ActivateBestChain, after which MaybeUpdateMempoolForReorg, with the checks disabled again,
    checks every spend of a 0xe0 leaf in the mempool again, holding cs_main. Returns the times of
    the calls and the mempool before and after the last one."""
    start = time.time()
    rpc(NODE_B, "invalidateblock", x)
    invalidated = time.time() - start
    own = block_with([], node=NODE_B)
    ark0.require(submit(own, ["nodeb"])["nodeb"] is None, "node B did not connect its own block")
    before_reconsider()
    before = rpc_json(NODE_B, "getmempoolinfo")
    rpc(NODE_B, "logging", '["bench"]', "[]")
    start = time.time()
    rpc(NODE_B, "reconsiderblock", x)
    reconsidered = time.time() - start
    rpc(NODE_B, "logging", "[]", '["bench"]')
    after = rpc_json(NODE_B, "getmempoolinfo")
    ark0.require(tip(NODE_B) != own.hash_hex, "node B stayed on its own block")
    # Out of the way of the next round, where invalidateblock x would otherwise land on it.
    rpc(NODE_B, "invalidateblock", own.hash_hex)
    return {"invalidateblock_s": round(invalidated, 3), "reconsiderblock_s": round(reconsidered, 3),
            "mempool_before": {"txs": before["size"], "bytes": before["bytes"]},
            "mempool_after": {"txs": after["size"], "bytes": after["bytes"]}}


def block_x(args):
    x = args.x or state().get("x_hash")
    ark0.require(x, "no block X: run trigger first, or give --x <hash>")
    return x, rpc_json(NODE_A, "getblockheader", x)["height"]


# --------------------------------------------------------------- steps ----

def cmd_tripwires(args):
    rc, _, _ = rpc_try(NODE_A, "gettripwireinfo")
    ark0.require(rc != 0, "node A has the tripwire rules already: the outputs exist")
    tx = fund([(TRIPWIRE_SATS, TREE_NUMS.scriptPubKey), (TRIPWIRE_SATS, TREE_DRILL.scriptPubKey)])
    wait_confirmed([tx.txid_hex], nodes=(NODE_A, NODE_B), depth=args.confirmations)
    created = rpc_json(NODE_A, "getblockheader", rpc_json(NODE_A, "getrawtransaction", tx.txid_hex, "1")["blockhash"])
    at = height(NODE_A)
    h3 = (at + args.margin) // 10 * 10 + 10
    outpoints = f"{tx.txid_hex}:0,{tx.txid_hex}:1"
    remember(tripwires=outpoints, created=created["height"])
    print(json.dumps({"nums": f"{tx.txid_hex}:0", "drill": f"{tx.txid_hex}:1", "created_at": created["height"],
                      "tip": at, "suggested_h3": h3, "arm": f"./m3_arm.sh {h3} {outpoints}"}, indent=1))


def cmd_fund(args):
    require_active()
    txs = []
    for start in range(0, args.count, FUND_BATCH):
        n = min(FUND_BATCH, args.count - start)
        txs.append(fund([(S3_SATS, HYBRID.scriptPubKey)] * n))
    wait_confirmed([tx.txid_hex for tx in txs])
    print(json.dumps({"funded": args.count, "txids": [tx.txid_hex for tx in txs]}, indent=1))


def cmd_paths(args):
    info = require_active()
    ark0.require(not info["ec_disabled_next_block"], "elliptic curve checks are disabled already: paths comes before trigger")
    ec_coin, pq_coin = coins(HYBRID.scriptPubKey, 2)
    spends = {"ec": s3_spend(ec_coin, "ec"), "pq": s3_spend(pq_coin, "pq")}
    for tx in spends.values():
        send(NODE_A, tx)
    wait_confirmed([tx.txid_hex for tx in spends.values()], nodes=(NODE_A, NODE_B, NODE_M2))
    result = {}
    for path, tx in spends.items():
        seen = rpc_json(NODE_A, "getrawtransaction", tx.txid_hex, "1")
        result[path] = {"txid": tx.txid_hex, "block": seen["blockhash"], "vsize": seen["vsize"], "weight": seen["weight"]}
    print(json.dumps(result, indent=1))


def cmd_negative(args):
    info = require_active()
    pq_coin, ec_coin = coins(HYBRID.scriptPubKey, 2)
    # Once the checks are disabled, the Schnorr check fails for that before its signature is read.
    ec_reason = REJECT_EC if info["ec_disabled_next_block"] else REJECT_SCHNORR
    cases = {
        "an invalid ML-DSA-44 signature in a 0xe0 leaf": ([s3_spend(pq_coin, "pq", flip=True)], REJECT_MLDSA44),
        "an invalid Schnorr signature in a 0xe0 leaf": ([s3_spend(ec_coin, "ec", flip=True)], ec_reason),
    }
    print(json.dumps({name: m3_refuse_m2_takes(txs, reason) for name, (txs, reason) in cases.items()}, indent=1))


def cmd_emptypool(args):
    require_active()
    _, found = tripwire_outputs()
    for node in (NODE_A, NODE_B):
        require_state(node, drill="unspent", disabled=False)
    wait_same_tip(("nodea", "nodeb"))
    fund_coin = coins(P2WSH_TRUE, 1)[0]
    parent = build([(fund_coin, P2WSH_TRUE, op_true)], [(fund_coin["value"] - FEE["plain"], P2WSH_TRUE)])
    # A relative lock of one block on the parent, so that the child's lock points name the block
    # that confirms it.
    child = build([(output(parent), P2WSH_TRUE, op_true)], [(parent.vout[0].nValue - FEE["plain"], P2WSH_TRUE)],
                  sequence=1)
    schnorr = s3_spend(coins(HYBRID.scriptPubKey, 1)[0], "ec")
    # Version 4 is not standard: when the reorganization is undone, the spend of the drill tripwire
    # does not go back into node B's mempool, from where it would reach the network.
    trigger = spend_drill(found["drill"], version=4)

    isolate(NODE_B)
    b1 = None
    try:
        a1 = block_with([parent], node=NODE_B)
        ark0.require(submit(a1, ["nodeb"])["nodeb"] is None, "node B did not connect A1")
        send(NODE_B, child)
        send(NODE_B, schnorr)
        b1 = copy.deepcopy(a1)
        b1.nNonce = other_nonce(a1)
        ark0.require([tx.txid_hex for tx in b1.vtx] == [tx.txid_hex for tx in a1.vtx] and b1.hash_hex != a1.hash_hex,
                     "B1 is not A1 under another nonce")
        ark0.require(submit(b1, ["nodeb"])["nodeb"] == "inconclusive", "node B did not keep B1 aside")
        b2 = block_with([trigger], node=NODE_B, prev=b1.hash_hex)
        answer = submit(b2, ["nodeb"])["nodeb"]
        # A node that took this step for a plain connect, because connecting B1 emptied
        # disconnectpool, would check its mempool again before the child's lock points, which
        # still name A1, are mended, and stop on them.
        ark0.require(answer is None and tip(NODE_B) == b2.hash_hex, f"node B did not connect B2: {answer}")
        require_state(NODE_B, drill="missing", disabled=True)
        pool = mempool(NODE_B)
        ark0.require(child.txid_hex in pool, "the child left node B's mempool")
        ark0.require(schnorr.txid_hex not in pool, "the Schnorr spend is still in node B's mempool")
        result = {"a1": a1.hash_hex, "b1": b1.hash_hex, "b2": b2.hash_hex, "child_kept": child.txid_hex,
                  "schnorr_removed": schnorr.txid_hex}
    finally:
        try:
            if b1 is not None:
                rpc(NODE_B, "invalidateblock", b1.hash_hex)
            rejoin(NODE_B)
        except Exception as e:
            print(f"node B did not answer ({e}): restart it with "
                  "kubectl -n ark0-m3 rollout restart statefulset/nodeb", file=sys.stderr)
            raise
    wait_same_tip(("nodea", "nodeb"))
    require_state(NODE_B, drill="unspent", disabled=False)
    print(json.dumps(result, indent=1))


def cmd_trigger(args):
    require_active()
    _, found = tripwire_outputs()
    for node in (NODE_A, NODE_B):
        require_state(node, drill="unspent", disabled=False)
    c = coins(HYBRID.scriptPubKey, 7)
    waiting_ec = [s3_spend(c[0], "ec"), s3_spend(c[1], "ec")]
    waiting_pq = [s3_spend(c[2], "pq"), s3_spend(c[3], "pq")]
    parent_pq = s3_spend(c[4], "pq", to=HYBRID.scriptPubKey)
    child_ec = s3_spend(output(parent_pq), "ec")
    late_ec, block_ec = s3_spend(c[5], "ec"), s3_spend(c[6], "ec")
    trigger = spend_drill(found["drill"])
    gone = waiting_ec + [child_ec]
    kept = waiting_pq + [parent_pq]

    # Right after the producer's block: the spends wait in both mempools, and block X, which holds
    # the spend of the drill tripwire and nothing else, comes before the producer's next block.
    wait_fresh_tip()
    wait_same_tip(tuple(NODES), timeout=60)
    for node in (NODE_A, NODE_B):
        for tx in kept + gone:
            send(node, tx, known_ok=True)
    x = block_with([trigger])
    answers = submit(x)
    ark0.require(all(answer in (None, "duplicate") for answer in answers.values()),
                 f"block X was not connected everywhere: {answers}. If the producer's block came first, "
                 "it holds the waiting spends; run trigger again")
    x_height = rpc_json(NODE_A, "getblockheader", x.hash_hex)["height"]
    remember(x_hash=x.hash_hex, x_height=x_height)

    for name in ("nodea", "nodeb"):
        node = NODES[name]
        require_state(node, drill="missing", disabled=True)
        pool = mempool(node)
        # The producer may have mined the ML-DSA-44 spends since.
        ark0.require(all(tx.txid_hex in pool or confirmations(node, tx.txid_hex) for tx in kept),
                     f"{name} lost an ML-DSA-44 spend")
        ark0.require(not any(tx.txid_hex in pool or confirmations(node, tx.txid_hex) for tx in gone),
                     f"{name} kept a Schnorr spend")
    mempool_reason = refused(NODE_A, late_ec)
    ark0.require(MEMPOOL_REJECT_EC in mempool_reason, f"the mempool refused a Schnorr spend for: {mempool_reason}")
    in_block = m3_refuse_m2_takes([block_ec], REJECT_EC)
    wait_confirmed([tx.txid_hex for tx in kept], nodes=(NODE_A, NODE_B, NODE_M2))
    print(json.dumps({"x": x.hash_hex, "x_height": x_height, "trigger": trigger.txid_hex,
                      "removed": [tx.txid_hex for tx in gone], "kept_and_mined": [tx.txid_hex for tx in kept],
                      "mempool_refusal": mempool_reason, "block_with_a_schnorr_spend": in_block}, indent=1))


def cmd_reorg(args):
    x, x_height = block_x(args)
    require_state(NODE_A, drill="missing", disabled=True)
    ec = s3_spend(coins(HYBRID.scriptPubKey, 1)[0], "ec")
    result = {}
    start = time.time()
    rpc(NODE_B, "invalidateblock", x)
    result["invalidateblock_s"] = round(time.time() - start, 3)
    try:
        ark0.require(height(NODE_B) == x_height - 1, "node B is not on X's parent")
        require_state(NODE_B, drill="unspent", disabled=False)
        result["below_x"] = test_accept(NODE_B, ec)
        ark0.require(result["below_x"]["allowed"], "below X, node B refused a Schnorr spend")
    finally:
        start = time.time()
        rpc(NODE_B, "reconsiderblock", x)
        result["reconsiderblock_s"] = round(time.time() - start, 3)
    wait_same_tip(("nodea", "nodeb"))
    require_state(NODE_B, drill="missing", disabled=True)
    result["above_x"] = test_accept(NODE_B, ec)
    ark0.require(result["above_x"].get("reject-reason") == MEMPOOL_REJECT_EC,
                 f"above X, node B answered {result['above_x']}")
    print(json.dumps(result, indent=1))


def cmd_recheck(args):
    x, x_height = block_x(args)
    require_state(NODE_A, drill="missing", disabled=True)
    ark0.require(height(NODE_A) > x_height, "wait for a block above X, so that X's branch outweighs one block")
    if args.at_tip:
        # Above X the mempool refuses Schnorr spends, so there are none to remove.
        ark0.require(args.schnorr == 0, "--at-tip takes --schnorr 0")
        coins_below = height(NODE_A) - 1
    else:
        ark0.require(height(NODE_A) - x_height + 1 <= REORG_DEPTH,
                     f"X is more than {REORG_DEPTH} blocks deep, and invalidating it would not put back what is "
                     "above it; run recheck right after trigger, or with --at-tip --schnorr 0")
        coins_below = x_height
    s3 = recheck_coins(args.count + args.schnorr, coins_below)
    print(f"signing {args.count} ML-DSA-44 spends", file=sys.stderr)
    pq = [s3_spend(coin, "pq") for coin in s3[:args.count]]
    ec = [s3_spend(coin, "ec") for coin in s3[args.count:]]
    fillers = [filler(coin) for coin in coins(P2WSH_TRUE, args.fillers)]
    wait_same_tip(("nodea", "nodeb"))
    isolate(NODE_B)
    try:
        tip_height = height(NODE_B)
        if args.at_tip:
            # The tip's parent: a reorganization of two blocks, with the checks disabled at both ends.
            x, x_height = rpc(NODE_B, "getblockhash", str(tip_height - 1)), tip_height - 1
        ark0.require(tip_height - x_height + 1 <= REORG_DEPTH,
                     f"node B's tip is {tip_height - x_height + 1} blocks above and including {x}")
        baseline = reorg_across(x)
        for tx in pq + fillers:
            send(NODE_B, tx)

        def schnorr_below_x():
            # Valid below X: these are the spends the check removes again.
            for tx in ec:
                send(NODE_B, tx)
        loaded = reorg_across(x, schnorr_below_x)
        pool = mempool(NODE_B)
        ark0.require(not any(tx.txid_hex in pool for tx in ec), "a Schnorr spend is still in node B's mempool")
        ark0.require(all(tx.txid_hex in pool for tx in pq), "an ML-DSA-44 spend left node B's mempool")
    finally:
        try:
            # Already done unless a round stopped half way; without it node B could not follow X.
            rpc(NODE_B, "reconsiderblock", x)
            rejoin(NODE_B)
        except Exception as e:
            print(f"node B did not answer ({e}): restart it with "
                  "kubectl -n ark0-m3 rollout restart statefulset/nodeb", file=sys.stderr)
            raise
    wait_same_tip(("nodea", "nodeb"))
    print(json.dumps({"mode": "at the tip" if args.at_tip else "across X", "invalidated": x, "height": x_height,
                      "blocks_disconnected": tip_height - x_height + 1, "mldsa44_spends": len(pq),
                      "schnorr_spends": len(ec), "fillers": len(fillers), "baseline": baseline, "loaded": loaded},
                     indent=1))


def cmd_rejoin(args):
    print(json.dumps({"sent_to_node_a": rejoin(NODE_B)}, indent=1))


def cmd_status(args):
    result = {}
    for name, node in NODES.items():
        info = rpc_json(node, "getmempoolinfo")
        entry = {"height": height(node), "tip": tip(node), "connections": int(rpc(node, "getconnectioncount")),
                 "mempool": {"txs": info["size"], "bytes": info["bytes"]}}
        if name != "nodem2":
            rc, out, err = rpc_try(node, "gettripwireinfo")
            entry["tripwire"] = json.loads(out) if rc == 0 else err.splitlines()[-1]
            deployments = rpc_json(node, "getdeploymentinfo")["deployments"]
            # p2mr_tripwire is there once m3_arm.sh has given the node the rules.
            entry["deployments"] = {k: deployments[k] for k in ("p2mr", "p2mr_mldsa44", "p2mr_tripwire")
                                    if k in deployments}
        result[name] = entry
    print(json.dumps(result, indent=1))


def cmd_times(args):
    for h in range(args.start, height(NODE_A) + 1):
        block = rpc_json(NODE_A, "getblock", rpc(NODE_A, "getblockhash", str(h)), "1")
        print(h, block["time"], block["weight"], block["nTx"], flush=True)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="step", required=True)
    s = sub.add_parser("tripwires")
    s.add_argument("--confirmations", type=int, default=6)
    s.add_argument("--margin", type=int, default=240, help="blocks between the tip and the suggested H3")
    s.set_defaults(fn=cmd_tripwires)
    s = sub.add_parser("fund")
    s.add_argument("--count", type=int, default=20)
    s.set_defaults(fn=cmd_fund)
    for name, fn in (("paths", cmd_paths), ("negative", cmd_negative), ("emptypool", cmd_emptypool),
                     ("trigger", cmd_trigger), ("rejoin", cmd_rejoin), ("status", cmd_status)):
        sub.add_parser(name).set_defaults(fn=fn)
    s = sub.add_parser("reorg")
    s.add_argument("--x", help="block X, the one that spends the drill tripwire (default: from trigger)")
    s.set_defaults(fn=cmd_reorg)
    s = sub.add_parser("recheck")
    s.add_argument("--x", help="block X, the one that spends the drill tripwire (default: from trigger)")
    s.add_argument("--count", type=int, default=500, help="ML-DSA-44 spends in the full mempool")
    s.add_argument("--schnorr", type=int, default=50, help="Schnorr spends the check removes")
    s.add_argument("--fillers", type=int, default=12, help="spends with a 98,000-byte OP_RETURN")
    s.add_argument("--at-tip", action="store_true",
                   help="reorganize the tip's two blocks instead of across X (with --schnorr 0), once X is too deep")
    s.set_defaults(fn=cmd_recheck)
    s = sub.add_parser("times")
    s.add_argument("--start", type=int, default=1)
    s.set_defaults(fn=cmd_times)
    args = p.parse_args()
    control_client()
    args.fn(args)


if __name__ == "__main__":
    main()

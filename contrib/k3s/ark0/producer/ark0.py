#!/usr/bin/env python3
"""Ark-0 experimental signet: BIP-360 P2MR spend demonstration.

RPC goes through bitcoin-cli against node A. Private keys are read from files
under the Ark-0 home directory and are never printed. Every step appends a JSON
record to <home>/evidence/steps.jsonl so the evidence pack can be assembled
from it.

Nothing here is a secret and nothing here is host specific. Every path and the
wallet name come from the environment, and each default is the layout a single
operator gets by running the demo out of a home directory:

  ARK0_HOME         state, keys and the evidence log     ~/.ark0
  ARK0_BIN          directory holding bitcoin-cli and
                    bitcoin-util                         ~/bitcoin-p2mr/build/bin
  ARK0_MINER_PATH   upstream contrib/signet/miner        ~/bitcoin-p2mr/contrib/signet/miner
  ARK0_NODE_A       node A data directory                $ARK0_HOME/nodeA
  ARK0_NODE_B       node B data directory                $ARK0_HOME/nodeB
  ARK0_WALLET       wallet holding the challenge key     ark0

Dockerfile.miner sets all six to the layout inside the producer image, so the
same file runs unchanged on a workstation and in a pod.
"""
import argparse
import importlib.machinery
import importlib.util
import json
import os
import subprocess
import sys
import time

ARK0 = os.environ.get("ARK0_HOME") or os.path.expanduser("~/.ark0")
BIN = os.environ.get("ARK0_BIN") or os.path.expanduser("~/bitcoin-p2mr/build/bin")
MINER_PATH = os.environ.get("ARK0_MINER_PATH") or os.path.expanduser("~/bitcoin-p2mr/contrib/signet/miner")
GRIND = f"{BIN}/bitcoin-util grind"
NODE_A = os.environ.get("ARK0_NODE_A") or os.path.join(ARK0, "nodeA")
NODE_B = os.environ.get("ARK0_NODE_B") or os.path.join(ARK0, "nodeB")
WALLET = os.environ.get("ARK0_WALLET") or "ark0"
HRP = "tb"
EVIDENCE = os.path.join(ARK0, "evidence")
STEPS = os.path.join(EVIDENCE, "steps.jsonl")
NO_CODESEPARATOR = 0xffffffff
FEE_SATS = 10_000
# What a node says when it refuses a block because a script in it did not
# verify under a mandatory flag. Both malformed-block assertions require this
# prefix rather than any rejection at all: a block turned away for a bad
# timestamp or a bad Merkle root would also be "rejected", and would prove
# nothing about the P2MR rules.
BLOCK_REJECT = "block-script-verify-flag-failed"
# An upgraded, even, non-0xc0 leaf version. Its script is never executed.
LEAF_VERSION_FUTURE = 0xfa

# Load contrib/signet/miner as a module: new_block / generate_psbt /
# finish_block are exactly the block assembly the signet miner uses, so a
# block carrying a hand-made transaction is built the same way as any other.
_loader = importlib.machinery.SourceFileLoader("signetminer", MINER_PATH)
_spec = importlib.util.spec_from_loader("signetminer", _loader)
miner = importlib.util.module_from_spec(_spec)
_loader.exec_module(miner)

from test_framework.key import compute_xonly_pubkey, sign_schnorr  # noqa: E402
from test_framework.messages import (  # noqa: E402
    COutPoint, CTransaction, CTxIn, CTxInWitness, CTxOut,
)
from test_framework.p2mr import (  # noqa: E402
    P2MR_CONTROL_NODE_SIZE, p2mr_address, p2mr_construct,
)
from test_framework.script import (  # noqa: E402
    CScript, OP_CHECKSIG, OP_RETURN, SIGHASH_DEFAULT, TaprootSignatureHash,
)


# ----------------------------------------------------------------- RPC ----

def _cmd(wallet, node):
    cmd = [f"{BIN}/bitcoin-cli", f"-datadir={node}", "-signet"]
    if wallet:
        cmd.append(f"-rpcwallet={WALLET}")
    return cmd


def cli(*args, wallet=True, node=NODE_A):
    out = subprocess.run(_cmd(wallet, node) + list(args), stdout=subprocess.PIPE,
                         check=True).stdout
    return out.decode().strip()


def cli_stdin(method, *args, wallet=True, node=NODE_A):
    """Pass long arguments (block/tx hex, PSBTs) through stdin."""
    payload = ("\n".join(args) + "\n").encode()
    out = subprocess.run(_cmd(wallet, node) + ["-stdin", method],
                         stdout=subprocess.PIPE, input=payload, check=True).stdout
    return out.decode().strip()


def cli_try(method, *args, wallet=True, node=NODE_A):
    """Run a call that is expected to fail; return (rc, stdout, stderr)."""
    payload = ("\n".join(args) + "\n").encode()
    p = subprocess.run(_cmd(wallet, node) + ["-stdin", method],
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, input=payload)
    return p.returncode, p.stdout.decode().strip(), p.stderr.decode().strip()


def cli_json(*args, **kw):
    return json.loads(cli(*args, **kw))


def record(step, **fields):
    os.makedirs(EVIDENCE, exist_ok=True)
    entry = {"step": step, "time": int(time.time()), **fields}
    with open(STEPS, "a") as f:
        f.write(json.dumps(entry) + "\n")
    print(json.dumps(entry, indent=2))
    return entry


# --------------------------------------------------------------- mining ----

def reward_spk():
    addr = open(os.path.join(ARK0, "reward_address.txt")).read().strip()
    return bytes.fromhex(cli_json("getaddressinfo", addr)["scriptPubKey"])


def mine_block(extra_raw_txs=()):
    """Assemble, sign and submit one signet block.

    extra_raw_txs are appended to the template's transaction list verbatim;
    new_block() only reads each entry's "data" field, so a transaction the
    mempool refused can still be put into a block.
    Returns (block_hex, blockhash_or_None, submitblock_result).
    """
    tmpl = cli_json("getblocktemplate", '{"rules":["signet","segwit"]}', wallet=False)
    for raw in extra_raw_txs:
        tmpl["transactions"].append({"data": raw})
    block = miner.new_block(tmpl, reward_spk())
    psbt = miner.generate_psbt(block, tmpl["signet_challenge"])
    processed = json.loads(cli_stdin("walletprocesspsbt", psbt))
    assert processed["complete"], f"wallet could not sign the signet solution: {processed}"
    decoded = miner.decode_challenge_psbt(processed["psbt"])
    block = miner.get_block_from_psbt(decoded)
    solution = miner.get_solution_from_psbt(decoded)
    block = miner.finish_block(block, solution, GRIND)
    blockhex = block.serialize().hex()
    result = cli_stdin("submitblock", blockhex, wallet=False)
    accepted = result == ""
    return blockhex, (block.hash_hex if accepted else None), (result or None)


def cmd_mine(args):
    for i in range(args.count):
        blockhex, blockhash, result = mine_block()
        assert result is None, f"block rejected: {result}"
        height = int(cli("getblockcount", wallet=False))
        if args.quiet:
            # The block producer service runs this in a loop; keep it out of
            # the evidence log and print one line for the journal.
            print(f"height {height} {blockhash}", flush=True)
        elif args.count <= 3 or i == args.count - 1:
            record("mine", height=height, blockhash=blockhash)
        else:
            print(f"height {height} {blockhash}")


# ----------------------------------------------------------------- P2MR ----

def demo_tree(key_file="demo_key.hex"):
    """Two-leaf P2MR tree: leaf 'csig' is spendable, leaf 'ret' never is."""
    priv = bytes.fromhex(open(os.path.join(ARK0, key_file)).read().strip())
    xonly, _ = compute_xonly_pubkey(priv)
    leaf_csig = CScript([xonly, OP_CHECKSIG])
    leaf_ret = CScript([OP_RETURN])
    info = p2mr_construct([("csig", leaf_csig), ("ret", leaf_ret)])
    return priv, info


def tree_summary():
    priv, info = demo_tree()
    xonly, _ = compute_xonly_pubkey(priv)
    csig, ret = info.leaves["csig"], info.leaves["ret"]
    return {
        "address": p2mr_address(info.merkle_root, HRP),
        "merkle_root": info.merkle_root.hex(),
        "scriptPubKey": info.scriptPubKey.hex(),
        "demo_xonly_pubkey": xonly.hex(),
        "leaf_csig": {
            "script_asm": "<demo_xonly_pubkey> OP_CHECKSIG",
            "script_hex": bytes(csig.script).hex(),
            "leaf_version": hex(csig.leaf_ver),
            "leaf_hash": csig.leaf_hash.hex(),
            "control_block": csig.control_block.hex(),
        },
        "leaf_ret": {
            "script_asm": "OP_RETURN",
            "script_hex": bytes(ret.script).hex(),
            "leaf_version": hex(ret.leaf_ver),
            "leaf_hash": ret.leaf_hash.hex(),
            "control_block": ret.control_block.hex(),
        },
    }


def cmd_addr(args):
    summary = tree_summary()
    os.makedirs(EVIDENCE, exist_ok=True)
    with open(os.path.join(EVIDENCE, "p2mr_tree.json"), "w") as f:
        json.dump(summary, f, indent=2)
        f.write("\n")
    record("p2mr_tree", **summary)


def find_vout(txid, spk_hex):
    tx = cli_json("getrawtransaction", txid, "1", wallet=False)
    for out in tx["vout"]:
        if out["scriptPubKey"]["hex"] == spk_hex:
            return out["n"], int(round(out["value"] * 100_000_000))
    raise AssertionError(f"no output paying {spk_hex} in {txid}")


def cmd_fund(args):
    _, info = demo_tree()
    addr = p2mr_address(info.merkle_root, HRP)
    spk_hex = info.scriptPubKey.hex()
    amount = f"{args.amount:.8f}"

    rc, out, err = cli_try("sendtoaddress", addr, amount)
    if rc == 0:
        txid, method = out, "sendtoaddress"
        fallback_reason = None
    else:
        # Fall back to the raw transaction path and record why.
        fallback_reason = err
        raw = cli("createrawtransaction", "[]", json.dumps({addr: amount}))
        funded = json.loads(cli_stdin("fundrawtransaction", raw))
        signed = json.loads(cli_stdin("signrawtransactionwithwallet", funded["hex"]))
        assert signed["complete"], signed
        txid = cli_stdin("sendrawtransaction", signed["hex"])
        method = "createrawtransaction+fundrawtransaction+signrawtransactionwithwallet"

    blockhex, blockhash, result = mine_block()
    assert result is None, f"funding block rejected: {result}"
    vout, value = find_vout(txid, spk_hex)
    height = int(cli("getblockcount", wallet=False))

    utxo = {"txid": txid, "vout": vout, "value": value, "address": addr,
            "scriptPubKey": spk_hex}
    with open(os.path.join(ARK0, args.save), "w") as f:
        json.dump(utxo, f, indent=2)
    record("fund", method=method, fallback_reason=fallback_reason, address=addr,
           txid=txid, vout=vout, value_sats=value, confirmed_in=blockhash,
           height=height, saved_as=args.save)


# -------------------------------------------------------------- spending ----

def load_utxo(name):
    return json.load(open(os.path.join(ARK0, name)))


def build_spend(utxo, stack, dest_spk):
    tx = CTransaction()
    tx.version = 2
    tx.nLockTime = 0
    tx.vin = [CTxIn(COutPoint(int(utxo["txid"], 16), utxo["vout"]), b"", 0)]
    tx.vout = [CTxOut(utxo["value"] - FEE_SATS, dest_spk)]
    witin = CTxInWitness()
    witin.scriptWitness.stack = list(stack)
    tx.wit.vtxinwit = [witin]
    return tx


def sign_csig_leaf(tx, utxo, info, priv):
    leaf = info.leaves["csig"]
    spent = [CTxOut(utxo["value"], info.scriptPubKey)]
    sighash = TaprootSignatureHash(tx, spent, SIGHASH_DEFAULT, input_index=0,
                                   scriptpath=True, leaf_script=leaf.script,
                                   leaf_ver=leaf.leaf_ver,
                                   codeseparator_pos=NO_CODESEPARATOR)
    return sign_schnorr(priv, sighash)


def dest_script(cache=None):
    """Destination for a demo spend.

    The malformed cases pass a cache file so that the transaction the mempool
    refuses and the transaction the block carries are byte for byte the same,
    and therefore share a txid.
    """
    path = os.path.join(ARK0, cache) if cache else None
    if path and os.path.exists(path):
        addr = open(path).read().strip()
    else:
        addr = cli("getnewaddress", "ark0-p2mr-demo-destination", "bech32m")
        if path:
            with open(path, "w") as f:
                f.write(addr)
    return addr, bytes.fromhex(cli_json("getaddressinfo", addr)["scriptPubKey"])


def cmd_spend(args):
    priv, info = demo_tree()
    utxo = load_utxo(args.utxo)
    leaf = info.leaves["csig"]
    dest_addr, dest_spk = dest_script()

    # Placeholder witness first, then the same stack with the real signature:
    # the signature covers the transaction, not the witness.
    stack = [b"", bytes(leaf.script), leaf.control_block]
    tx = build_spend(utxo, stack, dest_spk)
    sig = sign_csig_leaf(tx, utxo, info, priv)
    stack[0] = sig
    tx = build_spend(utxo, stack, dest_spk)
    rawhex = tx.serialize().hex()

    txid = cli_stdin("sendrawtransaction", rawhex)
    in_mempool = txid in json.loads(cli("getrawmempool", wallet=False))
    blockhex, blockhash, result = mine_block()
    assert result is None, f"block rejected: {result}"

    confirmed = cli_json("getrawtransaction", txid, "1", wallet=False)
    spent_check = cli("gettxout", utxo["txid"], str(utxo["vout"]), wallet=False)
    height = int(cli("getblockcount", wallet=False))
    tip_b = cli("getbestblockhash", wallet=False, node=NODE_B)
    seen_b = cli_json("getrawtransaction", txid, "1", wallet=False, node=NODE_B)

    os.makedirs(EVIDENCE, exist_ok=True)
    with open(os.path.join(EVIDENCE, "spend_raw.hex"), "w") as f:
        f.write(rawhex + "\n")
    record("valid_spend",
           txid=txid, entered_mempool=in_mempool, raw_hex=rawhex,
           witness=[s.hex() for s in stack],
           blockhash=blockhash, height=height,
           confirmations=confirmed["confirmations"],
           vsize=confirmed["vsize"], weight=confirmed["weight"],
           fee_sats=FEE_SATS, destination=dest_addr,
           prevout_gettxout=(spent_check or "null (already spent)"),
           nodeB_besthash=tip_b,
           nodeB_confirmations=seen_b["confirmations"],
           nodeB_blockhash=seen_b["blockhash"])


# ---------------------------------------------------- malformed spending ----

def make_bad_tx(mode, utxo, info, priv, dest_spk):
    """Build one malformed P2MR spend. Returns (tx, stack, description)."""
    leaf = info.leaves["csig"]
    if mode == "lowbit":
        # c[0] with the low bit cleared. BIP-360 requires c[0] | 1, so that an
        # implementation reading the leaf version as c[0] rather than
        # c[0] & 0xfe fails immediately.
        control = bytes([leaf.control_block[0] & 0xfe]) + leaf.merklebranch
        desc = "control block first byte 0xc1 -> 0xc0 (low bit cleared)"
    elif mode == "wrongroot":
        # A well formed control block whose Merkle path folds to some other
        # root, so it does not reach the witness program.
        control = bytes([leaf.control_block[0]]) + bytes(P2MR_CONTROL_NODE_SIZE)
        desc = "control block with an all-zero inner node: path misses the root"
    elif mode == "wrongkey":
        control = leaf.control_block
        desc = "well formed spend signed by a different key"
    else:
        raise AssertionError(mode)

    stack = [b"", bytes(leaf.script), control]
    tx = build_spend(utxo, stack, dest_spk)
    if mode == "wrongkey":
        wrong = bytes.fromhex(
            open(os.path.join(ARK0, "demo_wrong_key.hex")).read().strip())
        stack[0] = sign_csig_leaf(tx, utxo, info, wrong)
    else:
        # The spend fails before the signature is looked at; 64 zero bytes are
        # a well formed placeholder.
        stack[0] = bytes(64)
    tx = build_spend(utxo, stack, dest_spk)
    return tx, stack, desc


def cmd_badspend(args):
    priv, info = demo_tree()
    utxo = load_utxo(args.utxo)
    _, dest_spk = dest_script("bad_dest_address.txt")
    tx, stack, desc = make_bad_tx(args.mode, utxo, info, priv, dest_spk)
    rawhex = tx.serialize().hex()

    rc, out, err = cli_try("sendrawtransaction", rawhex)
    assert rc != 0, f"mempool accepted a malformed spend: {out}"
    os.makedirs(EVIDENCE, exist_ok=True)
    path = os.path.join(EVIDENCE, f"bad_{args.mode}.hex")
    with open(path, "w") as f:
        f.write(rawhex + "\n")
    record("mempool_reject", mode=args.mode, description=desc,
           txid=tx.txid_hex, raw_hex_file=path, raw_hex=rawhex,
           witness=[s.hex() for s in stack],
           sendrawtransaction_error=err)


def cmd_badblock(args):
    """Put a malformed spend into a signet block and submit it to both nodes.

    Everything this writes into the evidence pack is asserted first, and the
    assertions are the point of the command. Recording node B's answer without
    checking it, or accepting any rejection reason at all, would let a run that
    proved nothing produce a file that looks like proof: a block refused
    because it was malformed in some unrelated way, or refused by node A while
    node B quietly accepted it, would have been written out just the same.
    """
    priv, info = demo_tree()
    utxo = load_utxo(args.utxo)
    _, dest_spk = dest_script("bad_dest_address.txt")
    tx, stack, desc = make_bad_tx(args.mode, utxo, info, priv, dest_spk)
    rawhex = tx.serialize().hex()

    # Both tips before, so that "nothing moved" can be checked on both nodes
    # rather than assumed for the one that was not watched.
    tip_before = cli("getbestblockhash", wallet=False)
    height_before = int(cli("getblockcount", wallet=False))
    tip_b_before = cli("getbestblockhash", wallet=False, node=NODE_B)
    height_b_before = int(cli("getblockcount", wallet=False, node=NODE_B))

    blockhex, blockhash, result = mine_block([rawhex])
    tip_after = cli("getbestblockhash", wallet=False)
    height_after = int(cli("getblockcount", wallet=False))

    rc_b, out_b, err_b = cli_try("submitblock", blockhex, wallet=False, node=NODE_B)
    tip_b_after = cli("getbestblockhash", wallet=False, node=NODE_B)
    height_b_after = int(cli("getblockcount", wallet=False, node=NODE_B))

    # The RPC call has to have worked. submitblock reports a rejected block in
    # its result string, not as an error, so a non-zero exit means the call
    # never reached node B or was refused by it. The "rejection" recorded below
    # would then be an artefact of the tooling rather than of consensus.
    assert rc_b == 0, f"node B submitblock RPC failed: {err_b or out_b}"

    # Both nodes refuse it, and refuse it for the script failure specifically.
    # `result is not None` alone accepts any of the dozen other reasons a block
    # can be rejected, including ones that would mean the test never reached
    # the P2MR rules at all.
    assert result is not None, "node A accepted a block with a malformed P2MR spend"
    assert result.startswith(BLOCK_REJECT), \
        f"node A rejected the block, but not for a script failure: {result}"
    assert out_b.startswith(BLOCK_REJECT), \
        f"node B rejected the block, but not for a script failure: {out_b}"

    # Neither node moved. A tip that changed while this ran means another block
    # arrived, so the readings no longer describe one event, and a tip that
    # changed to this block's hash would mean it was accepted after all.
    assert tip_after == tip_before, \
        f"node A tip moved on a rejected block: {tip_before} -> {tip_after}"
    assert tip_b_after == tip_b_before, \
        f"node B tip moved on a rejected block: {tip_b_before} -> {tip_b_after}"
    assert height_after == height_before, "node A height moved on a rejected block"
    assert height_b_after == height_b_before, "node B height moved on a rejected block"

    # Written only now, after every check above has held.
    os.makedirs(EVIDENCE, exist_ok=True)
    path = os.path.join(EVIDENCE, f"badblock_{args.mode}.hex")
    with open(path, "w") as f:
        f.write(blockhex + "\n")

    record("block_reject", mode=args.mode, description=desc,
           tx_txid=tx.txid_hex, tx_raw_hex=rawhex,
           block_hex_file=path, block_bytes=len(blockhex) // 2,
           block_height_attempted=height_before + 1,
           submitblock_result_nodeA=result,
           submitblock_result_nodeB=out_b,
           tip_before=tip_before, tip_after=tip_after,
           nodeB_tip_before=tip_b_before, nodeB_tip_after=tip_b_after)


def cmd_nonstandard(args):
    """An upgraded leaf version is consensus valid but does not relay.

    Leaf 'future' carries leaf version 0xfa. BIP-360, like BIP-341, leaves the
    script of an unknown leaf version unexecuted, so the spend succeeds as long
    as the control block reaches the root. Policy still refuses to relay it, so
    only a block can carry it: the mempool and the block layer deliberately
    disagree here, which is the opposite of the malformed cases above.
    """
    priv = bytes.fromhex(open(os.path.join(ARK0, "demo_key.hex")).read().strip())
    xonly, _ = compute_xonly_pubkey(priv)
    info = p2mr_construct([("future", CScript([OP_RETURN]), LEAF_VERSION_FUTURE),
                           ("csig", CScript([xonly, OP_CHECKSIG]))])
    addr = p2mr_address(info.merkle_root, HRP)
    spk_hex = info.scriptPubKey.hex()

    fund_txid = cli("sendtoaddress", addr, "0.50000000")
    _, fund_block, result = mine_block()
    assert result is None, f"funding block rejected: {result}"
    vout, value = find_vout(fund_txid, spk_hex)
    utxo = {"txid": fund_txid, "vout": vout, "value": value}

    leaf = info.leaves["future"]
    _, dest_spk = dest_script("bad_dest_address.txt")
    # No signature: the 0xfa leaf script is never executed.
    stack = [bytes(leaf.script), leaf.control_block]
    tx = build_spend(utxo, stack, dest_spk)
    rawhex = tx.serialize().hex()

    rc, out, err = cli_try("sendrawtransaction", rawhex)
    assert rc != 0, f"an upgraded leaf version relayed: {out}"

    tip_before = cli("getbestblockhash", wallet=False)
    blockhex, blockhash, block_result = mine_block([rawhex])
    assert block_result is None, \
        f"block with an upgraded leaf version rejected: {block_result}"
    confirmed = cli_json("getrawtransaction", tx.txid_hex, "1", wallet=False)
    seen_b = cli_json("getrawtransaction", tx.txid_hex, "1", wallet=False, node=NODE_B)

    record("nonstandard_but_valid",
           description="leaf version 0xfa: refused by the mempool, accepted in a block",
           address=addr, merkle_root=info.merkle_root.hex(),
           leaf_version=hex(LEAF_VERSION_FUTURE),
           control_block=leaf.control_block.hex(),
           fund_txid=fund_txid, fund_block=fund_block,
           txid=tx.txid_hex, raw_hex=rawhex,
           witness=[s.hex() for s in stack],
           sendrawtransaction_error=err,
           tip_before=tip_before, blockhash=blockhash,
           height=int(cli("getblockcount", wallet=False)),
           confirmations=confirmed["confirmations"],
           nodeB_confirmations=seen_b["confirmations"])


def cmd_chaintips(args):
    record("chaintips",
           nodeA=cli_json("getchaintips", wallet=False),
           nodeB=cli_json("getchaintips", wallet=False, node=NODE_B))


def cmd_status(args):
    a = cli_json("getblockchaininfo", wallet=False)
    b = cli_json("getblockchaininfo", wallet=False, node=NODE_B)
    dep = cli_json("getdeploymentinfo", wallet=False)
    record("status",
           nodeA={"blocks": a["blocks"], "bestblockhash": a["bestblockhash"],
                  "chain": a["chain"], "signet_challenge": a["signet_challenge"]},
           nodeB={"blocks": b["blocks"], "bestblockhash": b["bestblockhash"]},
           p2mr=dep["deployments"]["p2mr"],
           subversion=cli_json("getnetworkinfo", wallet=False)["subversion"])


def main():
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("mine")
    s.add_argument("count", type=int, nargs="?", default=1)
    s.add_argument("--quiet", action="store_true",
                   help="print one line per block, write nothing to the evidence log")
    s.set_defaults(fn=cmd_mine)
    s = sub.add_parser("addr")
    s.set_defaults(fn=cmd_addr)
    s = sub.add_parser("fund")
    s.add_argument("--amount", type=float, default=0.5)
    s.add_argument("--save", default="utxo1.json")
    s.set_defaults(fn=cmd_fund)
    s = sub.add_parser("spend")
    s.add_argument("--utxo", default="utxo1.json")
    s.set_defaults(fn=cmd_spend)
    s = sub.add_parser("badspend")
    s.add_argument("mode", choices=["lowbit", "wrongroot", "wrongkey"])
    s.add_argument("--utxo", default="utxo2.json")
    s.set_defaults(fn=cmd_badspend)
    s = sub.add_parser("badblock")
    s.add_argument("mode", choices=["lowbit", "wrongroot", "wrongkey"])
    s.add_argument("--utxo", default="utxo2.json")
    s.set_defaults(fn=cmd_badblock)
    s = sub.add_parser("nonstandard")
    s.set_defaults(fn=cmd_nonstandard)
    s = sub.add_parser("chaintips")
    s.set_defaults(fn=cmd_chaintips)
    s = sub.add_parser("status")
    s.set_defaults(fn=cmd_status)

    args = p.parse_args()
    return args.fn(args)


if __name__ == "__main__":
    sys.exit(main() or 0)

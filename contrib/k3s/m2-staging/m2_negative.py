#!/usr/bin/env python3
"""Submit blocks the M2 rule rejects to an M2 staging network, and check who takes them.

Runs inside the network's producer pod, which has the RPC credentials, the
producer's key and ark0.py:

    kubectl -n ark0-m2a exec -i deploy/m2-miner -- env PYTHONPATH=/opt/ark0/demo:/opt/ark0/bitcoin/test/functional \\
        python3 - a 300 < m2_negative.py
    kubectl -n ark0-m2b exec -i deploy/m2-miner -- env PYTHONPATH=/opt/ark0/demo:/opt/ark0/bitcoin/test/functional \\
        python3 - b < m2_negative.py

Every block is valid but for the M2 rule: on network a its classical signet
solution is signed by node A's wallet as usual, so an M1 node (without the
rule) takes it, while both nodes with the rule refuse it; on network b the M1
node takes anything. It prints the three answers of every case as JSON.
Network a's cases only mean something at or above the rule's height, which is
therefore given, and checked, on the command line.
"""
import copy
import hashlib
import json
import os
import sys

import ark0  # noqa: E402  (the producer; loads contrib/signet/miner)
from test_framework.script import CScriptOp, OP_0, OP_1NEGATE, OP_PUSHDATA4  # noqa: E402
from test_framework.signet_pq import (  # noqa: E402
    add_signet_pq_signature, signet_p2mr_solution, signet_pq_keygen,
    signet_pq_push, signet_pq_signature, witness_commitment_index,
)
from test_framework.blocktools import SIGNET_HEADER  # noqa: E402

NODES = {"nodea": ark0.NODE_A, "nodeb": ark0.NODE_B}
NODE_M1 = "/var/lib/ark0/nodeM1"
OTHER_SEED = hashlib.sha256(b"m2 staging negative cases: another ML-DSA-44 key, public, test only").digest()


def m1_client():
    """A bitcoin-cli data directory for the M1 node, next to the two ark0.py writes."""
    os.makedirs(NODE_M1, exist_ok=True)
    conf = open(os.path.join(ark0.NODE_A, "bitcoin.conf")).read()
    conf = conf.replace(f"rpcconnect={os.environ['ARK0_RPC_HOST_A']}", "rpcconnect=m2-nodem1")
    conf = conf.replace(f"rpcport={os.environ['ARK0_RPC_PORT_A']}", "rpcport=38452")
    with open(os.path.join(NODE_M1, "bitcoin.conf"), "w") as f:
        f.write(conf)
    os.chmod(os.path.join(NODE_M1, "bitcoin.conf"), 0o600)


def submit_all(block):
    blockhex = block.serialize().hex()
    answers = {}
    for name, datadir in list(NODES.items()) + [("nodem1", NODE_M1)]:
        answers[name] = ark0.cli_stdin("submitblock", blockhex, wallet=False, node=datadir) or "accepted"
    return answers


def template():
    return ark0.cli_json("getblocktemplate", '{"rules":["signet","segwit"]}', wallet=False)


def classical(block, tmpl):
    """Sign the classical solution with node A's wallet and grind, as mine_block does."""
    psbt = ark0.miner.generate_psbt(block, tmpl["signet_challenge"])
    processed = json.loads(ark0.cli_stdin("walletprocesspsbt", psbt))
    assert processed["complete"], processed
    decoded = ark0.miner.decode_challenge_psbt(processed["psbt"])
    return ark0.miner.finish_block(ark0.miner.get_block_from_psbt(decoded),
                                   ark0.miner.get_solution_from_psbt(decoded), ark0.GRIND)


def set_tail(block, tail):
    """The witness commitment output cut back to the commitment, then tail."""
    index = witness_commitment_index(block)
    block.vtx[0].vout[index].scriptPubKey = bytes(block.vtx[0].vout[index].scriptPubKey)[:38] + tail
    block.hashMerkleRoot = block.calc_merkle_root()
    return block


def network_a(rule_height):
    _, seckey = ark0.pq_key(ark0.PQ_BLOCK_SEED_FILE)
    _, other = signet_pq_keygen(OTHER_SEED)
    tmpl = template()
    # Below the rule's height the nodes with the rule would take these blocks too.
    assert tmpl["height"] >= rule_height, f"height {tmpl['height']} is below the rule's height {rule_height}"
    challenge = bytes.fromhex(tmpl["signet_challenge"])
    base = ark0.miner.new_block(tmpl, ark0.reward_spk())
    signed = copy.deepcopy(base)
    add_signet_pq_signature(signed, seckey, challenge)
    push = signet_pq_push(signet_pq_signature(signed))
    flipped = push[:100] + bytes([push[100] ^ 1]) + push[101:]
    wrong_key = copy.deepcopy(base)
    add_signet_pq_signature(wrong_key, other, challenge)
    cases = {
        "no PQ push": b"",
        "a flipped bit": CScriptOp.encode_op_pushdata(flipped),
        "another key": CScriptOp.encode_op_pushdata(signet_pq_push(signet_pq_signature(wrong_key))),
        "an opcode after the PQ push": CScriptOp.encode_op_pushdata(push) + bytes([OP_0]),
        "a non-push opcode instead of it": bytes([OP_1NEGATE]),
    }
    results = {"height": tmpl["height"]}
    for name, tail in cases.items():
        block = classical(set_tail(copy.deepcopy(base), tail), tmpl)
        results[name] = submit_all(block)
    return results


def network_b():
    pubkey, seckey = ark0.pq_key(ark0.PQ_CHALLENGE_SEED_FILE)
    other_pub, other_sec = signet_pq_keygen(OTHER_SEED)
    tmpl = template()
    base = ark0.miner.new_block(tmpl, ark0.reward_spk())
    solution = signet_p2mr_solution(copy.deepcopy(base), pubkey, seckey)
    flipped = solution[:10] + bytes([solution[10] ^ 1]) + solution[11:]
    push = CScriptOp.encode_op_pushdata(SIGNET_HEADER + solution)
    cases = {
        "a flipped bit": CScriptOp.encode_op_pushdata(SIGNET_HEADER + flipped),
        "another key's tree": CScriptOp.encode_op_pushdata(SIGNET_HEADER + signet_p2mr_solution(copy.deepcopy(base), other_pub, other_sec)),
        "the key's leaf, another signer": CScriptOp.encode_op_pushdata(SIGNET_HEADER + signet_p2mr_solution(copy.deepcopy(base), pubkey, other_sec)),
        "no witness": CScriptOp.encode_op_pushdata(SIGNET_HEADER + b"\x00\x00"),
        "a non-push opcode instead of it": bytes([OP_1NEGATE]),
        "an opcode after it": push + bytes([OP_0]),
        "a non-shortest encoding": bytes([OP_PUSHDATA4]) + (len(SIGNET_HEADER) + len(solution)).to_bytes(4, "little") + SIGNET_HEADER + solution,
    }
    results = {"height": tmpl["height"]}
    for name, tail in cases.items():
        block = set_tail(copy.deepcopy(base), tail)
        results[name] = submit_all(ark0.miner.finish_block(block, None, ark0.GRIND))
    return results


def main():
    net = sys.argv[1] if len(sys.argv) > 1 else ""
    if net not in ("a", "b") or (net == "a" and len(sys.argv) != 3):
        raise SystemExit("usage: m2_negative.py a <rule height> | b")
    m1_client()
    print(json.dumps(network_a(int(sys.argv[2])) if net == "a" else network_b(), indent=1))


if __name__ == "__main__":
    main()

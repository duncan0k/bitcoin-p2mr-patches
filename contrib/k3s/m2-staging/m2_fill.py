#!/usr/bin/env python3
"""Fill the mempool of an M2 staging network, so that its next blocks are full.

Runs inside the network's producer pod, like m2_negative.py:

    kubectl -n ark0-m2a exec -i deploy/m2-miner -- env PYTHONPATH=/opt/ark0/demo:/opt/ark0/bitcoin/test/functional \\
        python3 - 12 < m2_fill.py

The producer pays every coinbase to P2WSH(OP_TRUE) (render.py), so anyone can
spend a mature one. Each spend here carries an OP_RETURN of 98,000 bytes and
sends the rest back to P2WSH(OP_TRUE): twelve are about 1.18 million vbytes,
more than a block holds. The blocks that follow show whether the producer's
reserved weight leaves room for its signature (their weight is printed by
`getblock <hash> 1`).
"""
import hashlib
import json
import sys

import ark0  # noqa: E402
from test_framework.messages import COutPoint, CTransaction, CTxIn, CTxInWitness, CTxOut  # noqa: E402
from test_framework.script import CScript, OP_0, OP_RETURN, OP_TRUE  # noqa: E402

WITNESS_SCRIPT = bytes(CScript([OP_TRUE]))
P2WSH = bytes(CScript([OP_0, hashlib.sha256(WITNESS_SCRIPT).digest()]))
DATA = 98_000
FEE = 300_000  # sats, about 3 sat/vB


def mature_coinbases(count):
    tip = int(ark0.cli("getblockcount", wallet=False))
    found = []
    for height in range(1, tip - 100):
        blockhash = ark0.cli("getblockhash", str(height), wallet=False)
        coinbase = ark0.cli_json("getblock", blockhash, "2", wallet=False)["tx"][0]
        for out in coinbase["vout"]:
            if out["scriptPubKey"]["hex"] != P2WSH.hex():
                continue
            # Unspent, mempool included, so that a second run does not reuse a coin.
            if ark0.cli("gettxout", coinbase["txid"], str(out["n"]), "true", wallet=False):
                found.append((coinbase["txid"], out["n"], round(out["value"] * 100_000_000)))
        if len(found) >= count:
            break
    return found[:count]


def spend(txid, n, value):
    tx = CTransaction()
    tx.version = 2
    tx.vin = [CTxIn(COutPoint(int(txid, 16), n), b"", 0xfffffffd)]
    tx.vout = [CTxOut(0, bytes(CScript([OP_RETURN, bytes(DATA)]))), CTxOut(value - FEE, P2WSH)]
    wit = CTxInWitness()
    wit.scriptWitness.stack = [WITNESS_SCRIPT]
    tx.wit.vtxinwit = [wit]
    return tx


def main():
    count = int(sys.argv[1]) if len(sys.argv) > 1 else 12
    coins = mature_coinbases(count)
    assert len(coins) == count, f"only {len(coins)} mature P2WSH(OP_TRUE) coinbases"
    sent = [ark0.cli_stdin("sendrawtransaction", spend(*coin).serialize().hex(), wallet=False) for coin in coins]
    info = ark0.cli_json("getmempoolinfo", wallet=False)
    print(json.dumps({"sent": len(sent), "mempool_bytes": info["bytes"], "mempool_txs": info["size"],
                      "tip": int(ark0.cli("getblockcount", wallet=False))}, indent=1))


if __name__ == "__main__":
    main()

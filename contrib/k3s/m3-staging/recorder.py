#!/usr/bin/env python3
"""Soak recorder of the M3 staging network (README.md in this directory).

Runs in the pod m3-recorder, which render.py makes from the producer image: every
RECORD_INTERVAL seconds it prints one JSON line per node on stdout with its height,
tip, mempool, connections and warnings, and on the M3 nodes what gettripwireinfo says
of the rules and the two tripwire outputs. It only reads. The RPC credentials come
from the network's Secret as a file that bitcoin-cli reads; nothing here prints them.

    RECORD_NODES      name=host:port ..., the nodes to ask
    RECORD_M3_NODES   the names among them that have the tripwire rules
    RECORD_INTERVAL   seconds between rounds (default 1800)
"""
import json
import os
import subprocess
import time

CREDENTIALS = "/secrets/rpccredentials.conf"
NODES = [entry.split("=", 1) for entry in os.environ["RECORD_NODES"].split()]
M3_NODES = set(os.environ.get("RECORD_M3_NODES", "").split())
INTERVAL = int(os.environ.get("RECORD_INTERVAL", "1800"))


def cli(hostport, *args):
    host, port = hostport.rsplit(":", 1)
    p = subprocess.run(["bitcoin-cli", f"-conf={CREDENTIALS}", "-signet", f"-rpcconnect={host}", f"-rpcport={port}", *args],
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=120)
    if p.returncode != 0:
        # The first line says what went wrong; bitcoin-cli ends with general advice.
        lines = p.stderr.strip().splitlines()
        raise RuntimeError(f"{args[0]}: {lines[0] if lines else f'exit {p.returncode}'}")
    return p.stdout.strip()


def record(name, hostport, now):
    entry = {"time": now, "node": name}
    try:
        chain = json.loads(cli(hostport, "getblockchaininfo"))
        pool = json.loads(cli(hostport, "getmempoolinfo"))
        entry.update(height=chain["blocks"], tip=chain["bestblockhash"], warnings=chain["warnings"],
                     mempool={"txs": pool["size"], "bytes": pool["bytes"]},
                     connections=int(cli(hostport, "getconnectioncount")))
        if name in M3_NODES:
            info = json.loads(cli(hostport, "gettripwireinfo"))
            entry["tripwire"] = {"height": info["height"], "tip_height": info["tip_height"],
                                 "ec_disabled_next_block": info["ec_disabled_next_block"],
                                 "states": {t["outpoint"]: t["state"] for t in info["tripwires"]}}
    except (RuntimeError, subprocess.TimeoutExpired, ValueError, KeyError) as e:
        entry["error"] = str(e)
    print(json.dumps(entry, sort_keys=True), flush=True)


def main():
    # A new pod is let through the network policy only a moment after it starts.
    time.sleep(10)
    while True:
        now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        for name, hostport in NODES:
            record(name, hostport, now)
        time.sleep(INTERVAL)


if __name__ == "__main__":
    main()

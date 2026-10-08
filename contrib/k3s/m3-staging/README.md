# M3 staging network

A short-lived signet that tries out M3 (`doc/p2mr-tripwire.md` in the series)
before it goes anywhere near Ark-0: P2MR leaves of leaf version 0xe0, whose
elliptic curve checks fail from the block after a tripwire output goes. It is
a test network and nothing else: every key in it is derived from a public
string in `render.py` and `m3_drill.py`.

| | `ark0-m3` |
|---|---|
| rules | `-signetmldsa44=<H1>` (default 50) from the start; `-signettripwire=<H3>@<NUMS outpoint>,<drill outpoint>` once both outputs exist (`m3_arm.sh`) |
| challenge | 1-of-1 bare multisig, as on Ark-0, signed by node A's wallet |
| nodes | `nodea`, `nodeb` (M3 build, with the rules), `nodem2` (the M2 build Ark-0 runs, without them) |
| producer | `ark0.py` from this repository, one block every 30 s, without `-signetpqblock`: M3 does not depend on M2's rule |
| recorder | `recorder.py`, the state of the three nodes every half hour, on its log (*Soak* below) |
| tripwire outputs | two P2MR outputs of two 0xc0 leaves, `<P> OP_CHECKSIG` and `OP_RETURN`, as on Ark-0: `P` is BIP-341's NUMS point for one, which nobody can spend, and a key derived from a public string for the drill one |

The two M3 nodes whitelist the pod network as `noban`, so that the M2 node is
not discouraged when it relays a block they reject. A NetworkPolicy lets only
pods of the same namespace reach the nodes, and the network's challenge gives
it its own message start, so it cannot talk to Ark-0.

## Images

- node: a build of the M3 series, `master m05 spacing m1 m2 m3`, as
  `p2mr-node:v31.1-p2mr-m3-dev-<head12>`;
- M2 node: the image Ark-0 runs (`../ark0/kustomization.yaml`);
- producer: `p2mr-miner:v31.1-p2mr-m3-dev-<head12>`, staged from that M3 build,
  so that it carries the M3 `test_framework` (`p2mr_tripwire.py`,
  `p2mr_mldsa44.py`) that `m3_drill.py` imports.

`<head12>` is the start of the series head, the commit of the Core tree the
patches were exported from. Both are staged in `/work/m3-imgctx`, apart from
`/work/patches`, `/work/src`, `/work/out/bin` and `/work/imgctx`, which Ark-0's
release builds use: a run of the whole series by `../m3-dev/m3-build.sh` with
`M3_IMGCTX=1` publishes its binaries and their `PROVENANCE.txt` there once every
check has passed, and then, before any other run:

    KUBECTL="sudo k3s kubectl" ./images.sh <run id>

stages the producer's context and builds both images with `../build-image.sh`
(`CONTEXT_ROOT=m3-imgctx`), which prints each tag and digest.

## Bringing it up

The tripwire outputs are transactions on this network, so their outpoints are
only known once it runs, and every node has to run the same rules: two nodes
with different tripwire outputs under one challenge follow different chains.
The nodes therefore start with `-signetmldsa44` alone, the outputs are created
and buried, and only then do both M3 nodes get `-signettripwire`, while the tip
is still far below `H3`. Before `H3` the tripwire rules change nothing, so the
restart loses nothing.

1. Render and apply. `render.py` needs a Core tree with the M3 series for
   `test_framework`; run it wherever that tree is and copy the output to the
   cluster:

       ./render.py --core ~/bitcoin-p2mr-m3 --node-image <M3 node> --m2-image <M2 node> --miner-image <M3 miner> > m3.yaml
       KUBECTL="sudo k3s kubectl" ./up.sh m3.yaml

   `up.sh` makes the namespace and random RPC credentials (through stdin,
   never on a command line), applies the manifest, waits for the nodes and
   gives node A the wallet with the challenge key.

2. Wait for height 110, where the first coinbases have matured, and create the
   tripwire outputs (the command line is under *Steps* below):

       ... python3 - tripwires < m3_drill.py

   It waits for 6 confirmations and prints both outpoints, the height that
   created them, a suggested `H3` 240 blocks (two hours) above the tip, and the
   `m3_arm.sh` command line.

3. Arm the M3 nodes, with that command line:

       KUBECTL="sudo k3s kubectl" ./m3_arm.sh <H3> <NUMS outpoint>,<drill outpoint>

   It refuses unless both outputs are unspent with 6 confirmations and the tip
   is at least 120 blocks below `H3` (`MIN_CONF`, `MARGIN`), adds the line to
   `nodea.conf` and `nodeb.conf`, restarts both nodes, and checks that each logs
   `Signet P2MR tripwire rules from height <H3> ...` and reports both outputs
   unspent through `gettripwireinfo`. The M2 node keeps its configuration.

4. Keep the value: `render.py --tripwire <H3>@<outpoints>` renders the armed
   manifest, so that applying it again changes nothing. A network that would
   need other values is a new network: take this one down and start again.

## Steps on a running network

`m3_drill.py` runs inside the producer pod, which has the RPC credentials,
node A's wallet for the signet solution, and `ark0.py`:

    kubectl -n ark0-m3 exec -i deploy/m3-miner -- env PYTHONPATH=/opt/ark0/demo:/opt/ark0/bitcoin/test/functional \
        python3 - <step> [options] < m3_drill.py

Each step checks what it is there to show, stops at the first difference and
prints its results as JSON. Coins come from the coinbases, which pay
P2WSH(`OP_TRUE`). Once the chain is at `H3` or above, in this order:

| step | what it shows |
|---|---|
| `fund --count 20` | outputs of template S3 (a Schnorr leaf and an ML-DSA-44 leaf, both 0xe0, at depth one), funded only once the rules apply |
| `paths` | an S3 output spent through its Schnorr leaf and another through its ML-DSA-44 leaf, both confirmed on all three nodes |
| `negative` | a block with an invalid ML-DSA-44 signature in a 0xe0 leaf, and one with an invalid Schnorr signature: both M3 nodes refuse each (`Invalid ML-DSA-44 signature`, `Invalid Schnorr signature`), the M2 node, for which 0xe0 is an unknown leaf version, takes it and goes back to the producer's chain once that is longer. Node A passes a block on before checking its scripts, so node B and the M2 node are cut off while the block is submitted to each in turn, and each answers for itself; `getchaintips` shows it invalid on both M3 nodes |
| `emptypool` | on node B, cut off from its peers while it runs: block A1 confirms a parent, whose child, with a relative lock of one block, and a Schnorr spend of an S3 output wait in the mempool; B1 is A1 with another nonce (the signet solution does not sign it), so the same transactions, coinbase included; B2 on B1 spends the drill tripwire. Connecting B1 leaves nothing of A1 to put back, and a node that took B2 for a plain connect because of that would stop on the child's lock points, which still name A1. Node B has to switch to B2 and keep the child while the Schnorr spend goes; then B1 is invalidated, node B goes back on the network (as `rejoin` does), and the drill tripwire is unspent there as before. Run it before `trigger` |
| `fund --count 600` | the S3 outputs `recheck` spends, made before the trigger so that they stay on the chain across its reorganizations; as every `fund`, at most 60 to a transaction (`FUND_BATCH`), so that a funding transaction that does go back to the mempool keeps its cluster within the limits, 64 transactions and 101 kvB |
| `trigger` | right after a block of the producer, Schnorr and ML-DSA-44 spends of S3 outputs (one ML-DSA-44 spend with a Schnorr child) are sent to both M3 nodes, and block X, which holds only the spend of the drill tripwire, is submitted to all three. From X+1: the drill tripwire is `missing` and `ec_disabled_next_block` true on both M3 nodes; both mempools drop the Schnorr spends and the child and keep the ML-DSA-44 spends, which the producer then mines; a new Schnorr spend is refused by the mempool (`Elliptic curve signature checks are disabled in this leaf`) and in a block by both M3 nodes, while the M2 node takes that block. Block X is kept in the pod for the next steps; it is printed too |
| `reorg` | on node B, soon after the trigger: `invalidateblock X` brings the drill tripwire back and a Schnorr spend is accepted again (`testmempoolaccept`); `reconsiderblock X` puts node B back on the tip with the checks disabled and the spend refused |
| `recheck` | on node B, cut off from its peers while it runs, at least one block after X: `invalidateblock X`, a block of node B's own on X's parent, `reconsiderblock X`, which disconnects that block and connects X and what follows in one step, so that MaybeUpdateMempoolForReorg ends with the checks disabled and checks every spend of a 0xe0 leaf in the mempool again, holding `cs_main`. Once with the mempool as it is, and once with 500 ML-DSA-44 spends, twelve 98,000-byte `OP_RETURN` fillers (as `../m2-staging/m2_fill.py`) and 50 Schnorr spends sent below X, which the check removes. Its coins come from funding transactions below X, or from one above X none of whose outputs paying S3 is spent, at most 60 of those, so that no cluster goes over the limits when X is invalidated. Across X it needs X at most 10 blocks deep, since `invalidateblock` puts back in the mempool only what the first ten blocks it disconnects held: run it right after `trigger`. Once X is deeper, `--at-tip --schnorr 0` reorganizes the tip's last two blocks instead, which also ends with the checks disabled and checks the same spends again, only with no Schnorr spends to remove, since above X the mempool refuses them. It prints the time of each `reconsiderblock` call, the bound on how long `cs_main` was held, and the mempool before and after; node B logs the `bench` category during the call. At the end node B goes back on the network as `rejoin` does, and the producer mines its spends |
| `rejoin` | node B back on the network, with the transactions its mempool took while cut off sent on to node A, parents first: a node does not announce those once it reconnects. `emptypool` and `recheck` end with it; run it on its own after one of them stopped half way |
| `status` | for each node: tip, connections, mempool, `gettripwireinfo` and the three deployments on the M3 nodes |
| `times` | height, time, weight and transaction count of every block, one line each |

Two things are left to the logs, which the pod cannot read:

    sudo k3s kubectl -n ark0-m3 logs nodea-0 | grep 'P2MR tripwire'
    sudo k3s kubectl -n ark0-m3 logs nodeb-0 | grep -E 'P2MR tripwire|bench'

Each M3 node logs the rules and their state at startup, the state when the
chain reaches `H3`, and `P2MR tripwire <outpoint> spent by <txid> in block X
(height <x>): elliptic curve checks in leaf version 0xe0 are disabled from
height <x+1>` once X is in its active chain, and only then. Node B also logs
the spend in B2 during `emptypool`, a block it gives up again right after.

## Soak

Leave the network running for at least 24 hours from `H3`, with the trigger
early in that time. The recorder, a pod of the network made from the producer
image (`m3-recorder`, at most a tenth of a CPU and 128 MiB), asks the three
nodes every half hour and prints one JSON line for each: height, tip, mempool,
connections, warnings, and on the two M3 nodes the rules' height, the state of
both tripwire outputs and `ec_disabled_next_block`. It reads the RPC
credentials from the network's Secret, as the producer does, and prints none of
them. Nothing of the soak runs on the host:

    sudo k3s kubectl -n ark0-m3 logs deploy/m3-recorder        # the records so far
    sudo k3s kubectl -n ark0-m3 delete deployment,configmap m3-recorder   # stop it

Look at its records once an hour or so, and at the rest when looking:

- the records: the three nodes on the same tip (the M2 node trails only right
  after a `negative` or `trigger` block), the same tripwire state on both M3
  nodes, `ec_disabled_next_block` true after X and nothing else changed, no
  warnings;
- `sudo k3s kubectl -n ark0-m3 get pods`: no restarts. A node that stopped
  shows it there, and its previous log (`logs --previous`) says why;
- the producer's log: one `height <n> <hash>` line every 30 s, no `block
  production failed`;
- the node logs: no `ERROR`, no assertion, and the `P2MR tripwire` lines as
  above;
- `sudo k3s kubectl top pods -n ark0-m3`: memory flat;
- `times`: block times about 30 s apart, weights below the limit, including
  the blocks right after `recheck`, which carry its spends.

It passes when the steps above have all passed, which covers the design's
acceptance for the test network (an invalid ML-DSA-44 signature in a 0xe0 leaf
refused in a block, both paths of the hybrid template spendable, one full
trigger with a reorganization across it and the mempool cleaned), and the soak
has shown none of the problems above. Keep the outputs of the steps.

## Taking it down

Keep the records first: the step outputs, the recorder's log, `times`, and the
`P2MR tripwire` lines of both M3 nodes. Then taking the network down deletes
its namespace and everything in it, its volumes included:

    sudo k3s kubectl delete namespace ark0-m3

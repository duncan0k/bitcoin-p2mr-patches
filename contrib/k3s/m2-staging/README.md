# M2 staging networks

Two short-lived signets that try out the two M2 candidates before one is
chosen for Ark-0. They are test networks and nothing else: every key in them
is derived from a public string in `render.py`.

| | `ark0-m2a` | `ark0-m2b` |
|---|---|---|
| rule | `-signetpqblock` (`doc/signet-pqblock.md`) | `-signetpqchallenge` (`doc/signet-pqchallenge.md`) |
| challenge | 1-of-1 bare multisig, as on Ark-0, signed by node A's wallet | derived from an ML-DSA-44 key |
| rule height | 300; the producer adds the PQ push from 200 | every block from height 1 |
| nodes | `nodea`, `nodeb` (M2 build, with the rule), `nodem1` (the M1 build, without it) | the same |
| producer | `ark0.py` from this repository, one block every 30 s | the same |

The nodes with the rule whitelist the pod network as `noban`, so that the M1
node is not discouraged when it relays a block the rule rejects. A
NetworkPolicy lets only pods of the same namespace reach them, and each
network's challenge gives it its own message start, so neither can talk to
Ark-0.

## Images

- node: a build of `master m05 spacing m1 m2a m2b` (`../run-build.sh`, then
  `../build-image.sh node`);
- M1 node: the image Ark-0 runs (`../ark0/kustomization.yaml`);
- producer: `../stage-miner.sh` after that build, then `../build-image.sh miner`,
  so that it carries the M2 `test_framework` (`signet_pq.py`) and this
  repository's `ark0.py`.

## Bringing one up

`render.py` needs a Core tree with the M2 series for `test_framework`; run it
wherever that tree is, and copy the output to the cluster:

    ./render.py a --core ~/bitcoin-p2mr-m2 --node-image <node> --m1-image <m1> --miner-image <miner> > m2a.yaml
    KUBECTL="sudo k3s kubectl" ./up.sh a m2a.yaml

`up.sh` makes the namespace and random RPC credentials (through stdin, never
on a command line), applies the manifest, waits for the nodes and, on network
a, gives node A the wallet with the classical key.

Taking one down deletes its namespace and everything in it:

    sudo k3s kubectl delete namespace ark0-m2a

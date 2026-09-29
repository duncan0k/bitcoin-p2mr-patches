#!/usr/bin/env python3
"""Render the manifests of one M2 staging network (README.md in this directory).

    ./render.py a --core <Core tree with the M2 series> \\
        --node-image p2mr-node:<M2 tag> --m1-image p2mr-node:<M1 tag> \\
        --miner-image p2mr-miner:<M2 tag> > m2a.yaml

Network a runs -signetpqblock on a 1-of-1 bare multisig challenge, the way
Ark-0 would; network b runs -signetpqchallenge. Each has two nodes with the
rule, one M1 node without it, and a block producer.

Every key here is derived from a public string, so anyone can recompute it:
these networks are for testing and nothing else. The RPC credentials are not
in the output; up.sh makes them in the cluster.
"""
import argparse
import hashlib
import json
import os
import sys

NETS = {
    "a": {"namespace": "ark0-m2a", "reserved_weight": 16000},
    "b": {"namespace": "ark0-m2b", "reserved_weight": 24000},
}
NODES = {  # name: (p2p port, rpc port)
    "nodea": (38433, 38432),
    "nodeb": (38443, 38442),
    "nodem1": (38453, 38452),
}
LABEL = "ark0-m2"


def derive(text):
    return hashlib.sha256(text.encode()).digest()


def keys(net):
    """The network's challenge lines for nodes with and without the rule, and its signing material."""
    from test_framework.descriptors import descsum_create
    from test_framework.key import ECKey
    from test_framework.script import CScript, OP_1, OP_CHECKMULTISIG
    from test_framework.signet_pq import signet_pq_challenge, signet_pq_keygen
    from test_framework.wallet_util import bytes_to_wif

    seed = derive(f"ark0-m2{net} staging ML-DSA-44 key, public, test only")
    pubkey, _ = signet_pq_keygen(seed)
    out = {"seed": seed.hex(), "key_sha256": hashlib.sha256(pubkey).hexdigest()}
    if net == "a":
        classical = derive("ark0-m2a staging classical key, public, test only")
        key = ECKey()
        key.set(classical, True)
        challenge = CScript([OP_1, key.get_pubkey().get_bytes(), OP_1, OP_CHECKMULTISIG]).hex()
        out["rule"] = [f"signetchallenge={challenge}", f"signetpqblock={pubkey.hex()}@{{height}}"]
        out["plain"] = [f"signetchallenge={challenge}"]
        desc = descsum_create(f"multi(1,{bytes_to_wif(classical)})")
        out["import"] = json.dumps([{"desc": desc, "timestamp": "now"}])
    else:
        out["rule"] = [f"signetpqchallenge={pubkey.hex()}"]
        out["plain"] = [f"signetchallenge={signet_pq_challenge(pubkey).hex()}"]
    return out


def reward_address():
    """P2WSH of OP_TRUE: anyone can spend the coinbase outputs, which is what filling blocks needs."""
    from test_framework.segwit_addr import encode_segwit_address
    return encode_segwit_address("tb", 0, hashlib.sha256(b"\x51").digest())


def conf(net, name, lines, reserved_weight):
    p2p, rpc = NODES[name]
    peers = [f"addnode=m2-{other}:{NODES[other][0]}" for other in NODES if other != name]
    body = [
        f"# M2 staging network {net}, {name}. Test keys only.",
        "signet=1", "server=1", "txindex=1", "dnsseed=0", "listenonion=0", "debug=validation",
        "", "[signet]",
        *lines,
        "signetpowtargetspacing=90@0",
        f"port={p2p}", f"rpcport={rpc}", "listen=1", "discover=0",
        f"bind=0.0.0.0:{p2p}", "rpcbind=0.0.0.0", "rpcallowip=10.42.0.0/16",
        *peers,
    ]
    if name != "nodem1":
        # A node with the rule would discourage the M1 node for relaying a
        # block the rule rejects; the M1 node is here to be watched, not banned.
        body += ["whitelist=noban@10.42.0.0/16", f"blockreservedweight={reserved_weight}"]
    body.append("fallbackfee=0.0001")
    return "\n".join(body) + "\n"


def indent(text, n):
    pad = " " * n
    return "".join(pad + line if line.strip() else line for line in text.splitlines(True))


def labels(component, n):
    return indent(f"app.kubernetes.io/part-of: {LABEL}\napp.kubernetes.io/component: {component}\n", n)


def statefulset(ns, name, image):
    p2p, rpc = NODES[name]
    return f"""apiVersion: v1
kind: Service
metadata:
  name: m2-{name}
  namespace: {ns}
spec:
  clusterIP: None
  selector:
{labels(name, 4)}  ports:
    - {{ name: p2p, port: {p2p}, targetPort: {p2p}, protocol: TCP }}
    - {{ name: rpc, port: {rpc}, targetPort: {rpc}, protocol: TCP }}
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: {name}
  namespace: {ns}
  labels:
{labels(name, 4)}spec:
  serviceName: m2-{name}
  replicas: 1
  selector:
    matchLabels:
{labels(name, 6)}  template:
    metadata:
      labels:
{labels(name, 8)}    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 10000
        runAsGroup: 10000
        fsGroup: 10000
        fsGroupChangePolicy: OnRootMismatch
        seccompProfile:
          type: RuntimeDefault
      terminationGracePeriodSeconds: 180
      containers:
        - name: bitcoind
          image: {image}
          imagePullPolicy: IfNotPresent
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
          args:
            - -conf=/config/{name}.conf
            - -datadir=/data
            - -printtoconsole
            - -rpcauth=$(M2_RPCAUTH)
          env:
            - name: M2_RPCAUTH
              valueFrom:
                secretKeyRef:
                  name: m2-rpc
                  key: rpcauth
          ports:
            - {{ name: p2p, containerPort: {p2p} }}
            - {{ name: rpc, containerPort: {rpc} }}
          startupProbe:
            exec:
              command: ["bitcoin-cli", "-conf=/config/{name}.conf", "-datadir=/data", "uptime"]
            periodSeconds: 10
            failureThreshold: 60
          resources:
            requests: {{ cpu: "100m", memory: "512Mi" }}
            limits: {{ cpu: "2", memory: "2Gi" }}
          volumeMounts:
            - {{ name: data, mountPath: /data }}
            - {{ name: config, mountPath: /config, readOnly: true }}
      volumes:
        - name: config
          configMap:
            name: m2-conf
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: ["ReadWriteOnce"]
        storageClassName: local-path
        resources:
          requests:
            storage: 5Gi
"""


def miner(ns, image, env):
    env_lines = "".join(f'            - {{ name: {k}, value: "{v}" }}\n' for k, v in env)
    return f"""apiVersion: apps/v1
kind: Deployment
metadata:
  name: m2-miner
  namespace: {ns}
  labels:
{labels("miner", 4)}spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
{labels("miner", 6)}  template:
    metadata:
      labels:
{labels("miner", 8)}    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 10000
        runAsGroup: 10000
        fsGroup: 10000
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: miner
          image: {image}
          imagePullPolicy: IfNotPresent
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
          env:
{env_lines}          resources:
            requests: {{ cpu: "250m", memory: "256Mi" }}
            limits: {{ cpu: "1", memory: "1Gi" }}
          volumeMounts:
            - {{ name: home, mountPath: /var/lib/ark0 }}
            - {{ name: credentials, mountPath: /secrets, readOnly: true }}
            - {{ name: config, mountPath: /config, readOnly: true }}
            - {{ name: pq, mountPath: /pq, readOnly: true }}
      volumes:
        - name: home
          emptyDir: {{}}
        - name: credentials
          secret:
            secretName: m2-rpc
            items:
              - {{ key: rpccredentials.conf, path: rpccredentials.conf }}
            defaultMode: 0440
        - name: config
          configMap:
            name: m2-conf
            items:
              - {{ key: reward_address.txt, path: reward_address.txt }}
        - name: pq
          secret:
            secretName: m2-pq
            defaultMode: 0440
"""


def render(args):
    net = NETS[args.net]
    ns = net["namespace"]
    k = keys(args.net)
    rule = [line.format(height=args.height) for line in k["rule"]]
    confs = {
        "nodea": conf(args.net, "nodea", rule, net["reserved_weight"]),
        "nodeb": conf(args.net, "nodeb", rule, net["reserved_weight"]),
        "nodem1": conf(args.net, "nodem1", k["plain"], net["reserved_weight"]),
    }
    data = "".join(f"  {name}.conf: |\n{indent(text, 4)}" for name, text in confs.items())
    data += f"  reward_address.txt: |\n    {reward_address()}\n"
    env = [
        ("ARK0_BLOCK_INTERVAL", args.interval), ("ARK0_WALLET", "ark0"),
        ("ARK0_RPC_HOST_A", "m2-nodea"), ("ARK0_RPC_PORT_A", NODES["nodea"][1]),
        ("ARK0_RPC_HOST_B", "m2-nodeb"), ("ARK0_RPC_PORT_B", NODES["nodeb"][1]),
        ("ARK0_CREDENTIALS", "/secrets/rpccredentials.conf"),
        ("ARK0_REWARD_ADDRESS_FILE", "/config/reward_address.txt"),
    ]
    if args.net == "a":
        env += [("ARK0_PQ_BLOCK_SEED_FILE", "/pq/seed"), ("ARK0_PQ_BLOCK_FROM", args.pq_from),
                ("ARK0_PQ_BLOCK_KEY_SHA256", k["key_sha256"])]
    else:
        env += [("ARK0_PQ_CHALLENGE_SEED_FILE", "/pq/seed")]

    docs = [f"""apiVersion: v1
kind: Namespace
metadata:
  name: {ns}
  labels:
    app.kubernetes.io/part-of: {LABEL}
""", f"""apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: m2-same-namespace-only
  namespace: {ns}
spec:
  podSelector: {{}}
  policyTypes: ["Ingress"]
  ingress:
    - from:
        - podSelector: {{}}
""", f"""apiVersion: v1
kind: ConfigMap
metadata:
  name: m2-conf
  namespace: {ns}
data:
{data}""", f"""# Derived from a public string: a test key, not a secret. Kept in a Secret so
# that the producer reads it the way a real key would be read.
apiVersion: v1
kind: Secret
metadata:
  name: m2-pq
  namespace: {ns}
stringData:
  seed: "{k['seed']}"
"""]
    if args.net == "a":
        docs.append(f"""# The classical challenge key, as a descriptor for node A's wallet (up.sh).
# Derived from a public string: a test key, not a secret.
apiVersion: v1
kind: Secret
metadata:
  name: m2-signer
  namespace: {ns}
stringData:
  import.json: '{k["import"]}'
""")
    docs += [statefulset(ns, "nodea", args.node_image), statefulset(ns, "nodeb", args.node_image),
             statefulset(ns, "nodem1", args.m1_image), miner(ns, args.miner_image, env)]
    sys.stdout.write("---\n".join(docs))
    print(f"{ns}: rule key SHA256 {k['key_sha256']}", file=sys.stderr)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("net", choices=sorted(NETS))
    p.add_argument("--core", required=True, help="Core source tree carrying the M2 series (for test_framework)")
    p.add_argument("--node-image", required=True)
    p.add_argument("--m1-image", required=True)
    p.add_argument("--miner-image", required=True)
    p.add_argument("--interval", type=int, default=30, help="seconds between blocks (default 30)")
    p.add_argument("--height", type=int, default=300, help="network a: the rule's height (default 300)")
    p.add_argument("--pq-from", type=int, default=200, help="network a: first height with the PQ push (default 200)")
    args = p.parse_args()
    sys.path.insert(0, os.path.join(args.core, "test", "functional"))
    render(args)


if __name__ == "__main__":
    main()

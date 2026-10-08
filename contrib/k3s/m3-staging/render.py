#!/usr/bin/env python3
"""Render the manifests of the M3 staging network (README.md in this directory).

    ./render.py --core <Core tree with the M3 series> \\
        --node-image p2mr-node:<M3 tag> --m2-image p2mr-node:<M2 tag> \\
        --miner-image p2mr-miner:<M3 tag> > m3.yaml

The network, ark0-m3, has two nodes with the M3 rules, one node of the M2 build
Ark-0 runs, without them, a block producer, and a recorder that prints the state
of the three nodes every half hour (recorder.py). Its nodes start with
-signetmldsa44 alone: the tripwire outputs only exist once the chain runs, and
m3_arm.sh adds -signettripwire once they do. To render the manifest of a network
that has been armed, so that applying it again changes nothing, give the same
value with --tripwire.

Every key here is derived from a public string, so anyone can recompute it:
this network is for testing and nothing else. The RPC credentials are not in
the output; up.sh makes them in the cluster.
"""
import argparse
import hashlib
import json
import os
import re
import sys

NAMESPACE = "ark0-m3"
NODES = {  # name: (p2p port, rpc port)
    "nodea": (38463, 38462),
    "nodeb": (38473, 38472),
    "nodem2": (38483, 38482),
}
RULE_NODES = ("nodea", "nodeb")
LABEL = "ark0-m3"


def derive(text):
    return hashlib.sha256(text.encode()).digest()


def keys():
    """The challenge, a 1-of-1 bare multisig as on Ark-0, and the descriptor that signs it."""
    from test_framework.descriptors import descsum_create
    from test_framework.key import ECKey
    from test_framework.script import CScript, OP_1, OP_CHECKMULTISIG
    from test_framework.wallet_util import bytes_to_wif

    classical = derive("ark0-m3 staging classical key, public, test only")
    key = ECKey()
    key.set(classical, True)
    challenge = CScript([OP_1, key.get_pubkey().get_bytes(), OP_1, OP_CHECKMULTISIG]).hex()
    desc = descsum_create(f"multi(1,{bytes_to_wif(classical)})")
    return challenge, json.dumps([{"desc": desc, "timestamp": "now"}])


def reward_address():
    """P2WSH of OP_TRUE: anyone can spend the coinbase outputs, which is what the tests spend."""
    from test_framework.segwit_addr import encode_segwit_address
    return encode_segwit_address("tb", 0, hashlib.sha256(b"\x51").digest())


def conf(name, lines):
    p2p, rpc = NODES[name]
    peers = [f"addnode=m3-{other}:{NODES[other][0]}" for other in NODES if other != name]
    body = [
        f"# M3 staging network, {name}. Test keys only.",
        "signet=1", "server=1", "txindex=1", "dnsseed=0", "listenonion=0", "debug=validation",
        "", "[signet]",
        *lines,
        "signetpowtargetspacing=90@0",
        f"port={p2p}", f"rpcport={rpc}", "listen=1", "discover=0",
        f"bind=0.0.0.0:{p2p}", "rpcbind=0.0.0.0", "rpcallowip=10.42.0.0/16",
        *peers,
    ]
    if name in RULE_NODES:
        # A node with the rules would discourage the M2 node for relaying a
        # block they reject; the M2 node is here to be watched, not banned.
        body.append("whitelist=noban@10.42.0.0/16")
    body.append("fallbackfee=0.0001")
    return "\n".join(body) + "\n"


def indent(text, n):
    pad = " " * n
    return "".join(pad + line if line.strip() else line for line in text.splitlines(True))


def labels(component, n):
    return indent(f"app.kubernetes.io/part-of: {LABEL}\napp.kubernetes.io/component: {component}\n", n)


def statefulset(name, image):
    p2p, rpc = NODES[name]
    return f"""apiVersion: v1
kind: Service
metadata:
  name: m3-{name}
  namespace: {NAMESPACE}
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
  namespace: {NAMESPACE}
  labels:
{labels(name, 4)}spec:
  serviceName: m3-{name}
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
            - -rpcauth=$(M3_RPCAUTH)
          env:
            - name: M3_RPCAUTH
              valueFrom:
                secretKeyRef:
                  name: m3-rpc
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
            limits: {{ cpu: "1", memory: "2Gi" }}
          volumeMounts:
            - {{ name: data, mountPath: /data }}
            - {{ name: config, mountPath: /config, readOnly: true }}
      volumes:
        - name: config
          configMap:
            name: m3-conf
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


def miner(image, env):
    env_lines = "".join(f'            - {{ name: {k}, value: "{v}" }}\n' for k, v in env)
    return f"""apiVersion: apps/v1
kind: Deployment
metadata:
  name: m3-miner
  namespace: {NAMESPACE}
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
            requests: {{ cpu: "100m", memory: "256Mi" }}
            limits: {{ cpu: "500m", memory: "1Gi" }}
          volumeMounts:
            - {{ name: home, mountPath: /var/lib/ark0 }}
            - {{ name: credentials, mountPath: /secrets, readOnly: true }}
            - {{ name: config, mountPath: /config, readOnly: true }}
      volumes:
        - name: home
          emptyDir: {{}}
        - name: credentials
          secret:
            secretName: m3-rpc
            items:
              - {{ key: rpccredentials.conf, path: rpccredentials.conf }}
            defaultMode: 0440
        - name: config
          configMap:
            name: m3-conf
            items:
              - {{ key: reward_address.txt, path: reward_address.txt }}
"""


def recorder(image):
    """recorder.py, from the producer image: reads the three nodes, prints one JSON line each per round."""
    script = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "recorder.py")).read()
    nodes = " ".join(f"{name}=m3-{name}:{rpc}" for name, (_, rpc) in NODES.items())
    return [f"""apiVersion: v1
kind: ConfigMap
metadata:
  name: m3-recorder
  namespace: {NAMESPACE}
data:
  recorder.py: |
{indent(script, 4)}""", f"""apiVersion: apps/v1
kind: Deployment
metadata:
  name: m3-recorder
  namespace: {NAMESPACE}
  labels:
{labels("recorder", 4)}spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
{labels("recorder", 6)}  template:
    metadata:
      labels:
{labels("recorder", 8)}    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 10000
        runAsGroup: 10000
        fsGroup: 10000
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: recorder
          image: {image}
          imagePullPolicy: IfNotPresent
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
          command: ["python3", "/recorder/recorder.py"]
          env:
            - {{ name: RECORD_NODES, value: "{nodes}" }}
            - {{ name: RECORD_M3_NODES, value: "{' '.join(RULE_NODES)}" }}
            - {{ name: RECORD_INTERVAL, value: "1800" }}
          resources:
            requests: {{ cpu: "10m", memory: "64Mi" }}
            limits: {{ cpu: "100m", memory: "128Mi" }}
          volumeMounts:
            - {{ name: credentials, mountPath: /secrets, readOnly: true }}
            - {{ name: recorder, mountPath: /recorder, readOnly: true }}
      volumes:
        - name: credentials
          secret:
            secretName: m3-rpc
            items:
              - {{ key: rpccredentials.conf, path: rpccredentials.conf }}
            defaultMode: 0440
        - name: recorder
          configMap:
            name: m3-recorder
"""]


def render(args):
    challenge, descriptor = keys()
    rules = [f"signetchallenge={challenge}", f"signetmldsa44={args.mldsa44_height}"]
    if args.tripwire:
        rules.append(f"signettripwire={args.tripwire}")
    confs = {
        "nodea": conf("nodea", rules),
        "nodeb": conf("nodeb", rules),
        # The M2 build knows neither option; it only follows the same challenge.
        "nodem2": conf("nodem2", [f"signetchallenge={challenge}"]),
    }
    data = "".join(f"  {name}.conf: |\n{indent(text, 4)}" for name, text in confs.items())
    data += f"  reward_address.txt: |\n    {reward_address()}\n"
    env = [
        ("ARK0_BLOCK_INTERVAL", args.interval), ("ARK0_WALLET", "ark0"),
        ("ARK0_RPC_HOST_A", "m3-nodea"), ("ARK0_RPC_PORT_A", NODES["nodea"][1]),
        ("ARK0_RPC_HOST_B", "m3-nodeb"), ("ARK0_RPC_PORT_B", NODES["nodeb"][1]),
        ("ARK0_CREDENTIALS", "/secrets/rpccredentials.conf"),
        ("ARK0_REWARD_ADDRESS_FILE", "/config/reward_address.txt"),
        # For m3_drill.py, which also asks the M2 node.
        ("M3_CONTROL_HOST", "m3-nodem2"), ("M3_CONTROL_PORT", NODES["nodem2"][1]),
    ]

    docs = [f"""apiVersion: v1
kind: Namespace
metadata:
  name: {NAMESPACE}
  labels:
    app.kubernetes.io/part-of: {LABEL}
""", f"""apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: m3-same-namespace-only
  namespace: {NAMESPACE}
spec:
  podSelector: {{}}
  policyTypes: ["Ingress"]
  ingress:
    - from:
        - podSelector: {{}}
""", f"""apiVersion: v1
kind: ConfigMap
metadata:
  name: m3-conf
  namespace: {NAMESPACE}
data:
{data}""", f"""# The challenge key, as a descriptor for node A's wallet (up.sh).
# Derived from a public string: a test key, not a secret.
apiVersion: v1
kind: Secret
metadata:
  name: m3-signer
  namespace: {NAMESPACE}
stringData:
  import.json: '{descriptor}'
"""]
    docs += [statefulset("nodea", args.node_image), statefulset("nodeb", args.node_image),
             statefulset("nodem2", args.m2_image), miner(args.miner_image, env)]
    docs += recorder(args.miner_image)
    sys.stdout.write("---\n".join(docs))
    print(f"{NAMESPACE}: challenge {challenge}, OP_CHECKMLDSA44 from {args.mldsa44_height}, "
          + (f"tripwire rules {args.tripwire}" if args.tripwire else "no tripwire rules yet"), file=sys.stderr)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--core", required=True, help="Core source tree carrying the M3 series (for test_framework)")
    p.add_argument("--node-image", required=True, help="the M3 build")
    p.add_argument("--m2-image", required=True, help="the M2 build Ark-0 runs")
    p.add_argument("--miner-image", required=True)
    p.add_argument("--interval", type=int, default=30, help="seconds between blocks (default 30)")
    p.add_argument("--mldsa44-height", type=int, default=50, help="-signetmldsa44 (default 50)")
    p.add_argument("--tripwire", help="-signettripwire, <H3>@<txid:vout>,<txid:vout>, once m3_arm.sh has set it")
    args = p.parse_args()
    if args.tripwire:
        m = re.fullmatch(r"(\d+)@[0-9a-f]{64}:\d+,[0-9a-f]{64}:\d+", args.tripwire)
        if not m or int(m.group(1)) <= args.mldsa44_height:
            p.error("--tripwire must be <H3>@<txid:vout>,<txid:vout> with H3 above --mldsa44-height")
    sys.path.insert(0, os.path.join(args.core, "test", "functional"))
    render(args)


if __name__ == "__main__":
    main()

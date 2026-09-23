# Ark-0 manifests

The Kubernetes side of moving the experimental signet off its host. This was
applied on 2026-09-16; `../ARK0-MIGRATION.md` is the procedure and carries the
record of that run, and it is the document to follow, not this one. This file
says what each manifest is and what has to be filled in first.

| File | What it creates |
|---|---|
| `00-namespace.yaml` | the `ark0` namespace |
| `10-configmap.yaml` | **template** for `ark0-conf`: both node configurations and the coinbase destination. Deliberately absent from `kustomization.yaml`; render it first |
| `render-config.sh` | fills that template, refuses placeholder or implausible values, and diffs against the live object before anything is applied |
| `15-networkpolicy.yaml` | default-deny ingress for the namespace, plus each node's P2P and RPC ports re-opened to pods in this namespace; see the caveat below |
| `20-pvcs.yaml` | `data-nodea-0` and `data-nodeb-0`, created before the StatefulSets so they can be loaded with the existing chain |
| `25-seed-pod.yaml` | `ark0-seed`, the helper pod the chain is copied through |
| `30-services.yaml` | headless `ark0-nodea` and `ark0-nodeb` |
| `40-statefulset-nodea.yaml` | node A, the block producing node |
| `41-statefulset-nodeb.yaml` | node B, the verifying node |
| `50-miner.yaml` | `ark0-miner`, the block producer |
| `60-observe.yaml` | `ark0-observe`: the ten minute observation probe, its 1Gi volume, its script, `alert.py`, which reports problems in the logs to a webhook, and the ServiceAccount that lets it read the three pod phases |
| `70-soak.yaml` | `ark0-soak`: the six hourly M0.5 wallet round trip and its script; it writes `soak.log` to the observation volume |

The numbers are the order to apply them in. Do not apply the directory in one
go: the chain has to be copied into the volumes between `25-seed-pod.yaml` and
the two StatefulSets, or the nodes start empty and node A mines a branch that
competes with the one the host already has.

`producer/` is not a manifest. It holds the two scripts the block producer
image runs, `ark0.py` and `miner_loop.sh`, which `../stage-miner.sh` copies
into the image build context. They used to live only in an operator's home
directory, which meant this checkout could not build the image and no reader
could see what went into it. Every path and the wallet name they use comes from
the environment with a documented default, so the same file runs on a
workstation and in a pod; `../Dockerfile.miner` sets the pod values and edits
neither file.

## The configuration is rendered, never applied as it stands

`10-configmap.yaml` is a template. It carries two placeholders, because both
values belong to one particular network rather than to this repository, and a
third value that is right for one cluster and wrong for the next. None of them
is a secret.

| Placeholder or value | What it is |
|---|---|
| `REPLACE_WITH_SIGNET_CHALLENGE` | the `signetchallenge=` line both nodes run |
| `REPLACE_WITH_REWARD_ADDRESS` | the coinbase destination the producer pays to |
| `rpcallowip=10.42.0.0/16` | the cluster's pod range; this cluster runs a `/24` |

Applying this file unrendered replaces both running nodes' configuration with
the literal string `REPLACE_WITH_SIGNET_CHALLENGE`, which is why it is no
longer in `kustomization.yaml` and why `kubectl apply -k` cannot do it. A
server-side dry run does not protect you here: the API server checks that a
ConfigMap holds strings, not that the strings are a usable `bitcoin.conf`.

`render-config.sh` is the supported path:

```bash
KUBECTL="sudo k3s kubectl" ./render-config.sh --from-live -o ~/ark0-conf.yaml
```

It fills the values, refuses anything missing, still a placeholder, or the
wrong shape, and then shows `kubectl diff` against the live object. Nothing is
applied unless you add `--apply`, and `--apply` runs the diff first.

| Source | Where the values come from |
|---|---|
| `--from-live` | the `ark0-conf` already on the cluster |
| `--from-host DIR` | `DIR/nodeA/bitcoin.conf` and `DIR/reward_address.txt` |
| environment | `ARK0_SIGNET_CHALLENGE`, `ARK0_REWARD_ADDRESS`, `ARK0_RPCALLOWIP` |

Prefer `--from-live` on a running network. It is what the nodes are reading, so
a reported "no difference" is proof that the render reproduced the running
configuration exactly rather than approximately. `rpcallowip` falls back to the
cluster's own pod CIDR when nothing supplies one.

The rendered file is not a secret but it is not repository content either.
Write it outside the checkout; `-o` is required so that it cannot default into
one.

## The two Secrets

Both are created by hand and neither appears in any file here. `kubectl create
secret` keeps their values out of the shell history only if the shell is
configured to ignore commands with a leading space, so prefer `--from-file`
over `--from-literal` where there is a choice.

**`ark0-rpc`**, the RPC credentials, two keys:

| Key | Content | Read by |
|---|---|---|
| `rpcauth` | one `user:salt$hash` line, without the `rpcauth=` prefix | both nodes, expanded into `-rpcauth=` |
| `rpccredentials.conf` | two lines, `rpcuser=` and `rpcpassword=` | the producer, appended to the client configurations its entry point writes |

Generate the pair with Core's own tool, `share/rpcauth/rpcauth.py` in the
source tree. The hash is what the nodes get, so the password never reaches a
node's command line; the password is what the producer gets, in a file, so it
never reaches a command line either.

**`ark0-signer`**, one key, `signer.wif`, from `~/.ark0/signer.wif`.

It is insurance, not a dependency. Node A's `ark0` wallet already holds the
signing key and travels with node A's data directory, so the producer signs
through `walletprocesspsbt` and never reads this file. It is mounted so that
the wallet can be rebuilt without going back to the host.

The exact commands are in `../ARK0-MIGRATION.md`, step 3.

## The wallet `70-soak.yaml` needs

`ark0-soak` is the only manifest here with a prerequisite that is not a
manifest: a descriptor wallet named `ark0soak` on node B, with private keys,
holding a ranged two leaf `tmr()` descriptor. Without it every run fails with
`no ranged tmr() descriptor in wallet ark0soak` and says so in `soak.log`.

Build it inside the pod, so the key never leaves it. The wallet's own master
key is enough, and `listdescriptors true` is the only place it is handled:

```sh
CLI="bitcoin-cli -conf=/config/nodeb.conf -datadir=/data"
$CLI -named createwallet wallet_name=ark0soak descriptors=true
W="$CLI -rpcwallet=ark0soak"
XPRV=$($W listdescriptors true | grep -o 'tprv[1-9A-HJ-NP-Za-km-z]*' | head -1)
BODY="tmr({pk($XPRV/0/*),pk($XPRV/1/*)})"
CS=$($CLI getdescriptorinfo "$BODY" | sed -n 's/.*"checksum": "\(.*\)".*/\1/p')
$W importdescriptors "[{\"desc\":\"$BODY#$CS\",\"timestamp\":\"now\",\"range\":[0,4]}]"
```

The import warns `Unknown output type, cannot set descriptor to active` if it
is asked to be active, and that is correct: `tmr()` has no output type, so the
wallet never hands out P2MR addresses and the descriptor stays inactive. It is
still spendable, and the wallet tops its range up to 1001 by itself. The recipe
is the one in `test/functional/wallet_p2mr.py` on the `p2mr-m05` branch, which
is also where the rest of what M0.5 promises is pinned down.

Nothing about that wallet is written down here, because the job reads the
descriptor back out of it with `listdescriptors` at run time.

## Alerts

Each observe run ends by handing both logs to `alert.py`, from the same
ConfigMap. It posts the problems it finds in the newest lines to a webhook,
and says so again when they clear:

| Key | Reported when |
|---|---|
| `rpc-A`, `rpc-B` | observe could not read the node, its `uptime` call or its height, in the last two samples |
| `pod-<name>` | a node or the producer was not Running and ready in the last two samples |
| `restart-<name>` | its restart count went up within the last hour |
| `stall` | node A's height has not moved for 30 minutes |
| `split` | the two nodes were on different tips in the last two samples |
| `tips` | node A lists more chain tips than an hour earlier: a fork or a rejected block |
| `soak` | the last line of `soak.log` is a failure, `dims FAIL` included |
| `soak-stale` | `soak.log` has had no line for ten hours; the job runs every six and may take 3 h 20 min. A missing or empty `soak.log` counts once `observe.log` goes back further than that: the soak job derives its address index from the lines in `soak.log`, so the file is never rotated, and missing or empty means a new volume or a lost log |
| `observe` | the newest observe line is more than 20 minutes old |

A problem is posted when it appears, again every six hours while any lasts,
and once more when the last one clears; `/observe/alert-state.json` holds what
was last delivered, and nothing that was not delivered is recorded. The new
state is written beside it before each post and put in place only after the
post went through; if even that cannot be written, the post still goes out
and says that it may repeat every ten minutes, rather than repeating without
a word or not going out at all. Delivery is therefore at least once: a run
that dies between a post and its record, or cannot rename the record into
place, can repeat that post. Runs take a lock on `/observe/alert.lock`, so a
Job made by hand from the CronJob waits for a scheduled run instead of
interleaving with it. On a volume that cannot be written the lock is taken
through a read-only handle. A run that cannot take the lock at all keeps no
state: it posts, with the warning that the post may repeat, and records
nothing. If the evaluation itself fails, for instance because a log cannot be
read, a line naming the error is posted instead. The two-sample rules ride out a single bad sample, such as a
pod restarting during planned maintenance. A post that fails is retried by
the next run, and a failure of `alert.py` never fails the observe run, whose
retry would append a second line for the same ten minutes.

Replayed against the observe log of 2026-09-21, when a reboot left both jobs
failing for 36 hours with nobody told, these rules report the three restarts
from the first run after the host came back, and `rpc-A` and `rpc-B` from the
second, ten minutes later. The replay reads the log as it was written. Three
records of that period, all on 2026-09-22, were split in two by the tip-count
bug this change fixes in `observe.sh`; they are skipped, and the records around
them carry the same readings.

The webhook is optional; without it `alert.py` prints what it would have sent
and records nothing, so the first run after the Secret appears posts whatever
is still wrong. A value that is not an `https://` URL is refused without being
printed; no path prints the URL.
It posts one JSON body, `{"text": ...}`, which a Slack incoming webhook takes
as it is. The URL is a credential, so it lives in a Secret created by hand,
from a file rather than the command line:

```bash
kubectl -n ark0 create secret generic ark0-alert --from-file=webhook-url=<file holding the URL>
```

Check the channel once with a Job made from the CronJob that runs
`alert.py --test` instead of the probe, which posts one line:

```bash
kubectl -n ark0 create job alert-test --from=cronjob/ark0-observe --dry-run=client -o json \
  | python3 -c 'import json, sys; j = json.load(sys.stdin); c = j["spec"]["template"]["spec"]["containers"][0]; c["command"] = ["python3", "/script/alert.py", "--test"]; print(json.dumps(j))' \
  | kubectl apply -f -
```

What this cannot report is its own absence: with the host or the cluster
down, or the observe CronJob not running at all, nothing runs `alert.py`.
Covering that takes something outside the cluster, such as a heartbeat
service that alerts when the pings stop.

## Network isolation, and whether your cluster enforces it

`15-networkpolicy.yaml` closes ingress to the namespace by default and re-opens
only each node's P2P and RPC ports, to pods in this namespace. Without it the
namespace is a naming boundary and not a network one: Kubernetes allows all
pod-to-pod traffic across all namespaces by default, and a headless Service
publishes the pod addresses directly.

The API server accepts a NetworkPolicy whether or not anything implements it,
so applying the file proves nothing. It has to be measured, and one failed
connection does not measure it. A `kubectl exec` that could not start, an image
without `bash`, a `/dev/tcp` the shell was built without, a node that is not
listening on the port yet: each of those exits non-zero and says nothing at all
about the policy. So the denied connection is only worth reading once two
things have been established from the same run, and even then it shows that
something filtered the path rather than that this file did.

```bash
PROBE_NS=<a namespace that is not ark0>
PROBE_POD=<a pod in it, from an image with bash>
IP=$(kubectl -n ark0 get pod nodea-0 -o jsonpath='{.status.podIP}')
K="kubectl -n $PROBE_NS exec $PROBE_POD -- timeout 5"

# 1. Control: can this pod open a TCP connection at all, with this tooling?
#    Aim it outside ark0, where no policy here has an opinion. Any listener
#    will do; the cluster DNS Service is one that is always there.
DNS=$(kubectl -n kube-system get svc kube-dns -o jsonpath='{.spec.clusterIP}')
$K bash -c "echo > /dev/tcp/$DNS/53"; echo "control: $?"

# 2. Control: is anything accepting connections on the port under test? Ask
#    from inside ark0, which is what the policy's allow rule is for.
kubectl -n ark0 exec nodeb-0 -- timeout 5 bash -c "echo > /dev/tcp/$IP/38432"
echo "listener: $?"

# 3. The measurement.
$K bash -c "echo > /dev/tcp/$IP/38432"; echo "probe: $?"
```

Read the three together:

| control | listener | probe | what it means |
|---|---|---|---|
| 0 | 0 | 124 | **consistent with filtering; enforcement unconfirmed.** The pod can connect, the port accepts connections from inside the namespace, and from outside the packets go nowhere until `timeout` gives up. Something is dropping them. These three readings cannot say it was this file. |
| 0 | 0 | 0 | **not enforced.** The connection went through, so nothing filtered that path. This is the one reading the probe settles by itself. |
| 0 | 0 | 1 | **consistent with filtering; enforcement unconfirmed.** `bash` was refused rather than left hanging. A plugin that drops denied packets gives the first row instead; kube-router rejects them with an ICMP port-unreachable, so on a kube-router cluster, which is what k3s runs by default, this is the reading enforcement gives. Like the first row, it needs the CNI's own rules to confirm. |
| non-zero | any | any | **inconclusive.** The probe pod cannot make connections, or has no `bash`, or no `/dev/tcp`. 126 and 127 are that last case. Nothing was tested. |
| 0 | non-zero | any | **inconclusive.** Nothing is listening on that port, so the probe had nothing to be blocked from. Check the node is up first. |

**The first row is not a pass, and the reason is worth being exact about.** The
two controls establish that the probe pod can open connections and that the
listener accepts them. Neither ties the timeout to `15-networkpolicy.yaml`. A
probe pod under an egress policy of its own, one that permits DNS and denies
this namespace, passes the first control and produces the same timeout with
Ark-0's ingress policy absent entirely. Node B's success in the second control
would also be unchanged by that absence, because a default-allow cluster lets
it through anyway. Any of a host firewall, another namespace's policy, a
service mesh or a CNI default can produce this reading.

So the timeout is the beginning of the case and not the end of it. Declaring
enforcement needs the rules the CNI actually installed, naming these policies,
read out of the CNI rather than inferred from a connection that failed. What
that means depends on the plugin: for kube-router, the policy chains and ipsets
it programs on the node, visible in `iptables-save` and `ipset list`; for
Calico or Cilium, that plugin's own policy dump for the pod. If this cluster's
policies are absent from that output, the timeout came from something else.

An image without `/dev/tcp` can use `nc` instead, keeping each place aimed
where it was: `nc -z -w 5 "$DNS" 53` for the first control, and
`nc -z -w 5 "$IP" 38432` for the second and the third. Aiming the first
control at the port under test would make it a second copy of the denied
probe and leave nothing controlled. One reading changes: `nc` exits 1 for a
timeout as well as for a refusal, so the first and third data rows collapse
into `0/0/1`, and under `nc` that combination is inconclusive rather than
consistent with filtering. `nc` cannot tell you which of the two happened.

On the cluster this network runs on, the policy was decoration until the
host was rebooted on 2026-09-21 and has been in force since, and both
conclusions rest on the CNI's own rules rather than on a probe. Before the
reboot kube-router's policy controller could not program its ipsets there,
so no policy chain and no ipset existed for these policies and every
pod-to-pod path was open; the diagnostic is in `../ARK0-MIGRATION.md`.
Since the reboot the `ip_set`, `ip_set_hash_ip` and `xt_set` modules are
loaded, and `iptables -S` on the node shows `KUBE-NWPLCY-*` chains naming
`ark0-nodea-ingress` and `ark0-nodeb-ingress`, matching on `KUBE-SRC-*` and
`KUBE-DST-*` sets, and one for `ark0-default-deny-ingress`. The probe above,
run from a pod in another namespace against node A's RPC port, reads
`0/0/1`. The observe job, whose pod is new every ten minutes, read heights
and peer counts from both nodes on every run until the host went down and
from neither on any run after it came back (its later `uptime` fields
read `ok`, the pod having been admitted by then), which puts the change
at the reboot. Nothing in
the manifests changed between the two readings. Whether this file is in
force is a property of the node, not of the file, and it has to be re-read
after any host change.

Enforcement has one consequence for pods in this namespace. kube-router adds
a pod's address to the source sets after the pod starts, and until it has,
the nodes refuse that pod (kube-router rejects rather than drops, which is
also why the probe above reads `0/0/1` here and not `0/0/124`). In the one
measurement taken, from a fresh pod, the first connections failed and the
first success came 1.8 s after start. A long-running pod never
notices; a CronJob pod whose script talks to a node in its first second does,
and that is how it was found, as `ERR` in every observe line and a failed
soak run after the reboot. `60-observe.yaml` and `70-soak.yaml` therefore
wait once, bounded, for each node to answer before they read anything.

## Running as a normal user, and the one cluster that cannot

Every workload here runs as uid 10000, the uid the images create and declare,
with `runAsNonRoot`, an `fsGroup` that gives that uid the data volume and the
mounted Secret, `seccompProfile: RuntimeDefault`, `allowPrivilegeEscalation:
false` and every capability dropped. The two Secret mounts are `0440` rather
than `0400`, because a Secret volume belongs to root and the pod's `fsGroup`,
so `0400` is readable only by root and an unprivileged pod reads it by group.

The cluster this network actually runs on cannot do that. Its nodes carry an
eBPF program attached to the root cgroup at `cgroup_inet_sock_create` which
denies `socket(AF_INET)` to uid 10000, so every one of these pods fails at
socket creation, before any address is involved. That belongs to that cluster
rather than to these manifests, so it is a separate overlay:

```bash
kubectl apply -k contrib/k3s/overlays/root-sockets
```

`../overlays/root-sockets/README.md` explains the restriction, shows how to
tell it apart from a port permission problem, and gives the check for deciding
whether any given cluster needs it. Use the base everywhere else.

## Checking the manifests without applying them

`kustomization.yaml` gathers the manifests so every variant can be validated in
one command. Against a cluster where the `ark0` namespace already exists:

```bash
kubectl apply --dry-run=server -k contrib/k3s/ark0
kubectl apply --dry-run=server -k contrib/k3s/overlays/root-sockets
kubectl apply --dry-run=server -k contrib/k3s/overlays/root-sockets-maintenance
```

The third is the one to use for every apply while nodes or configuration are
being changed. It is `root-sockets` with the block producer declared at zero
replicas, because an ordinary apply restores `replicas: 1` and restarts a
producer that was deliberately stopped. `../ARK0-MIGRATION.md` has the
sequence.

Before the namespace exists, a server-side dry run has nothing to validate
against, so use `kubectl apply --dry-run=client -f contrib/k3s/ark0/`.

`kustomization.yaml` is for validation and for giving the overlay something to
patch. It is not a bring-up procedure: applying it in one go skips loading the
chain into the volumes, and `25-seed-pod.yaml` is deliberately not in it.

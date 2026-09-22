# Moving the Ark-0 experimental signet into the cluster

A plan, not a script. Nothing in this document has been executed. It is written
to be followed top to bottom by someone with shell access to the node and
`kubectl`, and every step says what to check before moving on.

**Do not start while the network is under an observation freeze.** The
migration stops both nodes and the block producer, which breaks the 90 second
block cadence the observation depends on.

**This was executed on 2026-09-16.** What actually happened, the values it
was checked against, and where it departed from this plan are in the last
section. Read that before treating anything above as still pending.

## What exists today

Three systemd units on the host, all running as the operating account rather
than as root. `$HOME` below is that account's home directory.

| Unit | What it runs |
|---|---|
| `ark0-nodeA` | `bitcoind -datadir=$HOME/.ark0/nodeA`, the block producing node |
| `ark0-nodeB` | `bitcoind -datadir=$HOME/.ark0/nodeB`, the verifying node |
| `ark0-miner` | `$HOME/.ark0/demo/miner_loop.sh`, one block every `ARK0_BLOCK_INTERVAL=90` seconds |

The binaries come from `$HOME/bitcoin-p2mr/build/bin`. Both nodes are on
the same custom signet, sharing
`signetchallenge=5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae`,
with `txindex=1` and `debug=validation`.

| | node A | node B |
|---|---|---|
| P2P | `127.0.0.1:38433` | `127.0.0.1:38443` |
| RPC | `127.0.0.1:38432` | `127.0.0.1:38442` |
| peering | `addnode=127.0.0.1:38443` | `connect=127.0.0.1:38433` |
| data | `~/.ark0/nodeA/signet`, ~20 MB | `~/.ark0/nodeB/signet`, ~19 MB |

RPC uses cookie authentication: neither `bitcoin.conf` sets `rpcuser` or
`rpcauth`, so `bitcoin-cli` reads `<datadir>/signet/.cookie`.

The block producer is `~/.ark0/demo/ark0.py mine 1 --quiet` in a loop. It
drives node A through `bitcoin-cli -datadir=<nodeA> -signet -rpcwallet=ark0`,
loads `contrib/signet/miner` and `test_framework` out of the source tree, and
signs the signet solution with the `ark0` wallet in
`~/.ark0/nodeA/signet/wallets/ark0`. The signer key also exists on its own at
`~/.ark0/signer.wif`.

## What it becomes

Namespace `ark0`, nothing exposed outside the cluster.

The manifests are in `ark0/`, numbered in the order they are applied. That
directory's README says what each one is and what has to be filled in.

| Object | File | Purpose |
|---|---|---|
| `Namespace/ark0` | `00-namespace.yaml` | holds everything below |
| `ConfigMap/ark0-conf` | `10-configmap.yaml` | `nodea.conf`, `nodeb.conf`, `reward_address.txt` |
| `NetworkPolicy` x3 | `15-networkpolicy.yaml` | default-deny ingress, then each node's P2P and RPC ports re-opened to this namespace; see the caveat above |
| `PersistentVolumeClaim` x2 | `20-pvcs.yaml` | `data-nodea-0` and `data-nodeb-0`, created ahead of the StatefulSets so they can be loaded with the existing chain |
| `Pod/ark0-seed` | `25-seed-pod.yaml` | mounts both volumes so the chain can be copied in and, on a rollback, back out |
| `Service` x2 | `30-services.yaml` | headless `ark0-nodea` and `ark0-nodeb`, P2P 38433/38443 and RPC 38432/38442 |
| `StatefulSet/nodea` | `40-statefulset-nodea.yaml` | one replica, the `p2mr-node-m0` image, `/data` from a `volumeClaimTemplate` named `data` |
| `StatefulSet/nodeb` | `41-statefulset-nodeb.yaml` | the same with node B's configuration and ports, and the `p2mr-node-m05` image |
| `Deployment/ark0-miner` | `50-miner.yaml` | one replica, the `p2mr-miner` image, the loop against `ark0-nodea` |
| `Secret/ark0-rpc` | by hand | `rpcauth` for the nodes, `rpccredentials.conf` for the producer |
| `Secret/ark0-signer` | by hand | `signer.wif` |
| `CronJob/ark0-observe` + its volume | `60-observe.yaml` | the ten minute probe that replaces the host crontab line |
| `CronJob/ark0-soak` | `70-soak.yaml` | the six hourly M0.5 wallet round trip, writing to the observation volume |

Three images, not two, and the two node builds are not interchangeable:

| Name in the manifests | Built from | Used by |
|---|---|---|
| `p2mr-node-m0` | the ten consensus patches | node A, the block producer's chain |
| `p2mr-node-m05` | those plus the twenty-four M0.5 wallet patches | node B, the verifying node |
| `p2mr-miner` | the built tree's CLI and the producer scripts | the producer, the probe, the soak job |

Those are names without tags. The tag each one runs is in one place,
the `images:` block of `ark0/kustomization.yaml`, and it is always an
immutable tag of the form `p2mr-node:v31.1-p2mr-m05-<head12>`, where the
suffix is the first twelve hex digits of the commit the build's
`PROVENANCE.txt` records. `build-image.sh` writes such a tag once and never
moves it, so a tag names one build for good and what a workload runs is a fact
about that file. The consequence for these commands is that the workload files
are applied through kustomize; a `kubectl apply -f` on one of them alone asks
for an image with no tag and gets `p2mr-node-m0:latest`, which does not exist.
`README.md` in this directory carries the rule; rolling and rolling back are
below under [Maintenance](#maintenance-every-apply-is-a-producer-start).

The three tags committed in that block are this network's, from 2026-09-16.
They are not defaults: a bootstrap elsewhere replaces them with the references
its own builds print, in step 2 below, before step 5 starts anything.

Two things change out of necessity, and nothing else:

- **Addresses.** `bind` and `rpcbind` move from `127.0.0.1` to `0.0.0.0`,
  because a pod's loopback is not reachable from another pod. The ports stay
  the same. What that costs is stated below; it is the one real reduction in
  isolation this migration makes.
- **RPC authentication.** The cookie file lives inside node A's data directory,
  which the producer pod does not have. The nodes get an `rpcauth` line and the
  producer a credentials file, both from the `ark0-rpc` Secret. The
  alternative, mounting node A's volume into the producer read-only just to
  read its cookie, works on a single node but ties the two pods to one
  another's storage; credentials are the plainer answer. Each node still writes
  its own cookie, which is what its start-up probe authenticates with.

### What the namespace does and does not isolate

An earlier version of this document said isolation came from the namespace and
from there being no Service other than ClusterIP. That was wrong, and it is
worth being exact about why, because the mistake is easy to repeat.

Headless Services and the absence of a NodePort do keep these ports off the
node's own network: nothing outside the cluster can reach them. They do nothing
about the cluster's internal pod network. Kubernetes permits all pod-to-pod
traffic by default, in every direction, across every namespace, and a headless
Service makes the pod addresses easier to find rather than harder. A namespace
is a naming and authorisation boundary; on its own it is not a network
boundary.

Measured on this cluster on 2026-09-16, before any policy existed: a pod in the
unrelated `p2mr-build` namespace opened TCP connections to node A's RPC port
`38432`, node A's P2P port `38433` and node B's RPC port `38442`. RPC
authentication still applied, so nothing was read without credentials, but the
listeners were reachable.

`ark0/15-networkpolicy.yaml` is the fix: default-deny ingress for the whole
namespace, plus two rules re-opening each node's P2P and RPC ports to pods in
this namespace only. Egress is left open, and the file says why.

**On this cluster it does not take effect, and nothing here should be read as
if it does.** k3s runs kube-router's NetworkPolicy controller, and applying
these three policies made it fail every sync:

```
network_policy_controller.go:302] Aborting sync. Failed to sync network policy
chains: failed to perform ipset restore: ipset v7.16: Error in line 1: Kernel
error received: set type not supported
```

The `ip_set_hash_*` modules are present under `/lib/modules` but were not
loaded, and the iptables `set` match (`xt_set`) is missing as well, which is
the error that remains after loading them. No policy chain and no ipset was
ever created, and a cross-namespace connection to the RPC port succeeded
exactly as before. The policies were removed again (they were applied again
at 13:20 UTC the same day and stayed in place, unenforced, until the reboot
described below), and these were the first
NetworkPolicy objects this cluster had ever been given, so the breakage is
pre-existing and was merely unobserved.

Making it work is a host change on a shared cluster: loading the ipset modules
and `xt_set` persistently, then confirming no other namespace's traffic is
affected. That is an operator decision, not something this migration should do
on its own. Until it is made, treat the namespace boundary as **not enforced**,
and rely on RPC authentication and on nothing being published outside the
cluster. Anyone adopting these manifests on another cluster gets the policy for
free, and should verify enforcement rather than assume it; the check is in the
header of `ark0/15-networkpolicy.yaml`.

*Update, 2026-09-22.* The host was rebooted on 2026-09-21 (down 00:41 to
04:25 UTC) and came back with `ip_set`, `ip_set_hash_ip` and `xt_set` loaded,
and kube-router has enforced these three policies since; nothing in the
manifests changed. The evidence, the side effect on the observe and soak jobs
(in the one measurement taken, a fresh pod was refused for its first 1.8 s, so
both scripts now wait once for each node) and what it means for reading this
section are in
`ark0/README.md`, "Network isolation, and whether your cluster enforces it".

## Order of work

### 1. Record the state you have to reproduce

With everything still running. Keep the output: it is what step 7 compares
against.

```bash
cd ~
BIN=~/bitcoin-p2mr/build/bin
for n in nodeA nodeB; do
  echo "=== $n"
  $BIN/bitcoin-cli -datadir=$HOME/.ark0/$n -signet getblockcount
  $BIN/bitcoin-cli -datadir=$HOME/.ark0/$n -signet getbestblockhash
  $BIN/bitcoin-cli -datadir=$HOME/.ark0/$n -signet getblockhash 0
  $BIN/bitcoin-cli -datadir=$HOME/.ark0/$n -signet getchaintips
  $BIN/bitcoin-cli -datadir=$HOME/.ark0/$n -signet getdeploymentinfo | grep -A4 p2mr
  $BIN/bitcoin-cli -datadir=$HOME/.ark0/$n -signet getpeerinfo | grep -c '"addr"'
done > ~/ark0-premigration.txt
$BIN/bitcoin-cli -datadir=$HOME/.ark0/nodeA -signet listwallets >> ~/ark0-premigration.txt
$BIN/bitcoin-cli -datadir=$HOME/.ark0/nodeA -signet -rpcwallet=ark0 getwalletinfo >> ~/ark0-premigration.txt
cat ~/ark0-premigration.txt
```

Both nodes must report the same `getbestblockhash` and the same genesis hash,
and exactly one chain tip each with `status: active`. If they do not, stop:
the network has a problem that migrating will only hide.

### 2. Build the three images, in this order

All three were built and imported on 2026-09-15, so on this network the step is
a check rather than work. `contrib/k3s/README.md` covers the machinery; what
follows is the exact sequence that produces the three tags the manifests name,
because an earlier version of this document was not one. It said
`build-image.sh node` once, which produced `p2mr-node:v31.1-p2mr` -- a tag no
workload asks for -- and never produced the two that they do.

**Order matters.** Both node images are built from the same directory,
`/work/imgctx/node/bin`, which every build Job overwrites when it publishes. An
image has to be built immediately after the Job whose binaries it packages,
before the next Job replaces them.

```bash
export KUBECTL="sudo k3s kubectl"
cd /path/to/bitcoin-p2mr-patches

# Once: the patch series and the builder and node Dockerfiles onto the work
# volume, then the builder, which is built from the one seed.sh just put there.
# That order, not the other one. run-build.sh refuses to start if the builder
# reference it resolves is not in containerd, and says what to run.
contrib/k3s/seed.sh
contrib/k3s/build-image.sh builder

# 1. The ten consensus patches -> node A's image.
PATCH_SETS="master" contrib/k3s/run-build.sh
contrib/k3s/build-image.sh node            # last two lines: the reference, then its digest

# 2. Those plus the twenty-four M0.5 wallet patches -> node B's image.
PATCH_SETS="master m05" FRESH_CLONE=1 RUN_DEFAULT_FUNCTIONAL=1 \
FUNCTIONAL_TESTS="wallet_p2mr.py wallet_p2mr_signet.py wallet_p2mr_multisig.py \
                  wallet_p2mr_timelock.py feature_p2mr.py feature_p2mr_signet.py \
                  p2p_segwit.py" \
  contrib/k3s/run-build.sh
contrib/k3s/build-image.sh node            # a different reference: a different head

# 3. The producer image, from the M0.5 build's CLI and the repository's scripts.
contrib/k3s/stage-miner.sh
contrib/k3s/build-image.sh miner
```

Nothing in those `build-image.sh` lines names a tag, and that is deliberate.
`build.sh` publishes binaries only after every check it was asked for has
passed, and writes `PROVENANCE.txt` beside them recording the patch sets, the
verified series and the commit the source was at; `build-image.sh` requires all
three and derives both halves of the name from them. So the reference is a
consequence of what was compiled and tested rather than of what someone typed,
and labelling an M0.5 build `-m0` -- which would put wallet code on the block
producing node -- is not one keystroke away. A run whose tests failed publishes
nothing, and `build-image.sh` refuses a context whose provenance does not pass,
does not record a verified series, or does not record a head.

#### Record the three references, and pin them

Each `build-image.sh` run prints two lines after a blank one: the reference it
wrote, and that reference's manifest digest. Those are the outputs of this step
and there is nothing else to derive them from afterwards, because a head-suffixed
reference cannot be guessed from the patch set. Write all six values down:

```
p2mr-node:v31.1-p2mr-m0-<head12>     sha256:...     # from build 1
p2mr-node:v31.1-p2mr-m05-<head12>    sha256:...     # from build 2
p2mr-miner:v31.1-p2mr-<head12>       sha256:...     # from build 3
```

Then put them in the file the workloads read, before step 5 starts anything:

```bash
( cd contrib/k3s/ark0
  kustomize edit set image p2mr-node-m0=p2mr-node:v31.1-p2mr-m0-<head12>
  kustomize edit set image p2mr-node-m05=p2mr-node:v31.1-p2mr-m05-<head12>
  kustomize edit set image p2mr-miner=p2mr-miner:v31.1-p2mr-<head12> )
```

The subshell is there because `kustomize edit` insists on being run beside the
file it edits, and every command before and after this one in these steps is
relative to the repository root. A bare `cd` here leaves the shell two
directories down and the next `kubectl apply -k contrib/k3s/...` cannot find
its path.

Or type the three `newTag` values into the `images:` block of
`kustomization.yaml`, which is all those commands do and needs no extra tool.

**The values committed in that block are not defaults.** They are this
network's, from its migration on 2026-09-16, and they name builds that exist
only in this cluster's containerd. A bootstrap elsewhere that leaves them alone
gets three workloads asking for images nobody has. Replacing them is part of
this step, not an optional tidy afterwards.

#### Verify the digests before applying any workload

Check that containerd holds exactly what was just recorded, and what each image
was built from:

```bash
sudo k3s ctr -n k8s.io images ls | awk '/p2mr-(node|miner)/ {print $1, $3}'
grep -A2 '^  - name:' contrib/k3s/ark0/kustomization.yaml
sudo k3s kubectl -n p2mr-build exec p2mr-shell -- cat /work/imgctx/node/PROVENANCE.txt
```

Every reference in the `images:` block must appear in the first listing with
the digest recorded above. After the workloads are running, what they actually
pulled is visible per pod, and must match:

```bash
for p in nodea-0 nodeb-0; do
  sudo k3s kubectl -n ark0 get pod $p \
    -o jsonpath='{.metadata.name}{"  "}{.spec.containers[0].image}{"  "}{.status.containerStatuses[0].imageID}{"\n"}'
done
sudo k3s kubectl -n ark0 get deploy ark0-miner -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
```

Read the image out of `.spec`, not out of `.status`: a container status keeps
reporting whichever of an image's names the kubelet resolved it under, which
after a switch between two names for one digest is the old one. The spec says
what is pinned and the `imageID` says what is running.

Two node digests that are equal mean the same build was published twice and
node B is not running M0.5 at all. Node A's image must be the `-m0` one: the
block producer extends this chain, and a different consensus build would fork
it. Cross-check its version and series against whatever the chain was produced
with so far, using the reference recorded above:

```bash
sudo k3s kubectl -n p2mr-build run p2mr-node-check-m0 --rm -i --restart=Never \
  --image=p2mr-node:v31.1-p2mr-m0-<head12> --image-pull-policy=IfNotPresent -- -version
```

The producer image carries the copies of `ark0.py` and `miner_loop.sh` from
`contrib/k3s/ark0/producer/`, unmodified: both read their paths and the wallet
name from the environment, and `Dockerfile.miner` sets the pod values. Rebuild
it when either script changes in the repository. Check it without a network:

```bash
sudo k3s kubectl -n p2mr-build run p2mr-miner-check --rm -i --restart=Never \
  --image=p2mr-miner:v31.1-p2mr-<head12> --image-pull-policy=IfNotPresent \
  -- python3 /opt/ark0/demo/ark0.py --help
```

That is build 3's reference from the list above, the same one the
`p2mr-miner` entry was just pinned to. The shared `p2mr-miner:v31.1-p2mr` this
line used to name belongs to the Ark-0 cluster's history and is not created by
any build; asking for it here is asking for an image a fresh bootstrap has
never had.

### 3. Create the namespace, the config and the secrets

Work from a checkout of this repository, with a scratch directory for the two
files that must not be committed:

```bash
mkdir -p ~/ark0-migration && chmod 700 ~/ark0-migration
sudo k3s kubectl apply -f contrib/k3s/ark0/00-namespace.yaml
```

`10-configmap.yaml` is a template. It carries two placeholders, because both
values belong to this one network rather than to the repository, and a third
value, `rpcallowip`, that is right for one cluster and wrong for the next.
`render-config.sh` fills all three, refuses anything missing, still a
placeholder or the wrong shape, and shows the diff against whatever is already
on the cluster before it applies anything:

```bash
KUBECTL="sudo k3s kubectl" contrib/k3s/ark0/render-config.sh \
    --from-host ~/.ark0 -o ~/ark0-migration/configmap.yaml
# read the diff, then:
sudo k3s kubectl -n ark0 apply -f ~/ark0-migration/configmap.yaml
```

On a network that is already in the cluster, use `--from-live` instead of
`--from-host`: it takes the values out of the running `ark0-conf`, so a
reported "no difference" proves the render reproduced the running
configuration exactly rather than approximately.

The template is deliberately **not** in `contrib/k3s/ark0/kustomization.yaml`
and not in either overlay. That is what stops `kubectl apply -k` from
replacing both nodes' configuration with the literal string
`REPLACE_WITH_SIGNET_CHALLENGE`, which it would do without complaint: a
server-side dry run checks that a ConfigMap holds strings, not that the strings
are a usable `bitcoin.conf`. Every apply of this file goes through
`render-config.sh`.

`rpcallowip=10.42.0.0/16` in the template is a pod range, and this cluster runs
a `/24`. With nothing supplied, the script reads the cluster's own
`spec.podCIDR` rather than widening it to the template's value.

The two Secrets are created by hand and never committed. The `rpcauth` line
and its password come from Core's own generator:

```bash
cd ~/ark0-migration
python3 ~/bitcoin-p2mr/share/rpcauth/rpcauth.py ark0 > rpcauth.out
# rpcauth.out holds the rpcauth=... line and, on its last line, the password.
grep '^rpcauth=' rpcauth.out | cut -d= -f2- > rpcauth.txt
printf 'rpcuser=ark0\nrpcpassword=%s\n' "$(tail -1 rpcauth.out)" > rpccredentials.conf

sudo k3s kubectl -n ark0 create secret generic ark0-rpc \
    --from-file=rpcauth=rpcauth.txt \
    --from-file=rpccredentials.conf
sudo k3s kubectl -n ark0 create secret generic ark0-signer \
    --from-file=signer.wif=$HOME/.ark0/signer.wif

shred -u rpcauth.out rpcauth.txt rpccredentials.conf
```

Everything goes in through `--from-file` so that no secret ever reaches the
shell history, and `rpcauth.txt` holds the line without its `rpcauth=` prefix
because the StatefulSets add that themselves.

The nodes take the `rpcauth` line as an argument expanded from the Secret, so
the password itself never reaches a pod's command line; only the salted hash
does, which cannot be used to authenticate.

`ark0-signer` is insurance. The `ark0` wallet inside node A's data directory
already holds the signer key and is migrated with it in step 5, so the miner
does not read the WIF in normal operation. It is there so the wallet can be
rebuilt without going back to the host.

### 4. Pre-create the volumes and load the chain data

Creating the PVCs by hand, with the names a StatefulSet will look for
(`<volumeClaimTemplate>-<statefulset>-<ordinal>`), means the nodes never start
on an empty chain. A StatefulSet adopts a PVC that already exists.

```bash
sudo k3s kubectl apply -f contrib/k3s/ark0/20-pvcs.yaml
sudo k3s kubectl apply -f contrib/k3s/ark0/25-seed-pod.yaml
sudo k3s kubectl -n ark0 wait --for=condition=Ready pod/ark0-seed --timeout=300s
```

local-path binds a volume on first use, which is why the helper pod comes up
now: until something mounts both claims, there is nowhere to write.

**Now stop the network**, producer first, so no block is mined into a data
directory that is being copied:

```bash
sudo systemctl stop ark0-miner
sudo systemctl stop ark0-nodeB ark0-nodeA
systemctl is-active ark0-miner ark0-nodeA ark0-nodeB   # all three: inactive
pgrep -af 'bitcoind -datadir=$HOME/.ark0'       # no output
```

The units stop with `SIGTERM` and `TimeoutStopSec=180`; wait for `bitcoind` to
be gone rather than assuming, because a node killed mid-flush has to reindex.

Copy each `signet/` directory into its volume. The stale pid, cookie and lock
files belong to the stopped processes and are left behind:

```bash
for pair in "nodeA /a" "nodeB /b"; do
  set -- $pair
  tar -C "$HOME/.ark0/$1" --exclude=./signet/bitcoind.pid \
      --exclude=./signet/.cookie --exclude=./signet/.lock -cf - ./signet \
    | sudo k3s kubectl -n ark0 exec -i ark0-seed -- tar -C "$2" -xf -
done
sudo k3s kubectl -n ark0 exec ark0-seed -- chown -R 0:0 /a /b
sudo k3s kubectl -n ark0 exec ark0-seed -- sh -c 'du -sh /a/signet /b/signet; ls /a/signet/wallets'
```

**`0:0` because step 5 applies the `root-sockets` overlay**, under which the
nodes run as root. Ownership has to match whichever uid the nodes actually
run as, and root is not a way around that here: the container drops every
capability, so it has no `DAC_OVERRIDE` to read an owner-only data directory
and an owner-only wallet with. Under the unprivileged base the value is
`10000:10000`, and the choice belongs with the overlay rather than with this
command -- `contrib/k3s/overlays/root-sockets/README.md` says which cluster
needs which, and switching a running network between them means chowning the
volumes and not just re-applying. This step said `10000:10000` while step 5
always selected the root overlay, which is a mismatch that shows up as a node
that starts and cannot open its own chain.

The sizes must match `du -sh ~/.ark0/nodeA/signet ~/.ark0/nodeB/signet` to
within rounding, and `/a/signet/wallets` must list `ark0`. Then:

```bash
sudo k3s kubectl -n ark0 delete pod ark0-seed
```

### 5. Start the nodes

One `apply -k`, of the maintenance overlay, which is the same set as the
ordinary one with the producer declared at zero:

```bash
sudo k3s kubectl apply --dry-run=server -k contrib/k3s/overlays/root-sockets-maintenance
sudo k3s kubectl apply             -k contrib/k3s/overlays/root-sockets-maintenance
```

Per-file `apply -f` is not an option here and not merely discouraged: the
workload files name an image without a tag and the tag comes from the
kustomization. Which leaves the ordering to kustomize, which is why the
producer has to be declared at zero for this step. Kustomize emits Services
before workloads, so the names the configurations use resolve first, but it
emits the producer's Deployment before the two StatefulSets, and a producer
that starts against a node A which is not answering RPC yet is the failure this
whole document keeps coming back to. At zero it is created and does not run.

This also creates the two CronJobs and the three NetworkPolicies, which the
older per-file sequence left out. They are not equivalent to each other.
`ark0-observe` runs every ten minutes and only reads: heights, hashes, peer
counts and pod readiness, written to the observation volume. `ark0-soak` runs
every six hours and does change the chain. It loads a wallet on node B, asks
node A to fund an address, and broadcasts a spend of it, so it produces
transactions that the producer then mines. On a network that is still coming
up it will fail against nodes that do not answer yet, which is harmless, but it
is not an observer and should not be read as one. Suspend it if a bring-up is
going to sit half-finished:

```bash
sudo k3s kubectl -n ark0 patch cronjob ark0-soak -p '{"spec":{"suspend":true}}'
```

and set it back to `false` the same way once both nodes are up. Re-applying
does not clear it: `70-soak.yaml` does not declare `suspend`, so an apply has
no opinion about a field it never set, and a suspended CronJob stays suspended
through every apply until somebody unsuspends it.

`terminationGracePeriodSeconds: 180` mirrors the units' `TimeoutStopSec=180`:
`bitcoind` has to finish flushing before the kubelet gives up on it.

Each node's start-up probe calls `bitcoin-cli uptime` against itself, so a node
counts as ready only once it answers RPC, and `rollout status` waits for a
chain that is still loading rather than returning the moment the process
starts. Wait for both, then confirm the volumes were adopted rather than
recreated:

```bash
sudo k3s kubectl -n ark0 rollout status statefulset/nodea --timeout=300s
sudo k3s kubectl -n ark0 rollout status statefulset/nodeb --timeout=300s
sudo k3s kubectl -n ark0 get pvc      # data-nodea-0 and data-nodeb-0, Bound, no new claims
```

### 6. Start the block producer

Only after the previous step's `rollout status` has returned for node A. The
producer exists already; the ordinary overlay is the same set with
`replicas: 1`, so applying it is what starts it.

```bash
sudo k3s kubectl apply -k contrib/k3s/overlays/root-sockets
sudo k3s kubectl -n ark0 rollout status deployment/ark0-miner --timeout=300s
sudo k3s kubectl -n ark0 logs -f deployment/ark0-miner      # one "height N <hash>" line per block
```

`strategy: Recreate` in that manifest is not a detail. A rolling update would
briefly run two producers against the same chain with the same signing key,
which is how a signet forks itself.

The producer image already carries `ark0.py`, `miner_loop.sh`, the upstream
signet miner and the functional test framework, and its entry point writes the
two `bitcoin-cli` client configurations from the environment: node A's RPC
endpoint, node B's, and the credentials from the Secret. So the producer needs
no data directory of its own and no cookie file. What it does need is the
`ark0` wallet, which lives in node A's data directory and arrived with it in
step 4; the producer never reads `signer.wif`.

Its `$ARK0_HOME` is an `emptyDir`, so `ark0.py`'s evidence log does not survive
a restart. The loop writes nothing else, and the evidence pack on the host is
unaffected. Give it a volume if that log ever has to be kept.

### 7. Verify

```bash
A="sudo k3s kubectl -n ark0 exec nodea-0 -- bitcoin-cli -conf=/config/nodea.conf -datadir=/data"
B="sudo k3s kubectl -n ark0 exec nodeb-0 -- bitcoin-cli -conf=/config/nodeb.conf -datadir=/data"
$A getblockcount; $A getbestblockhash; $A getblockhash 0; $A getchaintips
$B getblockcount; $B getbestblockhash; $B getblockhash 0; $B getchaintips
$A getpeerinfo | grep '"addr"'
$A listwallets
$A -rpcwallet=ark0 getwalletinfo
$A getdeploymentinfo | grep -A4 p2mr
```

Against `~/ark0-premigration.txt`:

- the genesis hash of both nodes is unchanged;
- each node's block count is at least what it was, and never lower;
- if the miner is not running yet, `getbestblockhash` equals the recorded one on
  both nodes; once it is running, both nodes agree with each other and the
  count climbs by one roughly every 90 seconds;
- `getchaintips` shows exactly one tip with `status: active` on each node, and
  no `valid-fork` or `valid-headers` entry that was not there before;
- node A has one peer, node B, and node B has one peer, node A;
- `listwallets` contains `ark0` and its `getwalletinfo` reports the same key
  pool and transaction count as before;
- `getdeploymentinfo` still shows `p2mr` active at height 1.

Watch two full block intervals before calling it done:

```bash
for i in 1 2 3 4 5 6; do
  echo "$(date -u +%H:%M:%S) A=$($A getblockcount) B=$($B getblockcount)"
  sleep 45
done
```

The two counts may differ by one for a few seconds after a block; they must not
stay apart, and the hashes at equal heights must match:

```bash
H=$($A getblockcount); [ "$($A getblockhash $H)" = "$($B getblockhash $H)" ] && echo "tips agree"
```

### 8. Retire the host units

Only after the cluster nodes have produced and relayed blocks for a while.
Disabling and masking is what stops a reboot from starting a second producer on
the same chain, which is the one failure mode that cannot be undone.

```bash
sudo systemctl disable --now ark0-miner ark0-nodeB ark0-nodeA
sudo systemctl mask ark0-miner ark0-nodeB ark0-nodeA
rm -rf ~/ark0-migration        # the rendered ConfigMap; the Secrets are already shredded
```

Leave `~/.ark0` in place: it is the rollback, and it is 40 MB.

## Maintenance: every apply is a producer start

Read this before changing anything on a running network.

`50-miner.yaml` declares `replicas: 1`. Scaling the producer to zero by hand
does not change that declaration, so the next `kubectl apply -k` puts it back
and the producer starts. "Scale to nothing, apply, verify, scale back" reads
like a safe sequence and is not one: the apply in the middle undoes the first
step. This is ordinary Kubernetes behaviour, described in the
[declarative configuration guidance](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/declarative-config/),
and it bit the 2026-09-16 release roll, where the producer restarted mid-apply
and mined a block against a node A that was not answering RPC yet.

`strategy: Recreate` does not help. It prevents two producer revisions
overlapping; it has nothing to say about a replica count being restored.

So maintenance uses an overlay that declares the producer at zero:

```bash
K="sudo k3s kubectl"
M=contrib/k3s/overlays/root-sockets-maintenance

$K -n ark0 scale deployment/ark0-miner --replicas=0   # stop it, as its own step
$K -n ark0 rollout status deployment/ark0-miner --timeout=120s
$K -n ark0 get pods                 # no ark0-miner pod: the tip is frozen now
$K apply --dry-run=server -k $M     # always, before the real one
$K apply             -k $M          # producer declared at 0, so the scale stands
```

The order matters and the scale is not redundant. The overlay would stop the
producer by itself, but then the apply that stops it is the apply that carries
whatever else changed, and a node being replaced while the producer is still
terminating is the 13:21Z failure on 2026-09-16. Stopping first and declaring
zero second gives a tip that is frozen before anything else moves, and the
declaration is what stops a later apply from undoing the scale.

The tip stays frozen across further applies. Do the work: change images, apply
configuration through `ark0/render-config.sh`, restart a node, verify heights
and hashes against a reading taken before. Re-applying `$M` as many times as
needed is safe, because zero is what it declares.

Then start the producer, explicitly, as its own decision:

```bash
$K -n ark0 rollout status statefulset/nodea --timeout=900s   # node A answering first
$K -n ark0 scale deployment/ark0-miner --replicas=1
$K -n ark0 logs -f deployment/ark0-miner                     # one "height N <hash>" per block
```

Going back to `contrib/k3s/overlays/root-sockets` also restores `replicas: 1`,
so on a day with no maintenance the ordinary overlay both applies and starts
the producer. That is the whole difference between the two.

Two more things an apply does **not** carry, and which therefore have to be
done separately:

- **the node configuration**, because `10-configmap.yaml` is a template and is
  not in any kustomization. Use `ark0/render-config.sh`, which diffs first.
- **a node restart after a configuration change.** `bitcoind` reads its
  configuration at start-up, so an applied ConfigMap changes nothing until the
  pod is replaced. Do that with the producer already at zero.

### Rolling a workload onto a new build

Building an image no longer changes what anything runs. `build-image.sh`
imports under `<image>:<series>-<head12>`, a tag it has never written before
and will never write again, so a build adds content to containerd and leaves
every running pod naming exactly what it named. Moving a workload onto that
content is a separate, explicit edit, in the one file that records what runs.

**Stop the producer before changing anything, in its own step.** The edit and
the apply are one action, and the apply that carries a new node image is the
same apply that takes the producer away: done together, node A is being
replaced while the producer is still terminating, which is the race that put a
block against an unanswering node A at 13:21Z on 2026-09-16. The maintenance
overlay declaring zero is what makes stopping first stick, because the apply
that follows does not undo it.

```bash
K="sudo k3s kubectl"
M=contrib/k3s/overlays/root-sockets-maintenance

# 1. Freeze the tip. Nothing about the images has changed yet.
$K -n ark0 scale deployment/ark0-miner --replicas=0
$K -n ark0 rollout status deployment/ark0-miner --timeout=120s
$K -n ark0 get pods                                   # no ark0-miner pod
$K -n ark0 exec nodea-0 -- bitcoin-cli -conf=/config/nodea.conf -datadir=/data getbestblockhash
$K -n ark0 exec nodeb-0 -- bitcoin-cli -conf=/config/nodeb.conf -datadir=/data getbestblockhash

# 2. Now edit the pin. One entry per workload: p2mr-node-m0 is node A,
#    p2mr-node-m05 is node B, p2mr-miner is the producer, the probe and the
#    soak job together.
#    In a subshell: kustomize edit runs beside the file, and everything else
#    in this block is relative to the repository root.
( cd contrib/k3s/ark0 && kustomize edit set image \
      p2mr-node-m05=p2mr-node:v31.1-p2mr-m05-<head12> )

# 3. Apply it. The producer is already gone and this declares it at zero, so
#    the only thing moving is the node.
$K apply --dry-run=server -k $M
$K apply             -k $M
$K -n ark0 rollout status statefulset/nodeb --timeout=900s

# 4. Verify against the two hashes from step 1, then start the producer.
$K apply -k contrib/k3s/overlays/root-sockets
```

The edit in step 2 is the same change typed into the `newTag:` of that entry in
`kustomization.yaml`, which is all `kustomize edit` does and needs no extra
tool.

`rollout restart` is no longer part of it. The reason it used to be needed was
that a rebuild changed a tag's content without changing its string, so the pod
spec was identical and the apply was a no-op. An immutable tag changes the
string, so the apply is the rollout.

**Rolling back is the same edit with the previous tag, in the same order.**
Every roll below records the tag it moved off, and that tag still names the
same content it named on the day, because nothing overwrites a tag any more.
Stop the producer and read the tip first, exactly as above, then:

```bash
( cd contrib/k3s/ark0
  kustomize edit set image p2mr-node-m05=p2mr-node:v31.1-p2mr-m05-a64bad420d00 )
```

and apply the maintenance overlay, wait for the node, and start the producer
with `root-sockets`. A rollback is a roll in the other direction and has the
same race in it.

Two properties this relies on. A tag is only as immutable as containerd's copy
of it, so an image the roll table names must not be removed while it is
somebody's way back; `README.md` has the pruning rule. And the edit is in the
base, so both overlays inherit it and cannot disagree about what a workload
runs.

## Rollback

The host data directories are copied, never moved, so the host can take the
network back. The cost is the blocks the cluster mined in the meantime: the
host's copy is a strict prefix of the cluster's chain, so restarting the host
nodes from their own data would mine a competing branch from an older tip.

Stop the cluster side first, always, and in that order: the producer by itself,
confirmed gone, and only then an apply. The maintenance overlay would stop it
too, but an apply is a whole-set reconciliation, and if a pin or a pod spec in
this checkout differs from what the cluster is running, that same apply starts
replacing nodes while the producer is still terminating. That is the race the
roll recipe is written around, and a rollback is not exempt from it.

```bash
K="sudo k3s kubectl"
$K -n ark0 scale deployment/ark0-miner --replicas=0
$K -n ark0 rollout status deployment/ark0-miner --timeout=120s
$K -n ark0 get pods                          # no ark0-miner pod: the tip is frozen
# Now the apply, whose job here is to declare zero so nothing below restarts it.
$K apply --dry-run=server -k contrib/k3s/overlays/root-sockets-maintenance
$K apply             -k contrib/k3s/overlays/root-sockets-maintenance
$K -n ark0 scale statefulset/nodea statefulset/nodeb --replicas=0
$K -n ark0 get pods                          # none
```

The seed pod applied two steps below is a plain `-f`, so it cannot restore the
producer. Applying any overlay other than the maintenance one during a rollback
would.

Then copy the chain back, so the host resumes from the tip the cluster reached
instead of from where it left off. Re-create the helper pod and reverse the
direction:

```bash
sudo k3s kubectl apply -f contrib/k3s/ark0/25-seed-pod.yaml
sudo k3s kubectl -n ark0 wait --for=condition=Ready pod/ark0-seed --timeout=300s
mv ~/.ark0/nodeA/signet ~/.ark0/nodeA/signet.pre-migration
mv ~/.ark0/nodeB/signet ~/.ark0/nodeB/signet.pre-migration
sudo k3s kubectl -n ark0 exec ark0-seed -- tar -C /a -cf - ./signet | tar -C ~/.ark0/nodeA -xf -
sudo k3s kubectl -n ark0 exec ark0-seed -- tar -C /b -cf - ./signet | tar -C ~/.ark0/nodeB -xf -
sudo chown -R "$(id -un):$(id -gn)" ~/.ark0/nodeA/signet ~/.ark0/nodeB/signet
sudo k3s kubectl -n ark0 delete pod ark0-seed
sudo systemctl unmask ark0-nodeA ark0-nodeB ark0-miner
sudo systemctl start ark0-nodeA ark0-nodeB
# check the height matches what the cluster last reported, then:
sudo systemctl start ark0-miner
```

If the cluster never produced a block, the copy back is unnecessary: unmask and
start the units, and the `.pre-migration` directories are not needed at all.

Removing the cluster side entirely, once the host is confirmed healthy:

```bash
sudo k3s kubectl delete namespace ark0        # takes both 20Gi volumes with it
```

## Things that will bite

- **Two producers.** Exactly one `ark0.py mine` loop may exist at any moment.
  The window to watch is between step 6 and step 8, when the host units are
  stopped but not yet masked and a reboot would start them.
- **An apply starts the producer.** `replicas: 1` is declared in
  `50-miner.yaml`, so scaling to zero by hand survives exactly until the next
  `apply -k`. Every apply during maintenance goes through
  `contrib/k3s/overlays/root-sockets-maintenance`, which declares zero. The
  section above spells out the sequence.
- **An apply does not change the node configuration, and changing it does not
  restart a node.** `10-configmap.yaml` is a template and is in no
  kustomization; it goes through `ark0/render-config.sh`. `bitcoind` reads its
  configuration at start-up, so an applied ConfigMap does nothing until the pod
  is replaced.
- **A different binary.** The node image must be built from the same patch
  series as the chain it extends. A node enforcing different consensus rules
  will reject the other's blocks, and on a two-node network that is a permanent
  split. The producer image is built from the same tree and carries the copies
  of `ark0.py` and `miner_loop.sh` in `contrib/k3s/ark0/producer/`, so it has to
  be rebuilt when either script changes in the repository.
- **DNS caching.** `connect=ark0-nodea:38433` is resolved when node B dials. If
  node A's pod is replaced and takes a new IP, node B reconnects to the new one
  on its next attempt, but an established connection to the old address is
  dropped first; a minute of no peer after a node A restart is expected, longer
  is not.
- **Ownership on a populated volume.** The `chown -R` in step 4 has to happen
  and has to match the overlay step 5 applies, or `bitcoind` starts against
  files it cannot open and exits. Under `root-sockets` that is `0:0`; under
  the unprivileged base it is `10000:10000`. Running as root does not make
  the mismatch survivable, because the container drops every capability and
  so has no `DAC_OVERRIDE`.
- **Nothing is exposed.** There is no NodePort and no Ingress. Reaching an RPC
  from the host is `kubectl exec`, or
  `sudo k3s kubectl -n ark0 port-forward nodea-0 38432:38432` for a session.

## Executed on 2026-09-16

Carried out between 10:51Z and 11:16Z, on the same node the build namespace
runs on. The network was moved without losing a block: the chain the cluster
extends is the one the host left, from the same genesis, with the same wallet.

### Image parity, settled before anything was stopped

The host's `~/bitcoin-p2mr` is not ten commits on top of `v31.1`. It is one,
`3d070af p2mr: integrated (squashed for build)`, so the commit count this plan
suggests comparing says 1 on the host and 10 in the cluster and proves nothing.
The content does: the two trees hash to the same git tree object.

| | host `~/bitcoin-p2mr` | cluster build |
|---|---|---|
| HEAD | `3d070af`, one squashed commit | `37752a2`, ten commits |
| commits past `v31.1` | 1 | 10 |
| `git rev-parse HEAD^{tree}` | `2a416c8befaab7e01762d148b6f0f181f412181f` | `2a416c8befaab7e01762d148b6f0f181f412181f` |
| `bitcoind -version` | `v31.1.0` | `v31.1.0` |

Identical trees, so identical consensus code, whatever the history above them
looks like. That is the check worth repeating; the commit count is not.

The build was Job `p2mr-build-m0-parity`: the ordinary build Job with `WORK`
pointed at `/work/m0` and `/work/patches/master` copied to
`/work/m0/patches/master`, so it cloned, patched and built in its own tree and
never touched the M0.5 tree at `/work/src` — confirmed afterwards, still
`d879208`, 28 commits, tree `07fd5ba3e2619c6a4cad3d070cd9e664497b1300`. It took
3 m 26 s: ten patches applied, `test_bitcoin` 740 cases passed, and
`feature_p2mr.py`, `feature_p2mr_signet.py` and `p2p_segwit.py` passed.

The image was built from `/work/imgctx/node-m0`, a copy of `Dockerfile.node`
beside that build's `bin/`, with the same kaniko pod template and the same
`ctr images import`.

| Tag | Built from | Runs |
|---|---|---|
| `p2mr-node:v31.1-p2mr-m0` | the ten consensus patches | `nodea-0` |
| `p2mr-node:v31.1-p2mr-m05` | M0.5; an alias of the existing `p2mr-node:v31.1-p2mr` | `nodeb-0` |
| `p2mr-node:v31.1-p2mr` | unchanged, kept | nothing |
| `p2mr-miner:v31.1-p2mr` | unchanged | `ark0-miner`, `ark0-observe` |

Node B on M0.5 is the point, not a compromise. M0.5 adds wallet and descriptor
code and changes no consensus rule, so it should accept every block the m0
producer makes. Divergence between the two pods is the result this network
exists to look for.

### Before and after

| | before, 11:05:36Z, producer stopped | after, 11:15:35Z |
|---|---|---|
| block count, node A | 1290 | 1295 |
| block count, node B | 1290 | 1295 |
| best hash, both | `0000024241a925e0f4041b06ee5a0b323aaf63995ffc78d7dd84726ff7b5d506` | `00000093f52ccbebc0bd9d991ace64e1cf14ae9d693e51a6b28f6c301e4a1114` |
| genesis, both | `00000008819873e925422c1ff0f99f7cc9bbb232af63a077a480a3633bee1ef6` | unchanged |
| chain tips, each node | 1 `active`, 3 `invalid` at height 127 | 1 `active`, 3 `invalid` at height 127 |
| `p2mr`, each node | buried, active, height 1 | buried, active, height 1 |
| connections, each node | 2 | 2 |
| `ark0` wallet on node A | loaded, txcount 1301, keypool 4000 | loaded, txcount 1306, keypool 4000 |

The three `invalid` tips at height 127 are the P2MR rejection demonstrations
from the network's first day. They were there before and they are there now,
and nothing `valid-fork` or `valid-headers` appeared.

An earlier reading at 10:54:11Z, with everything still running, had both nodes
agreeing at height 1282. That is the health check step 1 asks for, and it is
kept here as that. The 1290 row is what the cluster had to reproduce, and did.

Both nodes flushed and exited cleanly at 11:05:36Z, each logging
`ChainStateFlushed: block hash=0000024241a9...` and then `Shutdown done`, so
neither needed a reindex.

### The copy

`signet/` was streamed into each volume through `ark0-seed` and verified by
comparing a `sha256sum` manifest of every file on both sides, rather than by
size:

| | files | result |
|---|---|---|
| node A | 23 | every hash identical |
| node B | 21 | every hash identical |

`blk00000.dat` is `2025573c58d673ee56c9ef5f91c64f18acac7ec1b7321e530aba90269d10625a`
and `rev00000.dat` is `21f3db0831a50f85d2f69e669696f954cebb6ee370784bed4ea23a2e7c29ad08`
on both sides, with `chainstate/` matching file for file. `du -sb` differs by
about 30 KB between host and volume because the two filesystems size
directories differently; no file differs.

### Blocks after the cutover

The producer was applied at 11:08Z, and the first five blocks arrived at the
interval they were meant to:

| height | hash | seen at |
|---|---|---|
| 1291 | `0000026de7c946e5a9f7fa2bb00ade2737b6eb61069bd11b17cb3e38df444e2a` | 11:08:4xZ |
| 1292 | `00000324bfb9e69805d978eede48e46d9c2781e004e684468667e312dbfb239b` | 11:10:13Z |
| 1293 | `000000c300025d1e7159390e4ffa436bc923174dbfe7d7037d880c4c99268905` | 11:11:28Z |
| 1294 | `0000031eb69b4696cc14271ab43c657cfae8d34282a6f31b8b48684e9713d9ee` | 11:13:00Z |
| 1295 | `00000093f52ccbebc0bd9d991ace64e1cf14ae9d693e51a6b28f6c301e4a1114` | 11:14:31Z |

91 and 92 seconds between the last three, against a 90 second target, and node
B carried the same height and the same hash at every sample. The producer is
still spending from node A's `ark0` wallet: its transaction count went from
1301 to 1306 across those five coinbases.

### Observation

The crontab line running `~/.ark0/observe.sh` every ten minutes was removed,
and the other sixteen entries in that crontab were left alone.
`CronJob/ark0-observe` took over on the same period with the same twelve
fields, so the cluster's log reads as a continuation of `~/.ark0/observe.log`
rather than a new format. Its first line, from a manual run:

```
2026-09-16T11:10:33Z | 1292 | 1292 | 00000324bfb9e698 | 00000324bfb9e698 | 2 | 2 | 4 | 0 | Running | Running | Running
```

The last three fields were `systemctl is-active` on the host and are pod phases
now. The log is at `/observe/observe.log` on its own 1Gi claim.

### Where this departed from the plan

1. **The nodes run as root, not as uid 10000.** This cluster carries a BPF
   program attached to the root cgroup at `cgroup_inet_sock_create`, alongside
   `cgroup_inet4_bind` and `cgroup_inet6_bind` programs, and it denies
   `socket(AF_INET)` and `socket(AF_INET6)` to uid 10000. `bitcoind` starts and
   then fails every bind with `libevent: socket: Operation not permitted`,
   followed by `Unable to bind any endpoint for RPC server`. `AF_UNIX` is
   unaffected, and adding `NET_RAW`, `NET_ADMIN` and `NET_BIND_SERVICE` changes
   nothing, so it is not a capability problem and no change to the image can
   fix it. uid 0 and uid 1000 are both permitted. Root was chosen because it is
   the one verified end to end against these exact ports, binding
   `0.0.0.0:38433` and `0.0.0.0:38432` and reaching `Done loading`. `fsGroup`
   went with it, and the seeded chain was chowned `0:0` rather than
   `10000:10000`. This is a property of the cluster, not of this workload: it
   will bite anything else deployed here that runs as a high uid and opens a
   socket.
2. **Two node images instead of one**, as described above.
3. **`rpcallowip=10.42.0.0/24`**, not `/16`. That is this node's actual
   `podCIDR`, which is what the plan says to match it to.
4. **The tar exclusion had to be widened** from `./signet/.lock` to `*/.lock`.
   There is a second lock file, `./signet/blocks/.lock`, which the first copy
   carried into node A's volume and the checksum comparison caught. That copy
   was wiped and redone rather than patched.
5. **A second, frozen reading was taken** after stopping the producer and
   before stopping the nodes. The step 1 reading is taken while blocks are
   still being made, so it is stale by the time anything stops. The frozen
   reading is what the cluster was held to.
6. **The units were disabled but not masked.** Disabling removes the
   `multi-user.target.wants` symlinks, which is what stops a reboot from
   starting a second producer. Masking would also have to be undone before the
   rollback below could run.
7. **The observation log lives on its own claim**, not in node A's data
   directory. A second writer inside a running `bitcoind`'s datadir buys
   nothing, and a separate claim can be rotated or deleted without touching the
   chain.

### What is left on the host

`ark0-miner`, `ark0-nodeA` and `ark0-nodeB` are `inactive` and `disabled`, with
their unit files still in `/etc/systemd/system/`. `~/.ark0` and
`~/bitcoin-p2mr` are untouched: the host tree is still `3d070af`, and the two
data directories hold the chain as it stood at height 1290. Nothing from this
migration runs on the host, and the only `bitcoind` processes on the machine
are the two inside the pods. `~/k3s-build-tmp/` was deleted.

### Rolling back

None of this is needed unless the cluster side has to give the network up. The
host's copy stops at 1290, so the chain has to come back out of the volumes
first, or every block since is lost.

Stop the cluster side, producer first:

```bash
K="sudo -n k3s kubectl"
$K -n ark0 scale deployment/ark0-miner --replicas=0
$K -n ark0 rollout status deployment/ark0-miner --timeout=120s
$K -n ark0 scale statefulset/nodea statefulset/nodeb --replicas=0
$K -n ark0 get pods
```

Take the chain back out:

```bash
$K apply -f contrib/k3s/ark0/25-seed-pod.yaml
$K -n ark0 wait --for=condition=Ready pod/ark0-seed --timeout=300s
mv ~/.ark0/nodeA/signet ~/.ark0/nodeA/signet.pre-migration
mv ~/.ark0/nodeB/signet ~/.ark0/nodeB/signet.pre-migration
$K -n ark0 exec ark0-seed -- tar -C /a -cf - ./signet | tar -C ~/.ark0/nodeA -xf -
$K -n ark0 exec ark0-seed -- tar -C /b -cf - ./signet | tar -C ~/.ark0/nodeB -xf -
sudo chown -R "$(id -un):$(id -gn)" ~/.ark0/nodeA/signet ~/.ark0/nodeB/signet
$K -n ark0 delete pod ark0-seed
```

Hand the network back to systemd, checking the height before the producer
starts:

```bash
sudo systemctl enable ark0-nodeA ark0-nodeB ark0-miner
sudo systemctl start ark0-nodeA ark0-nodeB
~/bitcoin-p2mr/build/bin/bitcoin-cli -datadir=$HOME/.ark0/nodeA -signet getblockcount
sudo systemctl start ark0-miner
```

Put the host probe back by restoring the crontab line that runs
`~/.ark0/observe.sh` every ten minutes, then, only once the host is confirmed
healthy and never before:

```bash
sudo k3s kubectl delete namespace ark0
```

which takes all three volumes with it.

Skipping the copy back is only safe if the cluster produced no block at all,
and it produced one within a minute of starting. Running the host units and the
pods against the same chain at the same time puts two producers on one signing
key, which is the single mistake this plan exists to avoid.

## Executed on 2026-09-16 (release roll)

The second run of the day. The morning moved the network into the cluster on
the images that already existed; this one rebuilt node B and the producer from
the hardened tooling on `infra` and rolled the live workloads onto them.
Carried out between 13:10Z and 13:41Z, on the same node. No block was lost and
the chain was never reorganised: the tip the network was frozen at is still on
both nodes at the same hash.

### The build

Job `p2mr-build-m05-release-0916`, run id `m05-release-0916`, 13:10:05Z to
13:13:45Z. Fresh clone of `v31.1` at `9be056a`, both patch sets, `git am`
applied all thirty patches without a conflict. The ConfigMap the Job runs was
republished from this tree first: the copy on the cluster was the pre-hardening
`build.sh`, which published binaries before the tests ran and wrote no
provenance.

| Phase | Result | Wall time |
|---|---|---|
| clone `v31.1` | ok | 3 s |
| checksums, both sets | ok | — |
| `git am` 10 + 20 patches | ok | 1 s |
| cmake configure | ok | 14 s |
| cmake build `-j32` | ok | 3 s |
| `test_bitcoin --report_level=detailed` | 737 of 743 passed, 5 skipped | 56 s |
| functional, the 7 requested tests, `-j16` | 7 passed, 0 failed, 0 skipped | 22 s |
| functional, default set, `-j16` | 273 passed, 0 failed, 17 skipped | 2 m 01 s |
| total | ok | 3 m 40 s |

The three second build is a full compile and link of 483 translation units at a
100 % ccache hit rate, not a skipped step: `build.log` carries every
`Building CXX object` line through `[100%] Linking CXX executable`, and
`ccache.log` reads `Hits: 483 / 483 (100.0%)`. A hit rate that high is itself a
result, because the cache was filled by a different run of the same series: it
says this tree preprocesses to exactly what the earlier one did.

The seven were `wallet_p2mr.py`, `wallet_p2mr_multisig.py`,
`wallet_p2mr_timelock.py`, `wallet_p2mr_signet.py`, `feature_p2mr.py`,
`feature_p2mr_signet.py` and `rpc_psbt.py`. `rpc_psbt.py` is in the list
because the M0.5 series changes PSBT sighash handling for P2MR inputs and the
default set alone would have run it without anyone looking. The 17 default
skips are the usual ones: USDT tracepoints, `interface_zmq.py`, the tests that
want a previous release downloaded, and `tool_bench_sanity_check.py`.

`PROVENANCE.txt` beside the published binaries records the series:

```
result              : pass
run id              : m05-release-0916
patch sets          : master m05
patches applied     : 30
head                : 69ab43d0fa792ae9beb10c277f830fe35d03ddda
commit list sha256  : bdde3e4dd50f4136c21eb94b60451d725104d49c59fe68b431a225df847b5c87
```

Toolchain, from the builder image: `ubuntu:24.04@sha256:224a1869...`, cmake
3.28.3, g++ 13.3.0, python 3.12.3.

### Images

Neither `build-image.sh` invocation named a tag. The node tag came out of
`PROVENANCE.txt` by way of `node_image_for_patch_sets`, which is what it is
for. This was the first build of the producer image from `ark0/producer/`
rather than from an operator's home directory, and `stage-miner.sh` read
nothing outside this repository and the work volume.

| Workload | Tag | containerd manifest digest | imageID as the kubelet reports it |
|---|---|---|---|
| `nodea-0` | `p2mr-node:v31.1-p2mr-m0`, unchanged | `sha256:b120da303df805d13bc52e0ed9ab1f41173f61172f4c2a299421bf7fac66305f` | `sha256:44faa26d7d2980292d402d5d7329f4c8e8699a18c10e3c35e8395fd52c224593` |
| `nodeb-0` | `p2mr-node:v31.1-p2mr-m05`, rebuilt | `sha256:5fe7f4330940cef279a88e976a1c258c739120ef534a973fcfe9fc5f8e45bbaf` | `sha256:3fcf5dfdc2f61f8c11a62d0f859258250cbdb964e1988b217f159b381ffb4853` |
| `ark0-miner`, `ark0-observe`, `ark0-soak` | `p2mr-miner:v31.1-p2mr`, rebuilt | `sha256:7946a77fe2496e0047134840282597dc9931e1d7000c4ff3cf13b84ee6203949` | `sha256:bd8d5f7d5acf75e5d26a70fd9a5f7c99ad39e25462a0cd5cca3286a43ba454bd` |
| nothing | `p2mr-node:v31.1-p2mr`, kept | `sha256:1e2305cbdf9ceed81a15a4cb570b7886f0ec7fb7c3dee93f62ceaf23220108d9` | node B's rollback image |
| nothing | `p2mr-miner:v31.1-p2mr-pre-release`, kept | `sha256:2384d8d3c799ab8bf6eab5ef6a1f06f9ce080cb91675d78199625d76c9bbc702` | the producer's rollback image |

The two node digests differ, which is the check worth making: equal digests
would mean one build had been tagged twice and node B was not running M0.5 at
all. The morning's `-m05` was an alias of `p2mr-node:v31.1-p2mr`; it is a
separate image now, and that older tag is left in place as the way back.

Both images were started in a throwaway pod before anything live was touched.
The node image printed `Bitcoin Core daemon version v31.1.0`. The producer
image printed the `ark0.py` usage, which means the signet miner module loaded
and every `test_framework` import it pulls in resolved.

#### A content check that does not depend on digests

Digests say two images are different. They do not say which series each one
carries. `tmr()` does, because it exists only in the M0.5 series:

```
node B: tmr(pk(...)) -> error -5, "a single-leaf tree is anyone-can-spend ...
                        Use at least two leaves"
node A: tmr(pk(...)) -> error -5, "is not a valid descriptor function"
```

Node B knows the descriptor and rejects a one-leaf tree for the right reason.
Node A has never heard of it. That is the difference the two tags claim, tested
against the running pods rather than inferred from the build.

### Before and after

The first reading is the health check this plan's step 1 asks for, taken with
the producer still running. The frozen reading is the one the roll was held to.

| | health check, 13:19:46Z | frozen, 13:20:25Z, producer stopped | after the apply, 13:22:02Z | 13:40:59Z |
|---|---|---|---|---|
| block count, node A | 1374 | 1375 | 1376 | 1388 |
| block count, node B | 1374 | 1375 | 1376 | 1388 |
| best hash, both | `000000fddd9cb0bab1efd9159bb8addf940ca282819074e6e6640d13094c9a2e` | `0000033655c2ee7c4542a99ef1bcb4cb9208fc0559a193236985a77a22da2c39` | `000001496d10d545d6f39f4511f960061b9482b4aa1ccf04f8f85ac16e2364ef` | `0000028be02550eb59008681c48b55f477bf23b1e09d3e977d5c596d4e32ed6a` |
| genesis, both | `00000008819873e925422c1ff0f99f7cc9bbb232af63a077a480a3633bee1ef6` | unchanged | unchanged | unchanged |
| chain tips, each node | 1 `active`, 3 `invalid` at 127 | 1 `active`, 3 `invalid` at 127 | 1 `active`, 3 `invalid` at 127 | — |
| `p2mr`, each node | buried, active, height 1 | buried, active, height 1 | buried, active, height 1 | — |
| connections, each node | 2 | 2 | 2 | 2 |
| `ark0` wallet on node A | — | txcount 1399, keypool 4000 | — | txcount 1414 |

The frozen tip is the value that matters, and it survived the roll: after both
pods were replaced, `getblockhash 1375` on each node still returned
`0000033655c2ee7c4542a99ef1bcb4cb9208fc0559a193236985a77a22da2c39`. Nothing was
reorganised; the chain only grew. Both nodes report `/Satoshi:31.1.0/`, and the
three `invalid` tips at height 127 are the first day's P2MR rejection
demonstrations, still there, with nothing `valid-fork` or `valid-headers`
alongside them.

### Blocks after the roll

| height | hash | seen at |
|---|---|---|
| 1376 | `000001496d10d545d6f39f4511f960061b9482b4aa1ccf04f8f85ac16e2364ef` | before 13:21:29Z |
| 1377 | `0000034f2db04bafb5732985b6281671bbcf052b7598a809de773b1886589286` | 13:22:34Z |
| 1378 | `00000186934b5d1db502db98f74b84f2ef9c7a78608e6c09ef5a48abd562b26e` | 13:24:07Z |
| 1379 | `00000318653843ad55cef02d9c22429506a64300e10d6ae962d2005fdd7c2a6e` | 13:25:38Z |
| 1380 | `000002a6fef104f279bb1a886304b5b8ff99968dea3a17fddc4bda4fb06a73b2` | 13:27:10Z |
| 1381 | `000000d329b34cf2440cc7a6911a8bc15ad3bc5e632cf42cc0dd52e81fd5b13d` | 13:28:43Z |

93, 91, 92 and 93 seconds between the last five, against a 90 second target,
and node B carried the same height and the same hash at every sample. Node A's
`ark0` wallet went from txcount 1399 to 1414 over the run, so the producer on
the new image is still spending from the same wallet.

### The soak

`ark0-soak-release-0916`, created from the CronJob by hand, on the new producer
image. It funds from node A and spends from node B, so it exercises the M0.5
wallet code in the image this roll installed:

```
2026-09-16T13:30:34Z | b9e28edfda598fb2115dba3865d9831f38febed2bb697af257dce76061587611 | f2141a42c1653b17cbe25ad1dbf74815ba7d94fbebd7303aa7385122f422b12c | 1383/1384 | ok (index 103, vsize 117, sig 64B, leaf 34B, control 33B, dims PASS)
```

`dims PASS` is the part that is new today. The job used to log the witness
dimensions; it asserts them now, so a spend that confirmed but paid for a
bigger control block or a more expensive leaf fails the Job instead of being
written down as a success. 64, 34 and 33 bytes are a Schnorr signature over
`SIGHASH_DEFAULT`, an `OP_PUSHBYTES_32 <xonly> OP_CHECKSIG` leaf, and a depth
one control block, which is the two leaf tree the job imported.

### Observation

The first line the probe wrote after the roll, from the 13:40Z run:

```
2026-09-16T13:40:00Z | 1388 | 1388 | 0000028be02550eb | 0000028be02550eb | 2 | 2 | 4 | 0 | Running | Running | Running | ok | ok | ready:yes restarts:0 | ready:yes restarts:0 | ready:yes restarts:0
```

Twelve fields became seventeen. The two `ok` are an explicit `uptime` against
each node, so a wedged node is told apart from one that is merely behind, and
the three `ready:... restarts:...` pairs are what a bare pod phase could not
show: a pod stays `Running` while its container fails readiness, and it stays
`Running` across a crash loop.

### Where this departed from the plan

1. **`kubectl apply -k` restarts the producer.** `50-miner.yaml` declares
   `replicas: 1`, so the apply put the replica count back and the producer
   started while node A was still coming up. It mined 1376 against a node that
   was not answering yet, logged one `getblocktemplate` failure, retried
   fifteen seconds later and succeeded. No harm was done: exactly one producer
   existed throughout, and the retry is the loop working as designed. But the
   sequence "scale to nothing, apply, verify, scale back" cannot hold across an
   apply as written. The producer was scaled back to nothing, the nodes were
   verified against the frozen reading, and only then was it scaled up again.

   **Since fixed.** `contrib/k3s/overlays/root-sockets-maintenance` declares the
   producer at zero, so an apply during maintenance keeps it stopped rather than
   starting it. The maintenance section above and the rollback below both use
   it, and starting the producer is now a separate command. Disclosing this
   hazard was not enough while the written instructions still reproduced it.
2. **The two configuration placeholders were filled from the cluster, not the
   host.** `~/.ark0` was out of bounds for this run, and the values are already
   in the live `ark0-conf`, which is the better source anyway because it is
   what the running nodes actually read. `rpcallowip` was set to the live
   `10.42.0.0/24` rather than the file's `/16`. `kubectl diff -k` then reported
   `configmap/ark0-conf unchanged`, which is the check that the substitution
   reproduced the running configuration exactly rather than approximately.
   Applying the file as it stands in the repository would have overwritten both
   node configurations with `REPLACE_WITH_SIGNET_CHALLENGE`.

   **Since fixed.** The template is out of every kustomization, so no apply can
   reach it, and `ark0/render-config.sh` does by script what was done by hand
   here: reads the values from the live ConfigMap with `--from-live`, refuses a
   placeholder or an implausible value, takes `rpcallowip` from the cluster's
   own pod CIDR rather than the file's wider `/16`, and shows the diff before
   anything is applied. Run against this cluster it reports no difference.
3. **Node B's tag did not change, so the apply alone would not have replaced
   the pod.** The spec said `p2mr-node:v31.1-p2mr-m05` before and after; only
   what the tag points at changed. What recreated both node pods was the
   overlay's other change, the container level `securityContext` that the base
   gained today, and a recreated pod resolves the tag afresh. Rebuilding an
   image behind an unchanged tag is not by itself a rollout, and on a day
   without that securityContext change it would have needed a
   `rollout restart`.
4. **A rollback reference was made before the producer image was rebuilt.**
   `p2mr-miner:v31.1-p2mr` was the only name the running producer had, and
   rebuilding it replaces the only way back. `p2mr-miner:v31.1-p2mr-pre-release`
   now names the morning's image. It is a second name for content that was
   already there; nothing running was retagged.
5. **Three NetworkPolicies were created**, because they are in the base now and
   were not on the cluster. Whether this cluster enforces them was not
   re-tested; as of this morning kube-router could not program its ipsets here,
   so treat them as declared rather than enforced until the check in
   `ark0/README.md` says otherwise.
6. **The M0.5 patch count in the tooling's prose was wrong**, eighteen where
   the series is twenty. Corrected in the same commit as this record, except in
   the 2026-09-15 verification record in `README.md`, which describes a run that
   really did apply 28 patches and is left as the historical reading it is.

### Rolling back this roll

Not the migration rollback above, which hands the network back to the host.
This is the smaller one: undo the images and keep the network in the cluster.

```bash
K="sudo -n k3s kubectl"
# Declare the producer at zero rather than scaling it, so that any apply
# between here and the last line leaves it stopped.
$K apply -k contrib/k3s/overlays/root-sockets-maintenance
$K -n ark0 rollout status deployment/ark0-miner --timeout=120s
$K -n ark0 set image statefulset/nodeb bitcoind=p2mr-node:v31.1-p2mr
$K -n ark0 set image deployment/ark0-miner miner=p2mr-miner:v31.1-p2mr-pre-release
$K -n ark0 rollout status statefulset/nodeb --timeout=900s
# Verify the tip against the reading taken before, then start the producer as
# its own decision.
$K -n ark0 scale deployment/ark0-miner --replicas=1
```

Node A is untouched by this roll and stays where it is. Do not re-apply the
overlay afterwards without repeating the image edits: the manifests name the
new tags, and an apply would put them straight back. That is true of the
maintenance overlay as well, which changes only the replica count.

## Executed on 2026-09-16 (final tree, node B re-roll)

The third run of the day, and the first to follow the maintenance sequence
rather than discover why one was needed. Node B was rebuilt from the final
32-patch tree and the producer image from the repository's updated `ark0.py`;
node A was not touched. Carried out between 14:36Z and 17:50Z, most of that
spent building and waiting rather than changing anything. This run is also the
live validation of `render-config.sh`, the maintenance overlay and the series
verification, all of which behaved as documented.

### The build

Job `p2mr-build-m05-rel2-0916`, run id `m05-rel2-0916`, 14:48:21Z to 14:52:40Z.
Fresh clone of `v31.1` at `9be056a`, both patch sets, `git am` applied all
thirty-two patches without a conflict.

| Phase | Result | Wall time |
|---|---|---|
| clone `v31.1` | ok | 4 s |
| checksums, both sets | ok | — |
| `git am` 10 + 22 patches | ok | 1 s |
| series identification | `master m05`, 32 commits | — |
| cmake configure | ok | 14 s |
| cmake build `-j32` | ok | 4 s |
| `test_bitcoin --report_level=detailed` | 737 of 743 passed, 5 skipped | 55 s |
| functional, the 9 requested tests, `-j16` | 9 passed, 0 failed, 0 skipped | 1 m 01 s |
| functional, default set, `-j16` | 273 passed, 0 failed, 17 skipped | 2 m 00 s |
| total | ok | 4 m 19 s |

The nine were the seven this series owns, `wallet_p2mr.py`,
`wallet_p2mr_multisig.py`, `wallet_p2mr_timelock.py`, `wallet_p2mr_signet.py`,
`feature_p2mr.py`, `feature_p2mr_signet.py` and `p2p_segwit.py`, plus
`rpc_psbt.py` and `wallet_taproot.py`. The last two are there because the
series touches PSBT sighash handling for P2MR inputs and the Taproot builder's
tweak initialisation, and neither is P2MR-named, so neither would be looked at
if only the P2MR tests were run.

Four seconds of build is 483 translation units at a 100 % ccache hit rate,
compiled and linked; `build.log` ends at `[100%] Linking CXX executable`.

The new phase is `series`. `build.sh` no longer takes `PATCH_SETS` on trust: it
compares the `git patch-id` of every commit above `v31.1` with the patch-id of
every patch file on the volume, and the job log reads `tree matches patch sets
'master m05' (32 commits)`. The provenance records the comparison rather than
the claim:

```
patch sets requested: master m05
patch sets verified : master m05
series verified     : yes
commits over tag    : 32
patches applied     : 32
head                : fb0dfacf55d8edffff04e04b782f76f24679a40c
commit list sha256  : 48cf3bab72754f58c1664a7f5ea72c9fbaf0fe07ed1d1256040937995cc5e979
```

`build-image.sh` refuses to derive a tag unless `series verified` is `yes`, so
the tag on the image below is a measurement of the source, not a label typed
beside it.

### Images

| Workload | Tag | containerd manifest digest | imageID as the kubelet reports it |
|---|---|---|---|
| `nodeb-0` | `p2mr-node:v31.1-p2mr-m05`, rebuilt | `sha256:a97926175af2bfdcd983564413642a2ce92c0dbc81d5711a5b6b3c5a195e6993` | `sha256:cc233058ee31ee037b2effd62bd3d7c8b7f021292be781a890d1024459d0772b` |
| `nodea-0` | `p2mr-node:v31.1-p2mr-m0`, untouched | `sha256:b120da303df805d13bc52e0ed9ab1f41173f61172f4c2a299421bf7fac66305f` | `sha256:44faa26d7d2980292d402d5d7329f4c8e8699a18c10e3c35e8395fd52c224593` |
| `ark0-miner`, `ark0-observe`, `ark0-soak` | `p2mr-miner:v31.1-p2mr`, rebuilt | `sha256:ed54f802502c0d7324e887b59c16169b2b7ccc41a1629e97a61cd3feecff2e6c` | `sha256:356c396122e24adb484500b72e62403dc263bb25df063ce546c24438784dc0c6` |
| nothing | `p2mr-miner:v31.1-p2mr-previous`, kept | `sha256:7946a77fe2496e0047134840282597dc9931e1d7000c4ff3cf13b84ee6203949` | the producer's rollback, made by the guard below |
| nothing | `p2mr-miner:v31.1-p2mr-pre-release`, kept | `sha256:2384d8d3c799ab8bf6eab5ef6a1f06f9ce080cb91675d78199625d76c9bbc702` | the 2026-09-16 morning producer |
| nothing | `p2mr-node:v31.1-p2mr`, kept | `sha256:1e2305cbdf9ceed81a15a4cb570b7886f0ec7fb7c3dee93f62ceaf23220108d9` | the 18-patch node build |

The producer image was rebuilt because `ark0/producer/ark0.py` changed:
`cmd_badblock` now asserts what it records, so a block refused for an unrelated
reason, or refused by node A while node B accepted it, fails instead of being
written into the evidence pack. `bitcoin-cli` and `bitcoin-util` hash the same
as in the previous release build, so that script is the only thing inside the
image that moved.

`shell.yaml` and `25-seed-pod.yaml` are pinned by digest now. Applying the
pinned `shell.yaml` did not restart the helper pod and did not change what it
runs: `ubuntu:22.04` on this node already resolved to
`sha256:3ba65aa2...`, which is the digest that was pinned. A pin that changes
nothing is the only kind worth trusting on the day it lands.

#### Node B's rollback image is the consensus build

The previous `-m05` image, the 20-patch build from the 13:16Z roll, no longer
has a name. `ctr images import` moves a tag onto new content in place, and
rebuilding `p2mr-node:v31.1-p2mr-m05` took the only name that content had while
`nodeb-0` was still running it. Recovering it was judged not worth the effort,
so node B's documented fallback is now `p2mr-node:v31.1-p2mr-m0`:

```bash
sudo k3s kubectl -n ark0 set image statefulset/nodeb bitcoind=p2mr-node:v31.1-p2mr-m0
```

Node B only verifies, so the consensus-only build keeps it validating every
block node A produces. What stops is the soak, because `tmr()` does not exist
in that build and the round trip has nothing to spend. That is a degraded
fallback rather than an equivalent one, and it is the price of having
overwritten the tag.

So that this cannot happen again, `build-image.sh` now protects a tag in use.
Before an import it asks the cluster whether any pod, by spec or by container
status, names the tag about to be replaced; if one does, it gives the current
content another name and only then imports. It is a new name for content
already on the node, so nothing running changes. Its first live use was the
producer build in this run:

```
==> p2mr-miner:v31.1-p2mr is in use by a pod; keeping its current content as p2mr-miner:v31.1-p2mr-previous
```

That first version kept one name, `<tag>-previous`, and it was not enough. On a
second rebuild it would be moved again while a pod was still running what it
named, deleting that content's last name and reintroducing the problem it was
written to solve. It also read a failed `ctr` or `kubectl` query as "nothing is
using this", which is the opposite of what an unanswerable question means here.

Since corrected. The guard now writes `<tag>-running-<digest12>` as well, whose
name contains the digest and therefore cannot be overwritten by a later
rebuild; moves `-previous` only when the content it names is not still running;
fails closed on any query error; and reads the saved reference back before
importing. The line above would now read
`keeping its current content as p2mr-miner:v31.1-p2mr-running-<digest12>`,
followed by `rollback reference ready`.

### The sequence, and whether it held

The order was the one in "Maintenance: every apply is a producer start" above,
and each step is recorded here because this run was its first use.

1. **`render-config.sh --from-live`** read the challenge, the reward address
   and `rpcallowip` off the running `ark0-conf`, refused nothing, and reported
   `no difference: the live configuration is exactly this render`. Nothing was
   applied, because nothing needed to be. A zero diff is the useful result: it
   says the template plus the live values reproduces the running configuration
   exactly, which is the property the old hand-edited substitution could only
   be assumed to have.
2. **The maintenance overlay** was dry-run first. Its only substantive change
   was `replicas: 1` to `replicas: 0` on the producer; the two StatefulSets
   reported `configured` solely because their last-applied annotation changed
   when `10-configmap.yaml` left the kustomization, with no field difference in
   the diff. Applying it stopped the producer and did not restart either node:
   both kept `startTime: 2026-09-16T13:20:55Z` and `restartCount: 0`.
3. **Re-applying it kept the producer at zero.** This is the claim the overlay
   exists to make, so it was tested rather than assumed: a second
   `kubectl apply -k` left `spec.replicas` at 0 and no miner pod running.
4. **Node B was restarted explicitly**, with `kubectl rollout restart
   statefulset/nodeb`. The tag string did not change, only what it points at,
   so an apply would not have replaced the pod. The previous roll got this for
   free from an unrelated `securityContext` change and the record said so; this
   time there was no such change and the restart had to be asked for.
5. **The producer was started by applying `root-sockets`**, which restores
   `replicas: 1`. Node A was confirmed to be answering RPC first.

### Before and after

| | health check, 17:33:48Z | frozen, 17:35:07Z, producer at zero | node B on the new image, 17:36:02Z | 17:49:31Z |
|---|---|---|---|---|
| block count, node A | 1535 | 1536 | 1536 | 1544 |
| block count, node B | 1535 | 1536 | 1536 | 1544 |
| best hash, both | `000001fbe0c3fbfe11f8860cf5cd7cf8ce4b07023a941f24e596c480d7883001` | `000001527535f8339a2da53e885761b865f23cfb8c27abc30f0eb584c00e270b` | `000001527535f8339a2da53e885761b865f23cfb8c27abc30f0eb584c00e270b` | `0000018df8f8fbc7e78c94766862b10b592e9249655e83c5400a1b3fcdbb3753` |
| genesis, both | `00000008819873e925422c1ff0f99f7cc9bbb232af63a077a480a3633bee1ef6` | unchanged | unchanged | unchanged |
| chain tips, each node | — | 1 `active`, 3 `invalid` at 127 | 1 `active` at 1536, 3 `invalid` at 127 | — |
| `p2mr`, each node | — | buried, active, height 1 | buried, active, height 1 | — |
| connections, each node | 2 | 2 | 1 | 2 |
| `ark0` wallet on node A | — | txcount 1562 | — | txcount 1572 |

Node B came back on the new image at the frozen tip exactly, hash for hash,
inside the fifteen minutes the rollback condition allows; it took about
twenty-five seconds. The single connection at 17:36:02Z is the expected half of
the pair: node A dials node B and node B dials node A, and only one had
re-established that soon after node B's pod was replaced. Both were back by the
first block.

`tmr()` was checked against both running pods again, because it is the only
test that says which series each image carries rather than that they differ:
node B rejects a one-leaf tree with the M0.5 explanation, node A answers that
`tmr` is not a valid descriptor function.

### Blocks after the roll

Header times from node A, which are the producer's own, rather than poll
timestamps.

| height | hash | header time | gap |
|---|---|---|---|
| 1536 | `000001527535f8339a2da53e885761b865f23cfb8c27abc30f0eb584c00e270b` | 17:33:58Z | the frozen tip |
| 1537 | `0000026d22e774d9ccf60208fb661fb916ee70d0cbf629c4ac439422fe927691` | 17:37:47Z | 229 s |
| 1538 | `0000001afc2aa4611e92f12af3ed3feacdf9e54666cf74e63efb4199842482c0` | 17:39:21Z | 94 s |
| 1539 | `000000de3ae07c93a1618ec187d5537c10383765b16ed354a3fb75b8b24771b5` | 17:40:52Z | 91 s |
| 1540 | `000001208ee038467e408eb3d43ac05f4788d8e5cf8897ba44853dd3ba4d15ef` | 17:42:31Z | 99 s |
| 1541 | `00000102b3f048cb643e3f153eababd39efba734a06f57deed151871660dceb5` | 17:44:09Z | 98 s |

The 229 second gap is the maintenance window itself, from the producer being
declared at zero at 17:34:18Z to `root-sockets` starting it again at 17:37:45Z.
That is what a deliberate pause looks like in the block times, and it is the
only gap: 94, 91, 99 and 98 seconds follow, against a 90 second target, and
node B carried the same height and the same hash at every sample.

### The soak

`ark0-soak-rel2-0916`, on the new producer image, funding from node A and
spending from node B's new build:

```
2026-09-16T17:46:30Z | fc96eb09df9c7188be2afcd190df5a0e9e607fba931ac43571f986a4356e0140 | e70fd57b0cbb9babcb4117247f050b48ef56bd2b18a735c3a4c2c9c359be8fdf | 1543/1544 | ok (index 104, vsize 117, sig 64B, leaf 34B, control 33B, dims PASS)
```

Same witness dimensions as the 13:30Z run, 64, 34 and 33 bytes, asserted rather
than logged. The two M0.5 patches added since that run change which control
block a signer keeps, and the dimensions not moving is exactly what should
happen: one leaf per control block is a constraint on which one is kept, not on
how large it is.

### Observation

```
2026-09-16T17:40:00Z | 1538 | 1538 | 0000001afc2aa461 | 0000001afc2aa461 | 2 | 2 | 4 | 0 | Running | Running | Running | ok | ok | ready:yes restarts:0 | ready:yes restarts:0 | ready:yes restarts:0
```

Both nodes at the same height and hash, both answering RPC, all three pods
ready with no restarts, four chain tips and an empty mempool.

### Where this departed from the plan

1. **The producer image was rebuilt**, which the instructions made conditional
   on `stage-miner.sh` inputs having changed. They had: `ark0/producer/ark0.py`
   gained the malformed-block assertions described above. It was rebuilt inside
   the maintenance window, while the producer was already at zero, so it cost
   no additional pause, and it was checked in a throwaway pod before anything
   used it.
2. **Node B's rollback is a different image than the one it replaced**, for the
   reason in the images section. `build-image.sh` gained the guard that
   prevents the next occurrence, and that guard ran for the first time in this
   run.
3. **Nothing else.** `render-config.sh` reported no difference, the maintenance
   overlay held the producer at zero across a second apply, neither node
   restarted from an apply, node B reached the frozen tip well inside the
   window, and the soak passed on the first attempt.

## Executed on 2026-09-16 (34-patch tree, node B re-roll)

The fourth run of the day and the second to follow the maintenance sequence.
Node B was rebuilt from the 34-patch tree; node A and the producer image were
not touched. Carried out between 18:36Z and 19:00Z. This run was also the live
exercise of the round-four tooling: the address decoder in
`ark0/render-config.sh`, the clean-tree requirement in `build.sh`, and the
`<tag>-running-<digest12>` guard in `build-image.sh`. All three did what they
were written to do, and the third had a defect that this run found.

### The build

Job `p2mr-build-m05-rel3-0916`, run id `m05-rel3-0916`, 18:37:18Z to 18:41:46Z.
Fresh clone of `v31.1` at `9be056a`, both patch sets, `git am` applied all
thirty-four patches without a conflict.

| Phase | Result | Wall time |
|---|---|---|
| clone `v31.1` | ok | 3 s |
| checksums, both sets | ok | — |
| `git am` 10 + 24 patches | ok | 1 s |
| series identification | `master m05`, 34 commits, clean | — |
| cmake configure | ok | 14 s |
| cmake build `-j32` | ok | 4 s |
| `test_bitcoin --report_level=detailed` | 737 of 743 passed, 5 skipped | 54 s |
| functional, the 9 requested tests, `-j16` | 9 passed, 0 failed, 0 skipped | 1 m 02 s |
| functional, default set, `-j16` | 273 passed, 0 failed, 17 skipped | 2 m 09 s |
| total | ok | 4 m 28 s |

The job log reads `tree matches patch sets 'master m05' (34 commits, clean)`,
and the provenance carries the clean-tree result as its own field:

```
patch sets requested: master m05
patch sets verified : master m05
series verified     : yes
series note         : clean tree, patch-ids match
commits over tag    : 34
patches applied     : 34
head                : b53af660dad6c50824468bf0f644a236a3eddbdc
commit list sha256  : bd67e1067845a344141eb226384ee769a8f0b97d9be4ccbed7456ddf6a5a4396
```

With `FRESH_CLONE=1` the tree is clean by construction, so this run exercised
the check rather than tested it. The case it exists for is `APPLY_PATCHES=0`
against a tree someone has since edited, which this run had no reason to
produce.

### Images

Only the node image was rebuilt. The producer image's inputs were compared
rather than assumed, and every one of them was unchanged:

| Input | Running image | This build |
|---|---|---|
| `contrib/signet/miner` + `test_framework`, hashed as a set | `24c3c7b7461fc933` | `24c3c7b7461fc933` |
| `bitcoin-cli` | `14f67ade8723fd31` | `14f67ade8723fd31` |
| `bitcoin-util` | `37cd17c0822cec61` | `37cd17c0822cec61` |
| `ark0.py`, `miner_loop.sh`, the entry point | match `ark0/producer/` at this commit | unchanged since `c675293` |

No patch in either set touches `contrib/signet/miner`, and the two patches
added since the 32-patch tree do not touch `test_framework/p2mr.py`, which is
the only framework file the series adds. So a rebuild would have produced the
same image, and the producer was left alone.

| Workload | Tag | containerd manifest digest | imageID as the kubelet reports it |
|---|---|---|---|
| `nodeb-0` | `p2mr-node:v31.1-p2mr-m05`, rebuilt | `sha256:a64bad420d00462db66c9ec28477c2df56d036c311a42b2f070a4115ddc266dd` | `sha256:790785687d86cb9cefbb0993cd729e019b283c3aa726ef3982a17e77219bc606` |
| `nodea-0` | `p2mr-node:v31.1-p2mr-m0`, untouched | `sha256:b120da303df805d13bc52e0ed9ab1f41173f61172f4c2a299421bf7fac66305f` | `sha256:44faa26d7d2980292d402d5d7329f4c8e8699a18c10e3c35e8395fd52c224593` |
| `ark0-miner`, `ark0-observe`, `ark0-soak` | `p2mr-miner:v31.1-p2mr`, not rebuilt | `sha256:ed54f802502c0d7324e887b59c16169b2b7ccc41a1629e97a61cd3feecff2e6c` | `sha256:356c396122e24adb484500b72e62403dc263bb25df063ce546c24438784dc0c6` |
| nothing | `p2mr-node:v31.1-p2mr-m05-running-a97926175af2`, new | `sha256:a97926175af2bfdcd983564413642a2ce92c0dbc81d5711a5b6b3c5a195e6993` | node B's rollback, immutable |
| nothing | `p2mr-node:v31.1-p2mr-m05-previous`, new | `sha256:a97926175af2bfdcd983564413642a2ce92c0dbc81d5711a5b6b3c5a195e6993` | the same content, movable name |

Node B has a real rollback again, for the first time since the tag was
overwritten at 13:16Z:

```bash
sudo k3s kubectl -n ark0 set image statefulset/nodeb \
    bitcoind=p2mr-node:v31.1-p2mr-m05-running-a97926175af2
```

That is the 32-patch M0.5 build, so unlike the `-m0` fallback recorded above it
keeps `tmr()` and keeps the soak running. The `-m0` image remains the second
fallback.

#### The guard stopped the run, and was right to, for the wrong reason

`build-image.sh` exited 141 between kaniko finishing and the import: a built
tarball on the volume, no image, and the tag still pointing at the old content.
141 is 128 plus SIGPIPE.

Four of the guard's helpers were written as `printf '%s\n' "$BIG_STRING" |
consumer`, where the consumer stops reading as soon as it has an answer:
`grep -q`, or `awk` with `exit` inside a rule. The producer is then killed by
SIGPIPE, `set -o pipefail` makes the pipeline report 141, and a function whose
answer was *yes* returns *failure*. It only appears once the data outgrows a
pipe buffer, which is why it read correctly: on this node `ctr images ls` is
122 KB, and `ctr_ref_digest` printed the right digest and still killed the
script.

That direction is harmless. The other one is not. `image_in_use` has the same
shape, is called as `if ! image_in_use "$image"`, and a 141 there reads as "no
pod is using this tag" — so on a cluster with enough pods to fill the buffer,
the function whose whole purpose is to keep a rollback reference would have
skipped it and said so cheerfully. The guard was designed to fail closed; a
pipeline status had turned one of its questions into fail-open.

Fixed in the same commit as this record: all four helpers now read a
here-string instead of a pipe, and none of them stops reading before end of
input, so neither the signal nor the status can arise. Re-run afterwards, the
guard did exactly what it documents:

```
==> p2mr-node:v31.1-p2mr-m05 is in use; keeping its current content as p2mr-node:v31.1-p2mr-m05-running-a97926175af2
==> rollback reference ready: p2mr-node:v31.1-p2mr-m05-running-a97926175af2
```

One weakness was left, and is recorded here because it was live during this
run. `digest_in_use` decides whether `-previous` may be moved, and it compared
the manifest digest that `ctr images ls` reports against the `imageID` values
the kubelet reports, which are config digests. Those never match, so the "a pod
is still running what `-previous` names" branch could not fire and `-previous`
always moved. The consequence was bounded: `-previous` means "the build before
this one", which is what its name says, and the immutable
`-running-<digest12>` reference is the one a rollback should use. Worth
correcting, not worth correcting mid-roll.

That correction was attempted, and the attempt was itself wrong: the loop that
was to resolve `crictl inspecti` output never resolved anything and appended a
stale digest twice, while logging lines that looked right. Review caught it,
not the tooling. Three further rounds found three further holes in the same
function. The guard was removed on 2026-09-17 and replaced by tags that are
never overwritten, which is recorded at the end of this document; the paragraph
above is left as the reading it was on the day.

### The sequence

1. **`render-config.sh --from-live`** reported `no difference: the live
   configuration is exactly this render`. Nothing was applied. The round-four
   decoder validated the reward address by bech32m checksum, human-readable
   part and witness program length rather than by regular expression, and
   accepted the live address; no value was echoed by any check that did not
   pass.
2. **The maintenance overlay** was dry-run first. Its only substantive change
   was the producer's `replicas: 1` to `replicas: 0`; the StatefulSets reported
   `configured` from annotation churn alone, with no field difference. Applying
   it stopped the producer and restarted neither node.
3. **Re-applying it kept the producer at zero**, tested again rather than
   assumed.
4. **Node B was restarted explicitly** with `kubectl rollout restart
   statefulset/nodeb`, because the tag string does not change when only its
   content does.
5. **`root-sockets` started the producer**, after node A was confirmed to be
   answering RPC.

### Before and after

| | health check, 18:48:35Z | frozen, 18:49:37Z, producer at zero | node B on the new image, 18:50:08Z | 19:00:37Z |
|---|---|---|---|---|
| block count, node A | 1581 | 1582 | 1582 | 1589 |
| block count, node B | 1581 | 1582 | 1582 | 1589 |
| best hash, both | `000001315154287d7aa33940d4757c50ced992c5561bd09c2e49fa1969fbb4e7` | `000001b05223b761a67d87a22e09eeb9a69288a72a84b6943c0e19423b3adcbc` | `000001b05223b761a67d87a22e09eeb9a69288a72a84b6943c0e19423b3adcbc` | `000002e52b922b362a30422c9e0bbab8068348aa9fea11ee8d7d359e618e7b02` |
| genesis, both | `00000008819873e925422c1ff0f99f7cc9bbb232af63a077a480a3633bee1ef6` | unchanged | unchanged | unchanged |
| chain tips, each node | — | 1 `active` at 1582, 3 `invalid` at 127 | — | — |
| `p2mr`, each node | — | buried, active, height 1 | buried, active, height 1 | — |
| connections, each node | 2 | 2 | 1 | 2 |
| `ark0` wallet on node A | — | txcount 1612 | — | txcount 1621 |

Node B reached the frozen tip about eleven seconds after its pod was replaced,
against a fifteen minute allowance. The single connection immediately
afterwards is the expected half of the pair and both were back by the second
block. `tmr()` was checked against both running pods again: node B rejects a
one-leaf tree with the M0.5 explanation, node A does not know the function.

### Blocks after the roll

| height | hash | header time | gap |
|---|---|---|---|
| 1582 | `000001b05223b761a67d87a22e09eeb9a69288a72a84b6943c0e19423b3adcbc` | 18:48:57Z | the frozen tip |
| 1583 | `0000015594e2bfa8e573f97ed330f9bb66cbe26448b3eff9b1bd191a4c247b93` | 18:50:28Z | 91 s |
| 1584 | `00000103ea6ca65bd895da56669b5dc4281a1b7b997407411888acd086206b70` | 18:52:01Z | 93 s |
| 1585 | `000002d8d97840be8a04b9d3d85875e292c6df37abd5231f60b52b31db15257e` | 18:53:33Z | 92 s |
| 1586 | `000001d4bd1f403bf9e6c57cb5bba32d6716cebdaf711903cb5b9259093d51ce` | 18:55:06Z | 93 s |
| 1587 | `000002fa67e4afaae4c198d224368e60745d44165931345fe1c9abc73c06f2f6` | 18:56:52Z | 106 s |

Unlike the 17:37Z roll there is no visible pause. The producer was down 107
seconds, from the maintenance apply at 18:48:39Z to `root-sockets` at
18:50:26Z, which fitted inside one block interval; node B's own restart took
eleven seconds of that. A maintenance window that leaves no gap in the block
times is what this sequence is for.

### The soak

`ark0-soak-rel3-0916`, funding from node A and spending from node B's new
build:

```
2026-09-16T18:58:09Z | 9c3d8d984b2bf04978e6158dac6313be34d3880423d2651d988cc3b506d21526 | ef9cc6d2cfb898ca7791689da2896137f3b0d33ae24fec1d9ed8632c648c445f | 1588/1589 | ok (index 106, vsize 117, sig 64B, leaf 34B, control 33B, dims PASS)
```

64, 34 and 33 bytes again. The two patches added since the 32-patch tree change
how a P2MR input's spent output is located when the input names only the
previous transaction, which is a lookup question rather than a witness one, so
unchanged dimensions are the expected result and not merely an acceptable one.

### Observation

```
2026-09-16T19:00:00Z | 1589 | 1589 | 000002e52b922b36 | 000002e52b922b36 | 2 | 2 | 4 | 0 | Running | Running | Running | ok | ok | ready:yes restarts:0 | ready:yes restarts:0 | ready:yes restarts:0
```

### Where this departed from the plan

1. **`build-image.sh` had to be fixed before the image could be imported**, for
   the SIGPIPE reason above. The fix is in the commit that carries this record,
   and the corrected guard then ran and produced node B's rollback reference.
2. **The producer image was not rebuilt.** Its inputs were compared file by
   file against the running image and every one matched, so the condition for
   rebuilding was not met.
3. **The M0.5 count in the tooling's prose was stale again**, at two different
   vintages: twenty in some files and twenty-two in others, against a series of
   twenty-four. Corrected wherever it describes the current series, and left
   alone inside the three dated records, which describe the series as it was on
   the day each was written.

## Executed on 2026-09-17 (the rollback guard replaced by immutable tags)

No workload changed. This entry records a change to the tooling and the
manifests, and the one manual step the cluster still needs before the next
apply.

### Why

`ctr images import` moves a tag onto new content in place. Rebuilding
`p2mr-node:v31.1-p2mr-m05` on 2026-09-16 while `nodeb-0` was running it took
away the only name that pod's image had, and the roll fell back to the `-m0`
build. The answer then was a guard: before importing, ask the cluster which
images are in use and copy those to a second name first.

That guard was rewritten four times and was wrong each time. It compared
manifest digests against config digests, so its "still running" branch could
not fire. It treated any `/` in a reference as meaning the reference was
already registry-qualified, so `rancher/…` never resolved. It read
`ctr images ls` through `grep -q` and `awk … exit`, so on an output larger than
a pipe buffer the producer took SIGPIPE, `pipefail` reported 141, and the
function whose answer was *yes* returned *failure* — in one place that read as
"no pod is using this tag". It filtered pods by `status.phase=Running`, which
omits a Pending pod whose container is running. Each of those was found in
review rather than by the tooling, and each fix was smaller than the next hole.

The question "what is running right now" has no answer that is both complete
and small. So the guard is gone, and with it `--check-guard`, the `-previous`
and `-running-<digest12>` names, and every use of `crictl` in this tooling.

### What replaced it

`build-image.sh` imports under one immutable tag per build:

```
p2mr-node:v31.1-p2mr-m05-<head12>
```

`<head12>` is the first twelve hex digits of the commit its `PROVENANCE.txt`
records, taken from the verified series; an unverified provenance still refuses
to build. The builder image, which has no provenance, is suffixed with the hash
of its Dockerfile. If the tag exists already the script compares digests and
exits 0 on a match and 1 on a difference. It writes no other tag, and reads,
moves and deletes nothing. Its last two lines are the tag and the digest.

Which build a workload runs is now a fact about `ark0/kustomization.yaml`,
whose `images:` block both overlays inherit. The workload files name an image
without a tag, so they are applied through `apply -k` and not `apply -f`.
Rolling and rolling back are one line each, under
[Rolling a workload onto a new build](#rolling-a-workload-onto-a-new-build).

### The initial values, and the one manual step

The three `newTag` values were taken from what the cluster is running today, so
that the first apply of the new manifests is a no-op in content:

| `images:` entry | Tag | containerd manifest digest |
|---|---|---|
| `p2mr-node-m0` | `p2mr-node:v31.1-p2mr-m0-b120da303df8` | `sha256:b120da303df805d13bc52e0ed9ab1f41173f61172f4c2a299421bf7fac66305f` |
| `p2mr-node-m05` | `p2mr-node:v31.1-p2mr-m05-a64bad420d00` | `sha256:a64bad420d00462db66c9ec28477c2df56d036c311a42b2f070a4115ddc266dd` |
| `p2mr-miner` | `p2mr-miner:v31.1-p2mr-ed54f802502c` | `sha256:ed54f802502c0d7324e887b59c16169b2b7ccc41a1629e97a61cd3feecff2e6c` |

Those three tags do not exist on the cluster yet. The content does, under the
old shared names, so the step is three aliases and no rebuild. **Run these
before the next `apply -k`, or the pods will go to `ImagePullBackOff` on tags
containerd does not have.** They were not run as part of this change:

```bash
sudo k3s ctr -n k8s.io images tag --local \
    docker.io/library/p2mr-node:v31.1-p2mr-m0 \
    docker.io/library/p2mr-node:v31.1-p2mr-m0-b120da303df8
sudo k3s ctr -n k8s.io images tag --local \
    docker.io/library/p2mr-node:v31.1-p2mr-m05 \
    docker.io/library/p2mr-node:v31.1-p2mr-m05-a64bad420d00
sudo k3s ctr -n k8s.io images tag --local \
    docker.io/library/p2mr-miner:v31.1-p2mr \
    docker.io/library/p2mr-miner:v31.1-p2mr-ed54f802502c
```

`--local`, and never `--force`. Without it the command goes through
containerd's transfer service, whose image store updates an entry that already
exists, so a command written to create three names can replace three instead
and exit 0 either way. With it, a destination that exists fails and changes
nothing, and the answer to that failure is to compare digests, not to force.
The three above were run at 20:35:50Z in their earlier form, without the flag;
all three destinations were absent, so the result was the same, but that was an
observation about the cluster rather than a property of the command.

Then confirm each new name reports the digest in the table above:

```bash
sudo k3s ctr -n k8s.io images ls \
  | awk '$1 ~ /p2mr-(node|miner):v31\.1-p2mr-.*[0-9a-f]{12}$/ { print $1, $3 }'
```

The match is on `$1` and not on the whole line, and there is no hyphen before
the twelve hex digits. Both matter, and the first version of this command had
neither: `$` anchored past the digest, size and platform columns, so it matched
nothing at all, and `-[0-9a-f]{12}` required a hyphen-separated component
between `v31.1-p2mr-` and the digits, which the producer tag
`v31.1-p2mr-ed54f802502c` does not have. A verification command that silently
matches nothing is worse than no command, because an empty result reads like a
clean one. If this prints fewer than three lines, the aliases are not there.

It also prints `p2mr-node:v31.1-p2mr-m05-running-a97926175af2`, which ends in
twelve hex digits by coincidence of naming. That is the old guard's reference
to the 32-patch build, still node B's way back to it, and it is expected here
until it is pruned.

A tag is content-addressed here, so these add names and change nothing a pod is
using. After them, the first `apply -k` replaces each pod spec's image string
with a tag naming the content that pod is already running. The pods are
recreated, because the string changed; they come back on the same build. Do it
inside the maintenance sequence like any other roll, and expect the tips to be
where they were.

### What this document no longer says

The enumerated table of guard failure branches is gone from here and from
`README.md`, along with the `--check-guard` invocation, because the code it
enumerated is gone. The 2026-09-16 records keep their descriptions of the guard
as it behaved on the day, including the SIGPIPE stall and the rollback
references it wrote; `p2mr-node:v31.1-p2mr-m05-running-a97926175af2` is still on
the cluster and is still node B's way back to the 32-patch build.

## Executed on 2026-09-16 (switch to immutable tags)

This is the manual step the entry above leaves open, carried out on the
cluster. It appears to be dated a day earlier than that entry and is not: every
timestamp in this document is UTC, the cluster read `2026-09-16T20:52Z` while
this was written, and the heading above was dated from a clock ahead of UTC.
Read the two in the order they appear.

The first apply of the pinned manifests. No image was built and no build
changed: this moved every workload's image string from a shared mutable tag to
the immutable tag naming the content it was already running. Carried out
between 20:35Z and 20:50Z. The chain did not move across the switch and no pod
changed what it runs.

### The three aliases

Run at 20:35:50Z, in the form the section above then carried, which did not yet
have `--local`. All three destinations were absent, so the three names were
created and nothing was replaced; the flag was added afterwards to make that a
property of the command rather than of the day. Each new name reports the
digest the table predicts, and each matches the mutable tag it was made from:

| New name | containerd manifest digest | Same digest as |
|---|---|---|
| `p2mr-node:v31.1-p2mr-m0-b120da303df8` | `sha256:b120da303df805d13bc52e0ed9ab1f41173f61172f4c2a299421bf7fac66305f` | `p2mr-node:v31.1-p2mr-m0` |
| `p2mr-node:v31.1-p2mr-m05-a64bad420d00` | `sha256:a64bad420d00462db66c9ec28477c2df56d036c311a42b2f070a4115ddc266dd` | `p2mr-node:v31.1-p2mr-m05` |
| `p2mr-miner:v31.1-p2mr-ed54f802502c` | `sha256:ed54f802502c0d7324e887b59c16169b2b7ccc41a1629e97a61cd3feecff2e6c` | `p2mr-miner:v31.1-p2mr` |

The three `ctr images tag` commands worked. The verification command beside
them did not: it matched nothing, on a cluster where all three aliases existed.
The corrected version is above, with the two reasons. This is the failure mode
worth naming: the aliases were in fact correct, and an empty result from a
check that cannot match looks exactly like an empty result from a check that
found no problem. Every claim in the table above was made with a command that
was tested against a case it should reject.

### What the apply changed

Five image strings and one replica count, and nothing else:

| Workload | From | To |
|---|---|---|
| `nodea` | `p2mr-node:v31.1-p2mr-m0` | `p2mr-node:v31.1-p2mr-m0-b120da303df8` |
| `nodeb` | `p2mr-node:v31.1-p2mr-m05` | `p2mr-node:v31.1-p2mr-m05-a64bad420d00` |
| `ark0-miner` | `p2mr-miner:v31.1-p2mr` | `p2mr-miner:v31.1-p2mr-ed54f802502c` |
| `ark0-observe` | `p2mr-miner:v31.1-p2mr` | `p2mr-miner:v31.1-p2mr-ed54f802502c` |
| `ark0-soak` | `p2mr-miner:v31.1-p2mr` | `p2mr-miner:v31.1-p2mr-ed54f802502c` |

`render-config.sh --from-live` reported `no difference` beforehand, so the node
configuration was not part of this and nothing was applied through it.

### The same content, confirmed per pod

This is the property the switch has to have, and the one worth checking rather
than reasoning about: a new image string, the same bytes underneath.

| Pod | imageID before | imageID after |
|---|---|---|
| `nodea-0` | `sha256:44faa26d7d2980292d402d5d7329f4c8e8699a18c10e3c35e8395fd52c224593` | unchanged |
| `nodeb-0` | `sha256:790785687d86cb9cefbb0993cd729e019b283c3aa726ef3982a17e77219bc606` | unchanged |
| `ark0-miner` | `sha256:356c396122e24adb484500b72e62403dc263bb25df063ce546c24438784dc0c6` | unchanged |

The kubelet keeps reporting the *old* tag in `status.containerStatuses[].image`
even after the switch, because two names now point at one digest and it reports
whichever it resolved. `spec.template.spec.containers[].image` is where the
pinning shows, and all five read the immutable tag. Read the spec for what a
workload is pinned to and the imageID for what it is running; the status image
string answers neither question reliably.

### Before and after

| | frozen, 20:38:26Z, producer at zero | both nodes back, 20:39:18Z | 20:50:13Z |
|---|---|---|---|
| block count, node A | 1651 | 1651 | 1658 |
| block count, node B | 1651 | 1651 | 1658 |
| best hash, both | `0000032a42da83f8658b609bf1e5f8ffa66447d5eff403f15ce84f1da90460cc` | unchanged | `0000031d941424d0068adf7cd98999da5dd711b3e404dfc92ff6e9b4e59a5600` |
| genesis, both | `00000008819873e925422c1ff0f99f7cc9bbb232af63a077a480a3633bee1ef6` | unchanged | unchanged |
| chain tips, each node | 1 `active` at 1651, 3 `invalid` at 127 | 1 `active` at 1651, 3 `invalid` at 127 | — |
| `p2mr`, each node | buried, active, height 1 | buried, active, height 1 | — |
| connections, each node | 2 | 1 | 2 |
| `ark0` wallet on node A | txcount 1683 | — | txcount 1692 |

Both pods were replaced at 20:38:45Z and were Ready twelve seconds later, at
the same height and the same hash, with zero container restarts since. The
`tmr()` differential was checked again against the running pods: node B rejects
a one-leaf tree with the M0.5 explanation, node A does not know the function.
So the switch moved the names and not the builds.

### Blocks after the switch

| height | hash | header time | gap |
|---|---|---|---|
| 1651 | `0000032a42da83f8658b609bf1e5f8ffa66447d5eff403f15ce84f1da90460cc` | 20:37:54Z | the frozen tip |
| 1652 | `000002ca2f05e1301c921aa6b5153b6d171f46987c56262f7fe4fe95dc222813` | 20:39:37Z | 103 s |
| 1653 | `0000002bfd80d20f488a8be8b1a7e0722e58fb870fd357335e4e6da1e2b47f1e` | 20:41:10Z | 93 s |
| 1654 | `000000e44578066b8601951ea3b81b97c3136aa46627dcb4cb2c3470cf3fbbab` | 20:43:22Z | 132 s |
| 1655 | `000000959405c30bee031e4164b6fa4c2244689e8d7955c177c4f870b9002eac` | 20:44:55Z | 93 s |

The producer was down 112 seconds, from 20:37:43Z to the `root-sockets` apply
at 20:39:35Z, and 1652 absorbed it at 103 seconds. The 132 second gap at 1654
is grinding, not a fault: the producer log for that period is five consecutive
`height N <hash>` lines with no retry and no error between them, and the time
to find a nonce is a random variable whose tail looks exactly like this. A
failure would have left `block production failed; retrying in 15s` in the log,
as the 2026-09-16 release roll did.

### The soak

`ark0-soak-immutable-0916`, on the pinned producer tag:

```
2026-09-16T20:47:46Z | 63776840e88d2e15dced7c1e9ab271054bebd01a2c8ac5023d7cbe6482424369 | eaba57c805bcd4f379a2ca3ca3ea878cdcb3ed02ba80628a159795ff3bbc3866 | 1657/1658 | ok (index 107, vsize 117, sig 64B, leaf 34B, control 33B, dims PASS)
```

The Job's pod spec names `p2mr-miner:v31.1-p2mr-ed54f802502c`, so the CronJob
inherited the pin without being touched separately.

### Observation

```
2026-09-16T20:50:00Z | 1658 | 1658 | 0000031d941424d0 | 0000031d941424d0 | 2 | 2 | 4 | 0 | Running | Running | Running | ok | ok | ready:yes restarts:0 | ready:yes restarts:0 | ready:yes restarts:0
```

### Where this departed from the plan

1. **The producer was stopped before the apply, not by it.** The maintenance
   overlay declares `replicas: 0`, so scaling to zero first is not undone by
   the apply that follows, and the tip was genuinely frozen before either node
   was replaced. Doing it in one step would have had the producer terminating
   while node A was restarting, which is the race that put a block against an
   unanswering node A at 13:21Z. One extra command buys the frozen reading its
   name claims.
2. **The verification command for the aliases was broken**, as described above.
   Corrected in the same commit as this record.
3. **Nothing else.** `render-config.sh` reported no difference, the apply
   changed five image strings and one replica count, both nodes came back on
   the same tip with the same imageIDs, and the soak passed first time.

### What to prune, and what not to

Nothing was removed. The old mutable tags `p2mr-node:v31.1-p2mr-m0`,
`p2mr-node:v31.1-p2mr-m05` and `p2mr-miner:v31.1-p2mr` still exist and still
name the same content as their immutable aliases, which is what makes the
rollback in this section's instructions work. They are no longer referenced by
any workload. `p2mr-node:v31.1-p2mr-m05-running-a97926175af2` and
`p2mr-node:v31.1-p2mr-m05-previous` are the old guard's names for the 32-patch
build, which is node B's way back one step; `p2mr-miner:v31.1-p2mr-previous`
and `-pre-release` are the same for the producer. None of them should be
removed while it is somebody's way back.

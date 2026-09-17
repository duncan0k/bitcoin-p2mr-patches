# Overlay: `root-sockets`

Runs the five Ark-0 workloads as uid 0 instead of uid 10000.

This is a workaround for one cluster, not a recommendation. Apply it only after
confirming that the kernel underneath actually refuses unprivileged sockets, by
the check at the bottom of this file. Everywhere else, use the base.

## What goes wrong without it

`bitcoind` starts, reads its configuration, and then fails every bind:

```
[warning] libevent: socket: Operation not permitted
[error] Unable to bind any endpoint for RPC server
```

The producer, the observation probe and the soak job fail the same way, because
all three are RPC clients and a client needs a socket too.

`EPERM` from `socket(2)` rather than from `bind(2)` is the tell. A port
permission problem surfaces at `bind`; this surfaces one call earlier, when the
socket is created and no address is involved yet. Nothing about the port, the
address or the container is in play at that point, only the uid.

## Why

The nodes of this cluster carry eBPF programs attached to the **root cgroup**,
`/sys/fs/cgroup`, which every pod on the node inherits. The relevant one is
attached at `cgroup_inet_sock_create` and denies `socket(AF_INET)` and
`socket(AF_INET6)` to uid 10000. Two further programs at `cgroup_inet4_bind`
and `cgroup_inet6_bind` restrict binding separately.

```
ID   AttachType               Name
242  cgroup_inet_ingress      cgroupskb_ingre...
243  cgroup_inet_egress       cgroupskb_egres...
249  cgroup_inet_sock_create  cgroupsock_inet...
255  cgroup_inet4_bind        bind4_block_por...
256  cgroup_inet6_bind        bind6_block_por...
```

Three things follow, and each one rules out a fix that would otherwise be
preferable:

- **No image can fix it.** The decision is made in the kernel, against the uid,
  before the process sees a file descriptor.
- **It is not a capability problem.** `NET_RAW`, `NET_ADMIN` and
  `NET_BIND_SERVICE` make no difference, which is consistent with a cgroup BPF
  hook rather than with a capability check.
- **Not every uid is refused.** uid 0 and uid 1000 are both permitted. uid 0 is
  what this overlay uses, because it is the one that has been verified end to
  end against these ports on this cluster.

The programs are attached at the root cgroup by something outside Kubernetes, so
they are not removable from inside a namespace and they survive a pod restart, a
node drain and a k3s restart.

## What it changes, and what it does not

Five patches, each replacing one pod-level `securityContext` with
`runAsUser: 0` / `runAsGroup: 0`. Replacing rather than merging is deliberate:
`runAsNonRoot`, `fsGroup` and the seccomp profile have to come off with the uid,
and a strategic merge cannot remove a field it does not mention.

| Setting | Base | This overlay |
|---|---|---|
| `runAsNonRoot` | `true` | removed |
| `runAsUser` / `runAsGroup` | `10000` | `0` |
| `fsGroup` | `10000` | removed; root reads the volumes whatever owns them |
| `seccompProfile` | `RuntimeDefault` | removed |
| `allowPrivilegeEscalation` | `false` | `false`, unchanged |
| `capabilities` | `drop: [ALL]` | `drop: [ALL]`, unchanged |
| everything else | | unchanged |

The two container-level settings stay in force, so these pods still drop every
capability and still cannot gain privileges. What they lose is the uid
separation and the seccomp profile.

One consequence outside the manifests: the chain in the two node volumes has to
be owned by whichever uid the nodes run as. Under this overlay that is `0:0`,
which is what the seed step in `../../ARK0-MIGRATION.md` chowns it to. Under
the base it would be `10000:10000`. Switching a running network between the two
means chowning the volumes, not just re-applying.

## Check whether you need it

On a node of the cluster:

```bash
sudo bpftool cgroup tree /sys/fs/cgroup | head -20
```

A `cgroup_inet_sock_create` entry is the program described above. Confirm that
it is what is biting, rather than assuming, by running the base image as the
unprivileged uid and asking it to open a socket:

```bash
kubectl -n ark0 run sockcheck --rm -i --restart=Never \
  --image=p2mr-node:v31.1-p2mr-m0 --image-pull-policy=IfNotPresent \
  --overrides='{"spec":{"securityContext":{"runAsUser":10000,"runAsGroup":10000}}}' \
  --command -- sh -c 'bitcoin-cli -version >/dev/null && echo binary ok; \
    bitcoin-cli -rpcconnect=127.0.0.1 -rpcport=1 getblockcount 2>&1 | head -2'
```

`Operation not permitted` on socket creation means the hook is active for that
uid and this overlay is the answer. `Connection refused` means the socket was
created and only the connection failed, which is the expected result on a
cluster that does not have this restriction: use the base there.

## Applying

```bash
kubectl apply --dry-run=server -k contrib/k3s/overlays/root-sockets
kubectl apply             -k contrib/k3s/overlays/root-sockets
```

Two things this does not carry, both on purpose.

**The node configuration.** `10-configmap.yaml` is a template with placeholders
in it, so it is not in the base kustomization and is not in this overlay
either. An apply here leaves `ark0-conf` alone. Render and apply it separately
with `../../ark0/render-config.sh`, which diffs against the live object first.

**A paused producer.** `50-miner.yaml` declares `replicas: 1`, and an apply
puts that back. If the producer has been deliberately scaled to zero for
maintenance, applying this overlay restarts it, possibly against a node that is
still coming up. Use `../root-sockets-maintenance/` for every apply during node
or configuration work, and start the producer explicitly afterwards.

Read `../../ARK0-MIGRATION.md` first if the network is not already running:
the chain has to be in the volumes before the StatefulSets start, and neither
the base nor this overlay enforces that ordering.

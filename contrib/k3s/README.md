# Building and testing the P2MR series inside k3s

Everything here builds and tests the patch series in a Kubernetes namespace, so
nothing but the cluster's own resources is installed on the machine that runs
it. The source tree, the compiler cache and every log live on one
PersistentVolumeClaim, and the two images are built in-cluster and imported
straight into containerd.

There is no Docker daemon and no registry on the target node, so images are
built with kaniko to a tarball on the volume and imported with `ctr`.

## Layout

| File | Purpose |
|---|---|
| `Dockerfile.builder` | Ubuntu 24.04 with everything Core v31.1 needs to configure, build and run the unit and functional tests |
| `Dockerfile.node` | runtime image: the built binaries plus their shared libraries, nothing else |
| `Dockerfile.miner` | block producer image: that runtime plus python3, the upstream signet miner, the functional test framework and the Ark-0 producer scripts |
| `miner-entrypoint.sh` | the producer image's entry point: writes the two client configurations from the environment, then runs the loop |
| `namespace.yaml` | the `p2mr-build` namespace |
| `pvc.yaml` | the 200Gi `p2mr-work` volume and what lives on it |
| `shell.yaml` | idle helper pod, the way in and out of the volume |
| `kaniko.yaml` | Pod template for one image build |
| `build.yaml` | Job template for one build-and-test run |
| `build.sh` | what the Job runs: clone, `git am`, configure, build, unit tests, functional tests |
| `common.sh` | shared settings for the host-side scripts |
| `seed.sh` | create the namespace and volume, copy the patches and Dockerfiles onto it |
| `stage-miner.sh` | assemble the producer image's build context from the built tree and `ark0/producer/` |
| `build-image.sh` | build one image with kaniko and import it into containerd |
| `run-build.sh` | start one run, wait for it, print the log tail and the summary |
| `ARK0-MIGRATION.md` | step by step plan for moving the experimental signet nodes into the cluster, and the records of the runs that followed it |
| `ark0/` | the manifests that plan applies, with their own README |

`build.yaml` and `kaniko.yaml` are templates: `run-build.sh` and
`build-image.sh` substitute their `__TOKEN__` placeholders and apply the
result. Applying them directly does not work, by design, because every run
needs its own name.

## Prerequisites

- A cluster with a default storage class that can satisfy 200Gi, and outbound
  internet access from pods (the builder pulls Ubuntu packages, the Job clones
  `bitcoin/bitcoin`).
- `kubectl` access. On a k3s node without a kubeconfig in the invoking user's
  home, export `KUBECTL="sudo k3s kubectl"`; every script honours it.
- Shell access to the node for the one step Kubernetes cannot do: importing an
  image tarball into containerd. `CTR` defaults to `sudo k3s ctr`.
- If the node reaches Docker Hub through a mirror rather than directly, pass
  the same host to `build-image.sh` as `REGISTRY_MIRROR`. kaniko does its own
  registry pulls and does not read containerd's mirror configuration, so a node
  whose `/etc/rancher/k3s/registries.yaml` redirects `docker.io` needs kaniko
  told separately:

  ```bash
  REGISTRY_MIRROR=$(grep -A3 'docker.io:' /etc/rancher/k3s/registries.yaml \
                    | grep -o 'https://[^"]*' | head -1 | sed 's|https://||')
  ```

Only ClusterIP-less workloads are created. No Service, NodePort, LoadBalancer
or Ingress exists in the namespace: nothing here needs inbound connectivity.

## Quick start

From a checkout of this repository, on a machine that can reach the cluster:

```bash
export KUBECTL="sudo k3s kubectl"
contrib/k3s/seed.sh                 # namespace, volume, patches, build contexts
contrib/k3s/build-image.sh builder  # p2mr-builder:v31.1-<hash of Dockerfile.builder>
contrib/k3s/run-build.sh            # one build-and-test run
```

Neither of those two lines names the builder, and they do not have to: both
resolve it the same way, from the hash of `Dockerfile.builder`, through
`builder_image_ref` in `common.sh`. They used to disagree, the first writing a
suffixed reference and the second asking for the bare `p2mr-builder:v31.1`, so
this quick start could not work on a machine that had never built one before.
`run-build.sh` now checks that the reference is in containerd before it starts
a Job and says what to run if it is not, because there is no registry here and
a Job that cannot find its image waits in `ImagePullBackOff` rather than
failing. Editing the Dockerfile changes the reference, so the next build uses
the new builder and the previous one keeps its own name. A builder published
under `IMAGE_TAG_SUFFIX` is the one case where the two can differ;
`build-image.sh` prints the reference it wrote and the `BUILDER_IMAGE_REF` to
export for it.

An installation that already has the old shared `p2mr-builder:v31.1` does not
need to rebuild, as long as that image was built from the `Dockerfile.builder`
in this checkout. Give it the reference the tooling now resolves:

```bash
REF="$( . contrib/k3s/common.sh && canonical_image_ref "$(builder_image_ref)" )"
sudo k3s ctr -n k8s.io images tag --local \
    docker.io/library/p2mr-builder:v31.1 "$REF"
```

**`--local` is required and `--force` must not be added.** Without `--local`
the command goes through containerd's transfer service, whose image store
updates an existing entry, so it would replace a reference somebody else's
build had already published and exit 0 saying nothing. With it, a destination
that exists fails with `already exists` and nothing is changed. That is the
same rule `build-image.sh` follows, and it applies to every `ctr images tag` in
this repository.

If it does fail that way, the reference is already there and the question is
whether it names this image. Compare, and do not reach for `--force`:

```bash
sudo k3s ctr -n k8s.io images ls \
  | awk -v r="$REF" '$1 == r || $1 == "docker.io/library/p2mr-builder:v31.1" { print $1, $3 }'
```

Two equal digests mean the alias is already made and there is nothing to do.
Two different ones mean this checkout's `Dockerfile.builder` has been built
twice into different images, and the reference belongs to whichever was
published first; build a new one rather than taking the name away from it.

`seed.sh` is also how an updated patch series is published: it replaces the
patch directories and leaves the source tree, the compiler cache and the logs
alone.

## Running the two patch sets

The consensus series alone (the ten patches in `patches/`, the default):

```bash
KUBECTL="sudo k3s kubectl" contrib/k3s/run-build.sh
```

The full M0.5 set (those ten plus the twenty-four in `patches-m05/`), from a fresh
clone, with the P2MR functional tests and then the whole default functional
set:

```bash
KUBECTL="sudo k3s kubectl" \
PATCH_SETS="master m05" \
FRESH_CLONE=1 \
RUN_DEFAULT_FUNCTIONAL=1 \
FUNCTIONAL_TESTS="wallet_p2mr.py wallet_p2mr_signet.py wallet_p2mr_multisig.py wallet_p2mr_timelock.py feature_p2mr.py feature_p2mr_signet.py p2p_segwit.py" \
contrib/k3s/run-build.sh
```

Either one with the retarget spacing patch on top, which is what both Ark-0
nodes run since 2026-09-22:

```bash
KUBECTL="sudo k3s kubectl" \
PATCH_SETS="master spacing" \
FRESH_CLONE=1 \
FUNCTIONAL_TESTS="feature_p2mr.py feature_p2mr_signet.py feature_signet.py tool_signet_miner.py p2p_segwit.py" \
contrib/k3s/run-build.sh

KUBECTL="sudo k3s kubectl" \
PATCH_SETS="master m05 spacing" \
FRESH_CLONE=1 \
RUN_DEFAULT_FUNCTIONAL=1 \
FUNCTIONAL_TESTS="wallet_p2mr.py wallet_p2mr_signet.py wallet_p2mr_multisig.py wallet_p2mr_timelock.py feature_p2mr.py feature_p2mr_signet.py p2p_segwit.py" \
contrib/k3s/run-build.sh
```

The patch set names are directories under `/work/patches`, applied in the order
given. `seed.sh` publishes `patches/` as `master`, `patches-m05/` as `m05` and
`patches-spacing/` as `spacing`.

## Configuration

Every knob is an environment variable read by `run-build.sh` and passed into
the Job. The defaults build the consensus series and run the P2MR functional
tests.

| Variable | Default | Meaning |
|---|---|---|
| `RUN_ID` | UTC timestamp | names the Job and the log directory |
| `PATCH_SETS` | `master` | directories under `/work/patches`, applied in order |
| `FRESH_CLONE` | `0` | `1` deletes the source tree and clones again |
| `APPLY_PATCHES` | `1` | `0` builds the tree as it stands, without touching git |
| `BUILD_JOBS` | `32` | `cmake --build -j` |
| `USE_CCACHE` | `1` | adds the two ccache compiler launchers |
| `EXTRA_CMAKE_ARGS` | empty | appended to the configure step |
| `RUN_UNIT_TESTS` | `1` | run `build/bin/test_bitcoin` |
| `UNIT_TEST_ARGS` | `--report_level=detailed` | arguments for it |
| `FUNCTIONAL_TESTS` | the three P2MR consensus tests | space separated scripts; empty skips the phase |
| `RUN_DEFAULT_FUNCTIONAL` | `0` | `1` also runs the default set |
| `FUNCTIONAL_JOBS` | `16` | `test_runner.py --jobs` |
| `FUNCTIONAL_EXTRA_ARGS` | empty | appended to every `test_runner.py` call |
| `CPU` / `MEMORY` | `32` / `48Gi` | request and limit of the build container |
| `WAIT` | `1` | `0` returns as soon as the Job is created |

Apart from the two ccache launchers, the configure flags are exactly the ones
`apply.sh` uses:

```
-DCMAKE_BUILD_TYPE=Release -DBUILD_TESTS=ON -DENABLE_WALLET=ON
-DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF
```

Set `USE_CCACHE=0` for a run with no extra flags at all. The M0.5 series adds a
fuzz target, which needs `EXTRA_CMAKE_ARGS=-DBUILD_FUZZ_BINARY=ON` to be
compiled; the default build leaves it out, as `apply.sh` does.

## What is on the volume

```
/work/patches/master/   the 10 consensus patches + SHA256SUMS
/work/patches/m05/      the 24 M0.5 wallet patches + SHA256SUMS-m05
/work/patches/spacing/  the retarget spacing patch + SHA256SUMS-spacing
/work/imgctx/builder/   kaniko context for Dockerfile.builder
/work/imgctx/node/      kaniko context for Dockerfile.node, bin/ staged by build.sh
/work/img/              image tarballs, deleted once imported
/work/src/              the bitcoin/bitcoin clone with the series applied
/work/ccache/           compiler cache, capped at 20G, reused across runs
/work/test-cache/       functional test framework --cachedir
/work/test-tmp/<run>/   functional test framework --tmpdir
/work/out/runs/<run>/   every run's binaries, published or not
/work/out/bin/          the last set that passed, plus its PROVENANCE.txt
/work/logs/<run>/       summary.txt plus one log per phase
```

Reach any of it through the helper pod:

```bash
sudo k3s kubectl -n p2mr-build exec p2mr-shell -- cat /work/logs/<run>/summary.txt
sudo k3s kubectl -n p2mr-build exec p2mr-shell -- ls /work/logs
sudo k3s kubectl -n p2mr-build cp p2mr-build/p2mr-shell:/work/logs/<run> ./logs
```

`summary.txt` has one line per phase with its result and wall time, so the
tail of one file answers what ran, how long it took and whether it passed.

## The runtime node image

`build.sh` writes every run's binaries to `/work/out/runs/<run>/bin` as soon as
the compile finishes, and copies them to `/work/out/bin` and
`/work/imgctx/node/bin` only once every check that run was asked for has
passed. So the node image is always built from a tree that was tested, not
merely from one that compiled:

```bash
KUBECTL="sudo k3s kubectl" contrib/k3s/build-image.sh node
```

Beside the published binaries is a `PROVENANCE.txt` naming the patch sets, the
number of patches, the head commit, a hash of the ordered list of applied
commits, and the result of each check. `build-image.sh` refuses a context that
has no such file or whose file does not record a pass, and prints the summary
before building, so the series going into an image is visible at the moment it
is chosen. A run whose tests fail publishes nothing and leaves its binaries
under `/work/out/runs/<run>/`; `/work/out/bin` keeps the last set that passed.

The image contains `bitcoind`, `bitcoin-cli`, `bitcoin-tx`, `bitcoin-util` and
`bitcoin-wallet`, their shared libraries, and nothing else. It runs as uid
10000 with `/data` as the data directory, and takes every parameter from the
pod spec.

The command names no tag, and that is deliberate. There is no single node
image: Ark-0 runs node A on the ten consensus patches and node B on those plus
the twenty-four M0.5 wallet patches, each with the retarget spacing patch on top
since 2026-09-22, so the two need different tags, and getting
them the wrong way round would put wallet code on the block producing node.
`build-image.sh` therefore reads the patch sets out of the provenance file and
derives the suffix from what was actually built and tested:

| `patch sets verified` in `PROVENANCE.txt` | Tag produced |
|---|---|
| `master` | `p2mr-node:v31.1-p2mr-m0-<head12>` |
| `master m05` | `p2mr-node:v31.1-p2mr-m05-<head12>` |
| `master spacing` | `p2mr-node:v31.1-p2mr-m0-spacing-<head12>` |
| `master m05 spacing` | `p2mr-node:v31.1-p2mr-m05-spacing-<head12>` |

The `<head12>` suffix is what makes the tag immutable; the next section covers
it. The series part is what this lookup decides.

Note *verified*, not requested. `PATCH_SETS` is what the caller asked for, and
with `APPLY_PATCHES=0` against a source tree left over from an earlier run the
two can differ: a tree still carrying the M0.5 series builds, passes every test
and, if the label were taken on trust, publishes as `-m0` and lands on the
block producing node. So `build.sh` identifies the tree instead. It compares
the `git patch-id` of every commit on top of `v31.1` with the `git patch-id` of
every patch file on the volume, which ignores commit metadata, line numbers and
whitespace and compares only the changes themselves. Equal lists in the same
order mean the tree is that series.

Patch-ids compare committed history, and cmake compiles the working tree, so
the comparison only means anything while the two agree. An uncommitted edit to
a tracked file, or a new untracked `.cpp`, changes what gets compiled and
changes nothing a patch-id sees. `build.sh` therefore requires a clean index
and a clean tracked tree first, and refuses untracked files under `src/`,
`test/`, `cmake/`, `CMakeLists.txt` and `depends/`. Untracked files elsewhere
are fine: `build/`, the compiler cache and the functional tests' output are
untracked by design and are not build inputs.

The provenance records all of it: `patch sets requested`, `patch sets
verified`, `series verified` and a `series note` giving the reason when
verification did not happen. A mismatch is a loud warning and the *verified*
series wins, so the tag follows the source. A tree that is dirty, or that
matches no known set, records `series verified: no`, and `build-image.sh` then
refuses to choose a tag at all.

**The limitation this does not remove.** The build still happens in `/work/src`
in place, so what is verified is that the tree was clean and its history
matched a patch set at the moment the check ran, not that the bytes the
compiler read were frozen. Nothing here takes an immutable snapshot and
compiles that. `FRESH_CLONE=1` is the way to be certain: it deletes the tree
and clones again, so the source cannot carry anything from an earlier run or
from anyone's editor. Use it for any build whose image will be deployed.

A patch-set list with no tag defined is an error rather than a guess; add it to
`node_image_for_patch_sets` in `common.sh`. `NODE_IMAGE` in the environment
overrides the whole lookup, including the refusal, for anyone who really means
to. Because both builds
publish to the same `/work/imgctx/node/bin`, build each image directly after
its own run, before the next run overwrites it. `ARK0-MIGRATION.md` step 2 has
the ordered sequence and the digest checks.

**A tag this tooling writes is never replaced.** `ctr images import` moves a
tag onto new content in place, so rebuilding a tag a running workload uses
takes away the only name its current image had. That happened to
`p2mr-node:v31.1-p2mr-m05` on 2026-09-16 while `nodeb-0` was running it, and
the roll had to fall back to the `-m0` image.

The answer is not to protect shared tags but to stop having them. Every build
is imported under a tag of its own:

```
p2mr-node:v31.1-p2mr-m05-<head12>
```

The suffix is the first twelve characters of the commit `PROVENANCE.txt`
records, which is the source that was compiled and tested. The builder, which
has no provenance, is suffixed with the hash of its Dockerfile instead.
Different source gives a different tag, so two builds cannot collide; the same
source gives the same tag, and then the tag already exists and the question is
what to do about it. That is settled before anything is written: the tarball is
imported under a reference belonging to that run alone, the digests are
compared, and only then is the tag created. The same digest is reported as
"already present" and the tag is not touched. A different digest stops the run
with the tag still naming what it named, because one tag naming two things is
the property this scheme exists to keep.

The different digest is the normal outcome of a rebuild, not an unlikely one.
Two builds of the same context here produce different images, with or without
kaniko's `--reproducible`, and the reproducibility notes below say why.
`IMAGE_TAG_SUFFIX` is how to name such a build when it is wanted; it renames
and nothing else, and `build-image.sh` still requires the passing provenance,
the verified series and the head before it will name anything.

`build-image.sh` prints the tag and its digest as its last two lines.

What it does to containerd, exactly: it lists every image once with
`ctr images ls` and reads two rows out of that listing, the temporary
reference it just imported under and the tag it is about to write. The listing
is unfiltered, so the whole table is read; nothing else in it is used, and no
reference other than those two is written, moved or deleted. The temporary
reference is deleted on the way out.

Those rows are matched by an exact string, and `ctr images ls` prints every
name normalised, so a short name has to be normalised the same way before it is
compared. `canonical_image_ref` in `common.sh` does it once and the lookup, the
import name and the cleanup all use that one answer. It follows Docker's rules
rather than prepending a prefix: `docker.io` is the registry when the first
component is not one, `index.docker.io` means `docker.io`, and a Docker Hub
repository with no slash in it gains the implicit `library/`. Other registries
keep their host, their port and their path. The table it has to satisfy is in
that file and is run by:

```bash
contrib/k3s/build-image.sh --self-test
```

which reaches no cluster and changes nothing.

On the path where the tag is written, that deletion removes a name and not
content, because the final tag names the same manifest by then. On the path
where the run stops because the digests disagree, it does remove the content:
the tag was deliberately left naming the old manifest, so the build that was
just made has no other name and nothing refers to it. That is the intended
outcome of a refusal, and it is why the refusal names `IMAGE_TAG_SUFFIX` in
the same breath. A build worth keeping gets published under a name of its own
and rebuilt, rather than rescued out of a temporary reference.

**Two builds of the same target at the same time are not supported.** Both
would use the pod name `kaniko-<target>` and write the same
`/work/img/p2mr-<target>.tar`, so the second deletes and replaces the first's
pod and either may import the other's tarball. Create-only publication keeps
the result from being a silently overwritten tag, which is worth having, but it
does not make the two runs independent, and the reference each one prints may
not name what it built. Run them one at a time. If something else might start
one, wrap the whole invocation in a lock, which works because these scripts
drive containerd on the node they run on:

```bash
flock /tmp/p2mr-build-image.lock contrib/k3s/build-image.sh node
```

That serialises the invocations rather than the pods, which is the level the
collision is at. The lock is held for the whole run, so the second invocation
waits for the first to finish instead of overwriting its pod.

An earlier version tried to keep shared tags safe by asking the cluster which
images were in use and preserving those first. That question has no small,
complete answer: successive reviews found the implementation comparing config
digests against manifest digests, mis-normalising registry references,
filtering pods by a phase that omits running containers, and adding lookups
that could fail without saying so. Immutable tags make the question
unnecessary rather than answering it better.

**Which build a workload runs** is a fact about `ark0/kustomization.yaml`,
whose `images:` block pins each workload to one immutable tag and which both
overlays inherit. Rolling a workload:

```bash
( cd contrib/k3s/ark0
  kustomize edit set image p2mr-node-m05=p2mr-node:v31.1-p2mr-m05-<head12> )
```

or edit `newTag` by hand. The edit is step two of a sequence, not the whole of
it: the producer is stopped and the tip read before the pin changes, because
one apply that both stops the producer and replaces a node runs the two at
once. "Rolling a workload onto a new build" in `ARK0-MIGRATION.md` has the
order. Rolling back is the same edit with the previous tag, which that
document's roll table records, and it has the same race, so it takes the same
order. The old tag was never overwritten, so it still names the image it always
named.

Because the tag lives in the kustomization rather than in the workload files,
these manifests are applied through `kubectl apply -k`. A `kubectl apply -f` on
one workload file alone would ask for an untagged image that does not exist.

Check it without starting a network:

```bash
sudo k3s kubectl -n p2mr-build run p2mr-node-check --rm -i --restart=Never \
  --image=p2mr-node:v31.1-p2mr-m0-b120da303df8 --image-pull-policy=IfNotPresent -- -version
```

## The block producer image

`p2mr-miner` runs the Ark-0 block producing loop. It carries
`bitcoin-cli` and `bitcoin-util` rather than `bitcoind`, because a producer
drives a node instead of being one, plus python3, the upstream
`contrib/signet/miner`, the functional test framework that module imports, and
the two producer scripts from `ark0/producer/`.

Building the image is two steps, because the binaries and the miner module come
off the work volume while everything else comes from here. `stage-miner.sh`
collects it all into the build context, checking first that neither producer
script carries key material:

```bash
KUBECTL="sudo k3s kubectl" contrib/k3s/stage-miner.sh
KUBECTL="sudo k3s kubectl" contrib/k3s/build-image.sh miner
```

`ark0.py` and `miner_loop.sh` take every path and the wallet name from the
environment, each with a documented default matching the layout a workstation
run produces, so one copy serves both. `Dockerfile.miner` sets the pod values in
its `ENV` block and edits neither file. Until 2026-09-16 the two scripts were
read out of an operator's home directory and patched during the build, which
left this checkout unable to build the image and put an account path into a
public Dockerfile.

With no arguments the image runs the loop; with arguments it runs those
instead, which is how it is checked without a cluster or a network:

```bash
sudo k3s kubectl -n p2mr-build run p2mr-miner-check --rm -i --restart=Never \
  --image=p2mr-miner:v31.1-p2mr-ed54f802502c --image-pull-policy=IfNotPresent \
  -- python3 /opt/ark0/demo/ark0.py --help
```

That prints the usage, which means the signet miner module loaded and every
`test_framework` import it pulls in resolved.

## Cleaning up

The Job objects are only useful until their logs have been read; the logs
themselves are on the volume.

```bash
sudo k3s kubectl -n p2mr-build delete job --all
sudo k3s kubectl -n p2mr-build exec p2mr-shell -- rm -rf /work/test-tmp/<run>
```

Images accumulate now, one tag per build, because none is ever reused. Three
kinds are not spare. Per workload: the tag its `images:` entry in
`ark0/kustomization.yaml` names, and the tag it would roll back to, which the
roll table in `ARK0-MIGRATION.md` records. Removing the second is removing the
way back, and nothing warns about it. And one builder: the reference
`Dockerfile.builder` resolves to today, which is the one `run-build.sh` asks
for. Everything else is free to go:

```bash
# What is pinned, and therefore must stay.
grep -A2 '^  - name:' contrib/k3s/ark0/kustomization.yaml
# The builder this checkout would use.
( . contrib/k3s/common.sh && builder_image_ref )
# What exists.
sudo k3s ctr -n k8s.io images ls | awk '/p2mr-(builder|node|miner)/ { print $1 }'
sudo k3s ctr -n k8s.io images rm <the ones in neither list>
```

To remove everything, including the source tree and the caches:

```bash
sudo k3s kubectl delete namespace p2mr-build     # takes the 200Gi volume with it
sudo k3s ctr -n k8s.io images ls | awk '/p2mr-(builder|node|miner)/ { print $1 }'
sudo k3s ctr -n k8s.io images rm <each of them>
```

## Verification record

Run `m05-e2e`, 2026-09-15, on a 64 core / 528 GB node, k3s v1.34.4, build
container capped at 32 CPU and 48Gi, `-j32`, cold ccache. Fresh clone of
`v31.1`, both patch sets, `git am` applied all 28 patches without a conflict.

| Phase | Result | Wall time |
|---|---|---|
| clone `v31.1` | ok | 4 s |
| `git am` 10 + 18 patches | ok | 1 s |
| cmake configure | ok | 14 s |
| cmake build `-j32` (477 translation units) | ok | 1 m 12 s |
| `test_bitcoin --report_level=detailed` | 738 of 743 cases ran, all passed, 26 666 369 assertions | 53 s |
| functional, the 7 P2MR tests, `-j16` | 7 passed, 0 failed, 0 skipped | 1 m 02 s |
| functional, default set, `-j16` | 273 passed, 0 failed, 17 skipped | 2 m 00 s |
| total | ok | 5 m 27 s |

Boost's own module summary reads `737 test cases out of 743 passed`, `1 test
case out of 743 passed with warnings`, `5 test cases out of 743 skipped`,
`26666369 assertions out of 26666369 passed`. The warning belongs to
`script_assets_tests/script_assets_test`, which warns rather than fails when
its external test data is not present; it is an upstream condition and has
nothing to do with P2MR.

The seven were `wallet_p2mr.py`, `wallet_p2mr_signet.py`,
`wallet_p2mr_multisig.py`, `wallet_p2mr_timelock.py`, `feature_p2mr.py`,
`feature_p2mr_signet.py` and `p2p_segwit.py`. The default set's 17 skips are
the ones this build cannot run: the USDT tracepoint tests, `interface_zmq.py`
(`-DWITH_ZMQ=OFF`), the tests that need a previous release downloaded, and
`tool_bench_sanity_check.py` (the benchmarks are not built).

Images, as containerd reported them on the day: `p2mr-builder:v31.1` 261.1 MiB,
`p2mr-node:v31.1-p2mr` 47.7 MiB, `p2mr-miner:v31.1-p2mr` 46.2 MiB. That single
node tag is the one the Ark-0 migration then had to split into `-m0` and
`-m05`, one per patch set; the sizes are unaffected.

## Notes on reproducibility

Both Dockerfiles pin their base image by digest. Apt versions are not pinned
one by one: the `noble-updates` pocket drops superseded versions within weeks,
which would make the Dockerfile unbuildable. Instead the builder image records
the exact version of every installed package at `/opt/p2mr/packages.txt` and a
toolchain summary at `/opt/p2mr/toolchain.txt`, which `build.sh` copies into
the head of every run's `summary.txt`. That is enough to describe a build
exactly after the fact, and to re-create it from `snapshot.ubuntu.com` if that
is ever needed.

`build.sh` verifies the `SHA256SUMS` of each patch set before applying it and
refuses to start if the `v31.1` tag does not resolve to commit `9be056a`, so a
run either reproduces the documented series or stops. The checksum test is
`sha256sum -c --quiet` and its exit status is acted on directly; until
2026-09-16 it was piped into `tail`, which reported success whatever the
checksums said.

`kaniko.yaml` pins the executor by digest rather than running `:latest`, so the
same builder is used on every run. Note what that does not buy: the upstream
kaniko project was archived on 2025-06-03 and is read-only, so the pinned
executor will receive no further fixes of any kind, and it is a dead end rather
than a version awaiting an upgrade. It stays because it is the only way to
build an OCI image here without a Docker daemon and without a registry.

**Every image that comes from outside this cluster is pinned by digest**: the
two Dockerfile base images, the kaniko executor, and the two helper pods that
only hold a volume open. `shell.yaml` and `ark0/25-seed-pod.yaml` ran
`ubuntu:22.04` until 2026-09-16; neither ends up inside a published image, so
pinning them changes no artifact, but it does mean a `kubectl cp` or a chain
copy cannot quietly happen through a different `tar` than the one that was
tested. No floating tag is pulled from a registry by anything here. Re-pinning
is a deliberate edit: read the new digest off the node with
`sudo k3s ctr -n k8s.io images ls`, do not reach for `:latest`.

The images built here are a different matter: they are named rather than
digest-pinned. A name like `p2mr-node:v31.1-p2mr-m05-<head12>` identifies one
build as reliably as a digest does, because nothing ever writes it twice, but
it identifies it only inside the containerd that holds it. Two clusters that
built the same commit have the same name for two different images. So the
digest checks in `ARK0-MIGRATION.md` step 2 still matter and the digest is
still what gets recorded next to each reference; the name is how a manifest
asks for it, not proof of what it is.

**What is and is not reproducible.** Given the same patch set and the same base
image digest, the *binaries* are pinned as tightly as the recorded package list
allows, and `PROVENANCE.txt` names the series each published set came from. The
*images* are not bit-for-bit reproducible: the Dockerfiles install Ubuntu
packages from repositories that move, so two builds of the same context on
different days can produce different layers. Read the image digests as
identifying one particular build, not as something an independent party can
re-derive. What an outside reader can re-derive is the patch series and the
binaries' behaviour, which is what `ark0/REPRODUCE.md` walks through and what
the claims actually rest on.

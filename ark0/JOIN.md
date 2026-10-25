# Joining Ark-0

Ark-0 is the experimental custom signet described in [`NETWORK.md`](NETWORK.md). It now has
one public node, so instead of only replaying the snapshot offline you can run the patched
node yourself, connect it, and check the P2MR rules against your own copy of the live chain.

Before you start:

- **One party produces every block.** The signet challenge is a 1-of-1 multisig and only the
  operator holds the key, and it alone holds the ML-DSA-44 block key that M2 requires from height
  20000. Your node verifies every block but cannot produce one. Ark-0 is not
  a decentralized network.
- **It is a test network for trying things out.** It may be reset at any time.
- **The explorer and the faucet are the operator's too.** A block explorer at
  <https://explorer.ark0.si> shows blocks and transactions, with P2MR spends taken
  apart, and a faucet at <https://faucet.ark0.si> gives out coins to try P2MR with
  (section 4). You need neither of them to follow the chain or to check its P2MR rules.
- **Rule changes are announced in this repository before they apply.** The latest one, milestone
  M4-R, applies from height 63000 ("Rule change at height 63000 (M4-R)" in `NETWORK.md`), expected
  around 2026-11-28 14:00 UTC and not before 2026-11-26 04:00 UTC. From there nodes accept, in P2MR
  leaves of version `0xe0`, a zero-knowledge proof that the spender knows the seed behind a key in
  place of an elliptic curve signature. **It is a hard fork:** a node built with `patches-m3/` but
  without `patches-m4r/` stops following the chain at the first block that uses it, so upgrade
  before height 63000 (section 1). The one before, M4-S, applies from height 32000 ("Rule change at
  height 32000 (M4-S)"). From there nodes check `OP_CHECKSLHDSA128S`, an SLH-DSA signature check in
  P2MR leaves. M3 applies from height 30000 ("Rule change at height 30000 (M3)"). From there nodes
  run P2MR leaves of version `0xe0`, whose elliptic curve signature checks stop working once one of
  two designated outputs is spent; the operator will spend one of them in an announced drill. M2 and
  M1 have applied since heights 20000 and 8600. Those four are soft forks: a node built without
  `patches-m4/` keeps following the chain (an M3 build only up to the first rescue spend of M4-R)
  but does not check the SLH-DSA signatures or relay the spends that use them, a node without
  `patches-m3/` does not run those leaves or check their rule, a node without `patches-m2/` does not
  check the block signatures, and a node without `patches-m1/` does not enforce M1 and does not
  relay the spends that use it. A new challenge would be a new network, and everyone would start
  again.

## 1. Get the node

A stock Bitcoin Core cannot follow Ark-0. It does not enforce the P2MR rules, and it rejects
the block at height 8064, because from that height Ark-0 retargets its difficulty against 90
seconds per block (see "Parameters" in `NETWORK.md`). Since [ROLL DATE] the operator's nodes have
run eight patch sets on Bitcoin Core v31.1: the consensus series in `patches/`, the wallet series
in `patches-m05/` (section 4 uses it), the retarget patch in `patches-spacing/`, the M1 series in
`patches-m1/`, the M2 series in `patches-m2/`, the M3 series in `patches-m3/`, the M4-S series in
`patches-m4/` and the M4-R series in `patches-m4r/`, applied in that order. You can take that build
prebuilt, as binaries or as a container image, or build it yourself; the rest of this guide works
the same with either.

On Windows, macOS and Linux there is a way without commands: the witness program of the release
[`ark0-witness-0.2.0`](https://github.com/duncan0k/bitcoin-p2mr-patches/releases/tag/ark0-witness-0.2.0)
runs a build of the node with the six patch sets up to `patches-m3/` and shows what it reports
(`witness/README.md`). That build stops following Ark-0 at the first block with a rescue spend, from
height 63000 on; a release of the witness program with the M4-R build is to follow before then. This
guide does not cover the witness program.

### Prebuilt binaries or image

The release [`ark0-node-m4r`](https://github.com/duncan0k/bitcoin-p2mr-patches/releases/tag/ark0-node-m4r)
has the binaries for x86_64 Linux, `SHA256SUMS` and its signature `SHA256SUMS.asc`. Download the
three into an empty directory, with the release key from this repository, and check the signature:

```bash
R=https://github.com/duncan0k/bitcoin-p2mr-patches
curl -fsSL --remote-name-all $R/releases/download/ark0-node-m4r/ark0-node-m4r-x86_64-linux-gnu.tar.gz \
  $R/releases/download/ark0-node-m4r/SHA256SUMS $R/releases/download/ark0-node-m4r/SHA256SUMS.asc \
  https://raw.githubusercontent.com/duncan0k/bitcoin-p2mr-patches/ark0-node-m4r/ark0/release-signing-key.asc
gpg --import release-signing-key.asc
gpg --verify SHA256SUMS.asc SHA256SUMS
```

`gpg` has to report a good signature from `Ark-0 release signing key (duncan0k)`
and end with this line:

```
Primary key fingerprint: 025D 989B 68FF 923F 8EF6  9D4B 28F6 5A19 58A2 361A
```

The same key is published at <https://github.com/duncan0k.gpg>, with the same fingerprint. The
warning that the key is not certified with a trusted signature is expected. Then check the
archive, unpack it, and check the binaries too; each command runs only if the one before it
succeeded:

```bash
sha256sum --ignore-missing -c SHA256SUMS &&
  tar -xzf ark0-node-m4r-x86_64-linux-gnu.tar.gz &&
  sha256sum --ignore-missing -c SHA256SUMS
```

The binaries are linked against the system's C and C++ runtimes, libevent and SQLite. They need
glibc 2.39 or later and the C++ runtime of GCC 13.2 or later: Ubuntu 24.04 and Debian 13 have
both, Ubuntu 22.04 and Debian 12 do not. On Ubuntu 24.04 or Debian 13, install the libraries with
`sudo apt-get install libevent-core-2.1-7t64 libevent-extra-2.1-7t64 libevent-pthreads-2.1-7t64 libsqlite3-0`.
Wherever this guide says `./build/bin/`, use `ark0-node-m4r/bin/` instead.

The image is `ghcr.io/duncan0k/ark0-node`, also for x86_64, with the libraries in it. Pull it by
its digest, the hash on the line of `SHA256SUMS` that names `ark0-node-m4r-image-manifest.json`, so
that what you run is what the signature covers:

```bash
docker pull ghcr.io/duncan0k/ark0-node@sha256:<that hash>
```

The image runs `bitcoind` and has `bitcoin-cli` too. Give it a directory of your own as `/data`,
with the `bitcoin.conf` of section 2 in it, and run it as yourself, so that the files it writes
there stay yours:

```bash
mkdir ark0-data        # put the bitcoin.conf of section 2 in it first
docker run -d --name ark0 --user "$(id -u):$(id -g)" -v "$PWD/ark0-data:/data" \
  ghcr.io/duncan0k/ark0-node@sha256:<that hash> -datadir=/data -printtoconsole
docker exec ark0 bitcoin-cli -datadir=/data getblockchaininfo    # a few seconds later, once it has started
```

With the image, the container is the node: skip the `bitcoind` command of section 2, use
`docker exec ark0 bitcoin-cli -datadir=/data` wherever this guide says `./build/bin/bitcoin-cli`
(in section 4, `cli() { docker exec ark0 bitcoin-cli -datadir=/data "$@"; }`), and stop and start
the node with `docker stop -t 60 ark0` and `docker start ark0`. `debug.log` is in
`ark0-data/signet/`.

### Build from source

```bash
git clone https://github.com/duncan0k/bitcoin-p2mr-patches.git
git clone --branch v31.1 --depth 1 https://github.com/bitcoin/bitcoin.git
(cd bitcoin-p2mr-patches/patches && sha256sum -c ../SHA256SUMS)
(cd bitcoin-p2mr-patches/patches-m05 && sha256sum -c ../SHA256SUMS-m05)
(cd bitcoin-p2mr-patches/patches-spacing && sha256sum -c ../SHA256SUMS-spacing)
(cd bitcoin-p2mr-patches/patches-m1 && sha256sum -c ../SHA256SUMS-m1)
(cd bitcoin-p2mr-patches/patches-m2 && sha256sum -c ../SHA256SUMS-m2)
(cd bitcoin-p2mr-patches/patches-m3 && sha256sum -c ../SHA256SUMS-m3)
(cd bitcoin-p2mr-patches/patches-m4 && sha256sum -c ../SHA256SUMS-m4)
(cd bitcoin-p2mr-patches/patches-m4r && sha256sum -c ../SHA256SUMS-m4r)
cd bitcoin
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch \
       ../bitcoin-p2mr-patches/patches-spacing/*.patch ../bitcoin-p2mr-patches/patches-m1/*.patch \
       ../bitcoin-p2mr-patches/patches-m2/*.patch ../bitcoin-p2mr-patches/patches-m3/*.patch \
       ../bitcoin-p2mr-patches/patches-m4/*.patch ../bitcoin-p2mr-patches/patches-m4r/*.patch
cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_TESTS=OFF -DENABLE_WALLET=ON -DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF
cmake --build build -j"$(nproc)"
```

The build needs cmake 3.22 or later, GCC 12.1 or later (Ubuntu 22.04's default GCC 11 is too
old) or Clang 17 or later, pkgconf, libevent, the Boost headers and SQLite. On Debian or Ubuntu:
`sudo apt-get install build-essential cmake pkgconf python3 libevent-dev libboost-dev libsqlite3-dev`.
It also needs Rust 1.97.0 exactly, `rustc` and `cargo`, for the verifier of the M4-R series: with
rustup, `rustup toolchain install 1.97.0 --profile minimal`, which the series' `rust-toolchain.toml`
then selects; without rustup, those two on the `PATH`. The build uses only the crates vendored in
the series and downloads nothing.
`git am` needs a committer identity; if git has none, run it as
`git -c user.name=you -c user.email=you@example.com am ...`.

`patches/` and `patches-spacing/` alone, the build this guide described before M1, still follow
Ark-0, but such a node does not check the M1 signatures, and it has no P2MR wallet for section 4. A
node built without `patches-m2/`, as this guide described before M2, follows Ark-0 too, but does not
check the block signatures from height 20000 on. One built without `patches-m3/`, as this guide
described before M3, follows it as well, but does not run leaves of version `0xe0` or check their
rule from height 30000 on. One built without `patches-m4/`, as this guide described before M4-S,
follows it too (up to the first rescue spend, below), but does not check `OP_CHECKSLHDSA128S` from
height 32000 on. But one built without `patches-m4r/`, as this guide described before M4-R, stops at
the first block with a rescue spend, which can come from height 63000 on (section 5).

## 2. Configure it

Give the node a data directory of its own. Every signet, Ark-0 included, keeps its chain in the
same `signet/` subdirectory, so if this machine already runs Bitcoin Core, do not reuse its
`~/.bitcoin`: create an empty directory, put the file below in it as `bitcoin.conf`, and add
`-datadir=<that directory>` to every `bitcoind` and `bitcoin-cli` command in this guide. On a
machine without Bitcoin Core, `~/.bitcoin/bitcoin.conf` is fine.

```ini
signet=1
listen=0

[signet]
signetchallenge=5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae
signetpowtargetspacing=90@8064
addnode=node.ark0.si:38433
```

The first two settings under `[signet]` are rules of the network. With another challenge your
node is on a different signet altogether; without the spacing option it stops following Ark-0
at height 8064. Set both before the first start.

`node.ark0.si` is a node run by the operator. It listens on port 38433, over IPv4
only, and offers peer-to-peer connections and nothing else. Your node only needs its outgoing
connection to it; `listen=0` keeps it from accepting incoming ones.

Start the node and follow its progress:

```bash
./build/bin/bitcoind -daemonwait
./build/bin/bitcoin-cli getblockchaininfo
```

The chain is small, so the first sync takes minutes rather than hours.

## 3. Check that it is Ark-0

- At startup, `debug.log` (under `signet/` in the data directory) has these lines; another
  magic means another challenge:

  ```
  Signet P2MR tripwire rules from height 30000 in leaf version 0xe0; tripwire outputs 4319a38c0842846474ff038b54f22aec844147a97c2abc462bfb92957c1bc3a0:1 (BIP-341 NUMS) and 8991bb83673474b7c324f303365479c093d0b34f5e7d7ddf0648bcfdb1d42495:0 (drill key SHA256 077556bdd8807450ae70c372505543fdce8f6e97b51cc827a576a17233839cc2)
  Signet difficulty retargets against 90 s per block from height 8064
  Signet blocks need an ML-DSA-44 block signature from height 20000, by the key whose SHA256 is eee8548f51f25492de3ae430b7ce7ec549edfaa663b8f577f026ce0498fdd6ad
  Signet derived magic (message start): 40e8e404
  P2MR consensus rules active from height 1
  P2MR ML-DSA-44 rules active from height 8600
  P2MR SLH-DSA-SHA2-128s rules active from height 32000 (leaf versions 0xc0, 0xe0)
  Signet P2MR rescue lane from height 63000 for elliptic curve checks disabled by the tripwire (VOLEitH v0.2, statement 0x01); network id afd1fcbb77d445f14e06eb1a7dff52807c71d7765c311e6181e698734337b38e
  ```

- `./build/bin/bitcoin-cli getblockhash 1264` returns
  `00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4`, the last block of the
  snapshot in `snapshot/`. The genesis hash is the same on every signet and proves nothing.
- `./build/bin/bitcoin-cli getdeploymentinfo` lists `p2mr` as a buried deployment, active
  from height 1, `p2mr_mldsa44` as one active from height 8600, `p2mr_tripwire` as one with
  height 30000, `p2mr_slhdsa128s` as one with height 32000 and `p2mr_rescue` as one with height
  63000, and `P2MR` and `P2MR_MLDSA44` among the script flags (`P2MR_TRIPWIRE` too, from height
  30000, `P2MR_SLHDSA128S` from height 32000, and `P2MR_NO_RESCUE` below height 63000).
- `./build/bin/bitcoin-cli gettripwireinfo` reports `height` 30000, `leaf_version` `e0`, the two
  tripwire outputs of "Rule change at height 30000 (M3)" in `NETWORK.md`, each `unspent`, and
  `ec_disabled_next_block` false. After the drill described there, the drill output is `missing`
  and `ec_disabled_next_block` is true. Its `rescue` object reports `height` 63000 and the network
  id of the startup line. A node that has not yet synced past height 15748, where both outputs
  were created, reports both as `missing`, and logs so at startup; that is expected.
- `./build/bin/bitcoin-cli getpeerinfo` lists the public node, and once `getblockchaininfo`
  shows `blocks` equal to `headers` and `initialblockdownload` false, your node has caught up.

From there, the commands in sections 5 and 6 of [`REPRODUCE.md`](REPRODUCE.md) give the same
results on your node, with `./build/bin/bitcoin-cli` in place of its `acli` alias: they confirm
that P2MR is active and decode the P2MR script-path spend at height 123. Sections 7 and 8
replay the rejected blocks and transactions; follow them on a separate node started from the
snapshot, as that document describes, rather than rewinding the node you use to follow the
chain.

## 4. Get coins and spend them from P2MR

This needs the wallet series in `patches-m05/`, which both ways of getting the node in section 1
include. A node built from `patches/` and `patches-spacing/` alone, as this guide said before M1,
lacks it. Rebuild such a node in the `bitcoin/` clone from section 1: update your clone of this
repository with `git -C ../bitcoin-p2mr-patches pull`, run `git am --abort` if an earlier `git am`
stopped halfway, then `git reset --hard v31.1` (it discards any change of your own in that clone),
check all eight sets from there as section 1 does, with `../bitcoin-p2mr-patches/` in place of
`bitcoin-p2mr-patches/`, and run its `git am` line and `cmake --build build -j"$(nproc)"`. Then stop
the node with `./build/bin/bitcoin-cli stop`, wait until it has exited, and start it again with
`./build/bin/bitcoind -daemonwait` (both with your `-datadir`, if you use one); it keeps its chain.

BIP 360 defines no descriptor, so the wallet series adds a provisional one, `tmr()`: the tree
syntax of `tr()` without an internal key (`doc/p2mr.md` in the patched tree has the details). The
wallet never hands out P2MR addresses by itself; you import a `tmr()` descriptor and derive them.
The commands below build a two-leaf tree from keys under the new wallet's own extended private key,
each leaf checking one key with `OP_CHECKSIG`. With a data directory of your own, replace the
`cli()` line with `cli() { ./build/bin/bitcoin-cli -datadir="<dir>" "$@"; }`.

```bash
cli() { ./build/bin/bitcoin-cli "$@"; }
cli -named createwallet wallet_name=p2mr load_on_startup=true
XPRV=$(cli -rpcwallet=p2mr listdescriptors true | grep -o 'tprv[1-9A-HJ-NP-Za-km-z]*' | head -n 1)
DESC="tmr({pk($XPRV/0/*),pk($XPRV/1/*)})"
INFO=$(cli getdescriptorinfo "$DESC")
SUM=$(echo "$INFO" | grep -o '"checksum": "[a-z0-9]*"' | cut -d'"' -f4)
PUB=$(echo "$INFO" | grep -o '"descriptor": "[^"]*"' | cut -d'"' -f4)
cli -rpcwallet=p2mr importdescriptors "[{\"desc\": \"$DESC#$SUM\", \"timestamp\": \"now\", \"range\": [0, 99]}]"
FIRST=$(cli deriveaddresses "$PUB" "[0,0]" | grep -o 'tb1z[a-z0-9]*')
SECOND=$(cli deriveaddresses "$PUB" "[1,1]" | grep -o 'tb1z[a-z0-9]*')
echo "$FIRST"
```

`importdescriptors` should answer `"success": true`, and the last command prints a `tb1z` address.
`cli` and the variables live only in this shell. `XPRV` is the extended private key of the new
wallet's descriptors, and these commands pass it as an argument, where other users of the machine
could see it: keep this wallet for Ark-0 test coins.

1. Paste that address into the faucet at <https://faucet.ark0.si> and pass the human
   check. It sends 1 coin, which confirms with the next block. Each address and each visitor can
   claim once in 24 hours, and the faucet pays at most 100 claims in any hour.
   `cli -rpcwallet=p2mr getbalances` shows the coin as `untrusted_pending` until the block and as
   `trusted` after it.
2. Once the coin is `trusted`, spend it from P2MR, here to the second address. Ark-0 has no fee
   estimates, so give the fee rate yourself, in sat/vB:

   ```bash
   cli -rpcwallet=p2mr -named sendtoaddress address="$SECOND" amount=0.5 fee_rate=2
   ```

   The wallet signs through a script path. The input's witness is a 64-byte Schnorr signature,
   the 34-byte leaf script and a 33-byte control block: a control byte (the leaf version, `0xc0`,
   with its lowest bit set) and the 32-byte hash of the other leaf. The change goes to an ordinary
   address of the wallet, which never makes P2MR change.
3. Look the transaction id up in the explorer. For the P2MR input it shows the leaf script, the
   control block and the Merkle root recomputed from them, which has to equal the 32-byte witness
   program of the output being spent. Your node checked the same, and the signature, before it
   accepted the transaction.

To try an `OP_CHECKMLDSA44` leaf (M1) instead, the node's wallet cannot help: it neither makes
nor signs such leaves. [p2mr-ts](https://github.com/duncan0k/p2mr-ts) builds the S1 and S2
outputs and signs their spends ("Experimental: OP_CHECKMLDSA44 leaves" in its README), and your
node relays and checks the result like any other transaction. Use only the trees it builds: as
`NETWORK.md` explains, M1 restricts the spending of an output to committed ML-DSA-44 keys only if
every spendable leaf of its tree requires a valid signature under a key fixed by that leaf, as S1 and
S2 do.

Leaves of version `0xe0` (M3) are no different: the node's wallet neither makes nor signs them.
p2mr-ts builds the S3 outputs and signs spends through either of their leaves ("Experimental:
tripwired leaves (version 0xe0) and the S3 template" in its README), once the chain is six blocks
past height 30000. Pay nothing to such leaves before height 30000: until then they are not
executed, and anyone who knows a leaf and its path in the tree can spend the output through it.

Nor does the node's wallet make or sign leaves with `OP_CHECKSLHDSA128S` (M4-S), and p2mr-ts does
not build them yet. Pay nothing to such leaves before height 32000: until then the byte is
`OP_SUCCESS241`, and anyone who knows the leaf and its path in the tree can spend the output
through it.

Nor does the node's wallet make rescue spends (M4-R). The proofs are made off the node, with the
prover of [ark0-rescue](https://github.com/duncan0k/ark0-rescue), and a rescue gives away the key it
rescues: `doc/p2mr-rescue.md` in the patched tree, "Wallets and operators", says what a wallet has to
do first.

## 5. If something is wrong

- **Stuck at height 8063**, with the next block rejected for its difficulty: the node lacks
  `patches-spacing/` or the `signetpowtargetspacing=90@8064` option.
- **No blocks at all:** check the challenge, then that the node can reach
  `node.ark0.si` on port 38433.
- **`getdescriptorinfo` does not know `tmr`:** the node was built without `patches-m05/`, or still
  runs the binary from before; rebuild it and restart it as section 4 says.
- **`getdeploymentinfo` does not list `p2mr_mldsa44`:** the node was built without `patches-m1/`,
  or still runs the binary from before. It follows the chain but does not check the M1
  signatures; rebuild it and restart it as section 4 says.
- **`debug.log` has no `Signet blocks need an ML-DSA-44 block signature` line:** the node was
  built without `patches-m2/`, or still runs the binary from before. It follows the chain but does
  not check the block signatures; rebuild it (section 1) and restart it. If it accepted blocks at or
  above height 20000 before it was upgraded, start it once with `-reindex-chainstate` so that it
  checks them (`-reindex` on a pruned node).
- **`debug.log` has no `Signet P2MR tripwire rules` line, or `gettripwireinfo` answers
  `Method not found`:** the node was built without `patches-m3/`, or still runs the binary from
  before. It follows the chain but does not run leaves of version `0xe0` or check their rule;
  rebuild it (section 1) and restart it. If it accepted blocks at or above height 30000 before it
  was upgraded, start it once with `-reindex-chainstate` so that it checks them (`-reindex` on a
  pruned node).
- **`debug.log` has no `P2MR SLH-DSA-SHA2-128s rules` line, or `getdeploymentinfo` does not list
  `p2mr_slhdsa128s`:** the node was built without `patches-m4/`, or still runs the binary from
  before. It follows the chain but does not check the SLH-DSA signatures; rebuild it (section 1)
  and restart it. If it accepted blocks at or above height 32000 before it was upgraded, start it
  once with `-reindex-chainstate` so that it checks them (`-reindex` on a pruned node).
- **`debug.log` has no `Signet P2MR rescue lane` line, or `getdeploymentinfo` does not list
  `p2mr_rescue`:** the node was built without `patches-m4r/`, or still runs the binary from before.
  Rebuild it (section 1) and restart it before height 63000: from there it stops following the chain
  at the first block with a rescue spend. An M4-S node needs no reindex for this; an M3 node needs
  the one of the previous item.
- **Stuck at height 62999 or above, with the next block marked invalid:** the node ran without
  `patches-m4r/` when that block came and refused its rescue spend. Rebuild it (section 1), restart
  it, and let it continue with `./build/bin/bitcoin-cli reconsiderblock <hash of that block>`; the
  hash is in `debug.log`, in the line `InvalidChainFound: invalid block=`.
- **A prebuilt binary does not start**, saying that `GLIBC_2.39` or `GLIBCXX_3.4.32` is not found or
  that `libevent_core-2.1.so.7` cannot be opened: the system is older than the binaries need, or the
  libraries are missing (section 1). Install them, or use the image or a build from source.
- **`createwallet` says `Database already exists`, or a command says `Requested wallet does not
  exist or is not loaded`:** the wallet is left from an earlier run. In a new shell, define `cli`
  again; run `cli loadwallet p2mr` if the wallet is not loaded, then the lines from `XPRV=` on; they
  give the same addresses.
- **`listdescriptors` shows `xprv` rather than `tprv`, or the wallet never sees the faucet's
  coin:** `cli` may be talking to another Bitcoin Core on this machine. Use the `-datadir` form of
  `cli()` and run section 4 again from `createwallet` on. The wallet made on the other node keeps
  its keys there, and the new wallet has other addresses: a coin the faucet already sent can only be
  spent with the old keys, so claim again for the new address once 24 hours have passed. On the
  other node, `unloadwallet p2mr false` unloads the wallet created there and keeps it from loading
  at startup.
- **`sendtoaddress` says `Insufficient funds`:** check `cli -rpcwallet=p2mr getbalances`. If the
  coin is still `untrusted_pending`, wait for the next block.
- Report problems as issues on this repository.

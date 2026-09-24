# Joining Ark-0

Ark-0 is the experimental custom signet described in [`NETWORK.md`](NETWORK.md). It now has
one public node, so instead of only replaying the snapshot offline you can build the patched
node yourself, connect it, and check the P2MR rules against your own copy of the live chain.

Before you start:

- **One party produces every block.** The signet challenge is a 1-of-1 multisig and only the
  operator holds the key. Your node verifies every block but cannot produce one. Ark-0 is not
  a decentralized network.
- **The coins are worthless, and the chain may be reset.** The operator does not redeem,
  exchange or reward them, and joining earns nothing.
- **There is no faucet and no block explorer yet.** You can follow the chain and check its
  rules, but you will have no coins to spend.
- **Rule changes are announced in this repository before they apply.** The next one planned
  (milestone M1) is a soft fork: a node without it keeps following the chain but does not
  enforce the new rule. A new challenge would be a new network, and everyone would start again.

## 1. Build the node

A stock Bitcoin Core cannot follow Ark-0. It does not enforce the P2MR rules, and it rejects
the block at height 8064, because from that height Ark-0 retargets its difficulty against 90
seconds per block (see "Parameters" in `NETWORK.md`). You need the consensus series in
`patches/` and the retarget patch in `patches-spacing/`:

```bash
git clone https://github.com/duncan0k/bitcoin-p2mr-patches.git
git clone --branch v31.1 --depth 1 https://github.com/bitcoin/bitcoin.git
(cd bitcoin-p2mr-patches/patches && sha256sum -c ../SHA256SUMS)
(cd bitcoin-p2mr-patches/patches-spacing && sha256sum -c ../SHA256SUMS-spacing)
cd bitcoin
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-spacing/*.patch
cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_TESTS=OFF -DENABLE_WALLET=ON -DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF
cmake --build build -j"$(nproc)"
```

The build needs cmake 3.22 or later, GCC 12.1 or later (Ubuntu 22.04's default GCC 11 is too
old) or Clang 17 or later, pkgconf, libevent, the Boost headers and SQLite. On Debian or Ubuntu:
`sudo apt-get install build-essential cmake pkgconf python3 libevent-dev libboost-dev libsqlite3-dev`.
`git am` needs a committer identity; if git has none, run it as
`git -c user.name=you -c user.email=you@example.com am ...`. For a wallet that can create and
spend P2MR (`tb1z`) outputs, apply `patches-m05/` between the two sets, as the README shows.

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
addnode=ark0-node.cipherscope.io:38433
```

The first two settings under `[signet]` are rules of the network. With another challenge your
node is on a different signet altogether; without the spacing option it stops following Ark-0
at height 8064. Set both before the first start.

`ark0-node.cipherscope.io` is a node run by the operator. It listens on port 38433, over IPv4
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
  Signet derived magic (message start): 40e8e404
  P2MR consensus rules active from height 1
  Signet difficulty retargets against 90 s per block from height 8064
  ```

- `./build/bin/bitcoin-cli getblockhash 1264` returns
  `00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4`, the last block of the
  snapshot in `snapshot/`. The genesis hash is the same on every signet and proves nothing.
- `./build/bin/bitcoin-cli getdeploymentinfo` lists `p2mr` as a buried deployment, active
  from height 1, and `P2MR` among the script flags.
- `./build/bin/bitcoin-cli getpeerinfo` lists the public node, and once `getblockchaininfo`
  shows `blocks` equal to `headers` and `initialblockdownload` false, your node has caught up.

From there, the commands in sections 5 and 6 of [`REPRODUCE.md`](REPRODUCE.md) give the same
results on your node, with `./build/bin/bitcoin-cli` in place of its `acli` alias: they confirm
that P2MR is active and decode the P2MR script-path spend at height 123. Sections 7 and 8
replay the rejected blocks and transactions; follow them on a separate node started from the
snapshot, as that document describes, rather than rewinding the node you use to follow the
chain.

## 4. If something is wrong

- **Stuck at height 8063**, with the next block rejected for its difficulty: the node lacks
  `patches-spacing/` or the `signetpowtargetspacing=90@8064` option.
- **No blocks at all:** check the challenge, then that the node can reach
  `ark0-node.cipherscope.io` on port 38433.
- Report problems as issues on this repository.

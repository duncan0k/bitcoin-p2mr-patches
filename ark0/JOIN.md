# Joining Ark-0

Ark-0 is the experimental custom signet described in [`NETWORK.md`](NETWORK.md). It now has
one public node, so instead of only replaying the snapshot offline you can build the patched
node yourself, connect it, and check the P2MR rules against your own copy of the live chain.

Before you start:

- **One party produces every block.** The signet challenge is a 1-of-1 multisig and only the
  operator holds the key. Your node verifies every block but cannot produce one. Ark-0 is not
  a decentralized network.
- **It is a test network for trying things out.** It may be reset at any time.
- **The explorer and the faucet are the operator's too.** A block explorer at
  <https://ark0-explorer.cipherscope.io> shows blocks and transactions, with P2MR spends taken
  apart, and a faucet at <https://ark0-faucet.cipherscope.io> gives out coins to try P2MR with
  (section 4). You need neither of them to follow the chain or to check its rules.
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
`git -c user.name=you -c user.email=you@example.com am ...`.

For a wallet that can receive on and spend from P2MR (`tb1z`) outputs, which section 4 uses, add
the wallet series in `patches-m05/`: check it with the other two, before `cd bitcoin`, and apply it
between them with this `git am` line instead of the one above:

```bash
(cd bitcoin-p2mr-patches/patches-m05 && sha256sum -c ../SHA256SUMS-m05)
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch ../bitcoin-p2mr-patches/patches-spacing/*.patch
```

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

## 4. Get coins and spend them from P2MR

This needs a node built with `patches-m05/` as well (section 1). If yours was built without it,
rebuild it in the `bitcoin/` clone from section 1: run `git am --abort` if an earlier `git am`
stopped halfway, then `git reset --hard v31.1` (it discards any change of your own in that clone),
check `patches-m05/` from there with
`(cd ../bitcoin-p2mr-patches/patches-m05 && sha256sum -c ../SHA256SUMS-m05)`, and run the `git am`
line with all three sets and `cmake --build build -j"$(nproc)"`. Then stop the node with
`./build/bin/bitcoin-cli stop`, wait until it has exited, and start it again with
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

1. Paste that address into the faucet at <https://ark0-faucet.cipherscope.io> and pass the human
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

## 5. If something is wrong

- **Stuck at height 8063**, with the next block rejected for its difficulty: the node lacks
  `patches-spacing/` or the `signetpowtargetspacing=90@8064` option.
- **No blocks at all:** check the challenge, then that the node can reach
  `ark0-node.cipherscope.io` on port 38433.
- **`getdescriptorinfo` does not know `tmr`:** the node was built without `patches-m05/`, or still
  runs the binary from before; rebuild it and restart it as section 4 says.
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

# Reproducing Ark-0 offline

This walks an outside reader from an unmodified machine to a node that has
replayed the whole Ark-0 chain, decoded the P2MR spend at height 123, and
watched its own node reject the three malformed blocks. No network access to
Ark-0 is needed, and none is offered: everything comes from the snapshot and
the evidence files in this directory.

Every expected value below was produced by running these exact steps against
`snapshot/ark0-blocks-1264.dat` on a freshly built node with an empty data
directory and no peers.

## 1. Build the patched node

From the repository root:

```bash
bash apply.sh
```

That clones `bitcoin/bitcoin` at tag `v31.1` (commit `9be056a`), verifies the
patch checksums, applies the ten patches in `patches/` with `git am`,
configures, builds and runs the core tests. The binaries land in `bitcoin/build/bin`.

The snapshot ends at height 1264, below the first retarget that
`patches-spacing/` changes (8064), so replaying it needs `patches/` alone. A
node that follows the live chain past height 8063 also needs that patch and
`-signetpowtargetspacing=90@8064`; `NETWORK.md` explains why.

To build and test inside Kubernetes instead, use `contrib/k3s` on the `infra`
branch; its README covers the whole flow, including the runtime image
`p2mr-node:v31.1-p2mr` that the validation run below used.

Record that directory, and the data directory the node will use, as absolute
paths now — while the shell is still at the repository root and before any
`cd`:

```bash
BINDIR="$PWD/bitcoin/build/bin"
DATADIR="$PWD/ark0-node"
```

Every command below calls the daemon and the CLI through `$BINDIR`, never by
bare name. On a machine that already has Bitcoin Core installed, a bare
`bitcoind` or `bitcoin-cli` would resolve to that other build and quietly
produce output this document does not describe; on a machine that has none, it
would simply not be found. Any shell that runs a command from here needs both
variables set to these same absolute paths. If `apply.sh` was given an explicit
target directory, point `BINDIR` at `<that directory>/build/bin` instead.

Check the version:

```bash
"$BINDIR/bitcoind" -version | head -1
```

```
Bitcoin Core daemon version v31.1.0
```

## 2. Verify the snapshot

`SHA256SUMS` lists paths relative to this directory, so check it from here:

```bash
cd ark0 && sha256sum -c SHA256SUMS
```

The snapshot alone:

```
fe7de2bbfc811bce433db40bed803c8ec332069ab79c1cf58bd41d00513b38c4 *snapshot/ark0-blocks-1264.dat
```

The file is 444 796 bytes and holds 1265 records, heights 0 to 1264, in the
external block file format: 4 bytes of message start `40 e8 e4 04`, the block
size as a 32-bit little-endian integer, then the raw block.

## 3. Start a node on the snapshot

Use an empty data directory. The import is a one-time operation; later starts
do not need `-loadblock`.

```bash
CHALLENGE=5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae
SNAPSHOT=/path/to/ark0/snapshot/ark0-blocks-1264.dat

mkdir -p "$DATADIR"
"$BINDIR/bitcoind" -signet -signetchallenge=$CHALLENGE -datadir="$DATADIR" \
         -loadblock="$SNAPSHOT" \
         -connect=0 -listen=0 -dnsseed=0 \
         -txindex=1 -server=1 -printtoconsole
```

`-connect=0 -listen=0 -dnsseed=0` keeps the node entirely offline. `-txindex=1`
is optional: every `getrawtransaction` below passes a block hash, so the index
is not required, but the validation run used it.

The import takes about a second for 1265 blocks. In a second shell, set
`BINDIR` and `DATADIR` to the same two absolute paths as in step 1, then:

```bash
alias acli="$BINDIR/bitcoin-cli -datadir=$DATADIR -signet"
```

The double quotes matter: they bake both absolute paths into the alias at the
point it is defined, so `acli` keeps working after a `cd`. The CLI does not
need the challenge; `-signet` is enough for it to find the right port and data
directory.

## 4. Confirm you have the right chain

```bash
acli getblockhash 0
```

```
00000008819873e925422c1ff0f99f7cc9bbb232af63a077a480a3633bee1ef6
```

```bash
acli getblockcount
acli getbestblockhash
```

```
1264
00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4
```

```bash
acli getblockchaininfo
```

```json
{
  "chain": "signet",
  "blocks": 1264,
  "headers": 1264,
  "bestblockhash": "00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4",
  "bits": "1e0377ae",
  "target": "00000377ae000000000000000000000000000000000000000000000000000000",
  "difficulty": 0.001126515290698186,
  "time": 1789554384,
  "mediantime": 1789553932,
  "verificationprogress": 1,
  "initialblockdownload": false,
  "chainwork": "000000000000000000000000000000000000000000000000000000016cd0f6d4",
  "size_on_disk": 497080,
  "pruned": false,
  "signet_challenge": "5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae",
  "warnings": [
  ]
}
```

The genesis hash is the standard signet genesis and is the same on the public
signet; the `signet_challenge` field is what tells the two apart.

If the block count stays at 0, the node did not read the file. The usual cause
is a challenge mismatch: Core derives the message start from the challenge, so
a node started with a different one skips every record in the file as foreign.
Ark-0 blocks begin with `40e8e404`; confirm with `head -c 4 "$SNAPSHOT" | xxd`.

## 5. Confirm P2MR is active

```bash
acli getdeploymentinfo
```

The flags in force at the tip, and the deployment itself:

```json
"script_flags": [
  "CHECKLOCKTIMEVERIFY",
  "CHECKSEQUENCEVERIFY",
  "DERSIG",
  "NULLDUMMY",
  "P2MR",
  "P2SH",
  "TAPROOT",
  "WITNESS"
],
"deployments": {
  "p2mr": {
    "type": "buried",
    "active": true,
    "height": 1
  }
}
```

`p2mr` appears only because this is a custom signet. On mainnet, testnet3,
testnet4 and the default signet the patch series sets `P2MRHeight` to `INT_MAX`
and the deployment is not listed at all.

## 6. Find and decode the P2MR spend at height 123

```bash
acli getblockhash 123
```

```
000001531e5c2e64414868c90b2f550f6f3536e319625c1607f7961bd95641fd
```

```bash
acli getblock 000001531e5c2e64414868c90b2f550f6f3536e319625c1607f7961bd95641fd 1
```

The block holds two transactions, the coinbase and the spend:

```json
"height": 123,
"nTx": 2,
"tx": [
  "5efee47e8d359a76722d986012487efe4037d1c82fb9aab7070b775dafd1d889",
  "dfc5cdd473c6e7dcb73d046e0fde3950dbb52aeed7bcc583d57658976bf4131a"
]
```

```bash
acli getrawtransaction \
  dfc5cdd473c6e7dcb73d046e0fde3950dbb52aeed7bcc583d57658976bf4131a 2 \
  000001531e5c2e64414868c90b2f550f6f3536e319625c1607f7961bd95641fd
```

Verbosity 2 resolves the prevout, so the output type shows up directly:

```json
"txid": "dfc5cdd473c6e7dcb73d046e0fde3950dbb52aeed7bcc583d57658976bf4131a",
"hash": "e5e2048fa1fe791f5ad24a8eadabc8c6907da8a92a42105c91f240bca6b7414f",
"vin": [
  {
    "txid": "90792f87c306b13b84bce39ba959c6429f6b914c4e6639099503f4477f091a29",
    "vout": 0,
    "txinwitness": [
      "1693036a32c2a6263f35b60d3f5440c201cb931aad2c8f5e9727cbbf1305b08387479883b744a6194e039b717b5a8587342fd7413be9e7e5025a540ebd8bf372",
      "20692d6b7074760066c2ca4f131fc787afb4d5bf368b56298d9a3380b1c3dbb264ac",
      "c146c7eccffefd2d573ec014130e508f0c9963ccebd7830409f7b1b1301725e9fa"
    ],
    "prevout": {
      "height": 122,
      "value": 0.50000000,
      "scriptPubKey": {
        "hex": "5220903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00",
        "address": "tb1zjqeh0j8a7w8xju2kh4z2h6s6lk36garufwqct0uguq9z3nhtfvqqn7yq64",
        "type": "witness_v2_p2mr"
      }
    }
  }
]
```

The same classification from `decodescript`, with no chain context at all:

```bash
acli decodescript 5220903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00
```

```json
{
  "asm": "2 903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00",
  "desc": "addr(tb1zjqeh0j8a7w8xju2kh4z2h6s6lk36garufwqct0uguq9z3nhtfvqqn7yq64)#szww6ff6",
  "address": "tb1zjqeh0j8a7w8xju2kh4z2h6s6lk36garufwqct0uguq9z3nhtfvqqn7yq64",
  "type": "witness_v2_p2mr"
}
```

## 7. Replay the three block-level rejections

The three blocks in `evidence/badblock_*.hex` all build on height 126, so with
the node at height 1264 they are stale side branches and never get connected or
script-checked. Force the tip back to 126 first, and each submission becomes a
tip candidate that is fully validated.

```bash
acli invalidateblock 000001db43c12e5027f9d6fc9be7efb5d3e127a6bccc1eb7c1c8ff004a24a953
acli getblockcount && acli getbestblockhash
```

```
126
0000027bc9931451aa5c1d88b4298ea4d602b0c413c5f1d143497c82cbd7812b
```

Now submit each block and read what comes back:

```bash
acli submitblock "$(cat evidence/badblock_lowbit.hex)"
acli submitblock "$(cat evidence/badblock_wrongkey.hex)"
acli submitblock "$(cat evidence/badblock_wrongroot.hex)"
```

```
block-script-verify-flag-failed (Last bit of P2MR control block first byte must be 1)
block-script-verify-flag-failed (Invalid Schnorr signature)
block-script-verify-flag-failed (Witness program hash mismatch)
```

Each block has valid proof of work and a valid signet solution, so these are
consensus rejections from `ConnectBlock`, not header or solution failures. The
`block-` prefix confirms the flag is mandatory rather than policy.

```bash
acli getchaintips
```

```json
[
  { "height": 1264, "hash": "00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4", "branchlen": 1138, "status": "invalid" },
  { "height": 127, "hash": "000001dc88aca9b245f36030b0052cd9e3ed2b0fe902972b87ebe306538a82df", "branchlen": 1, "status": "invalid" },
  { "height": 127, "hash": "000001acc57234a1812326172e511d46797971e457fd925cc1220fd4f0b0d3b7", "branchlen": 1, "status": "invalid" },
  { "height": 127, "hash": "000001cca855f22bffd3f031c014613f292af754b06989306e10c42d62e1c0d6", "branchlen": 1, "status": "invalid" },
  { "height": 126, "hash": "0000027bc9931451aa5c1d88b4298ea4d602b0c413c5f1d143497c82cbd7812b", "branchlen": 0, "status": "active" }
]
```

The branch at 1264 is marked `invalid` only because `invalidateblock` is still
in force at height 127. Undo it:

```bash
acli reconsiderblock 000001db43c12e5027f9d6fc9be7efb5d3e127a6bccc1eb7c1c8ff004a24a953
acli getblockcount && acli getbestblockhash
```

```
1264
00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4
```

The three rejected branches stay listed as `invalid` beside the restored active
tip, which is the same picture the two live nodes have shown since 2026-09-15.

## 8. Replay the three mempool-level rejections

The same three transactions on their own, without blocks. Each spends an output
that is unspent in the snapshot, so nothing fails for a missing input:

```bash
acli gettxout 348ecdb32fc14674799707ac1019308ef21fbfde961d5afa414c5105cf77b5af 1
acli gettxout a0e747ae78d5634b1809b49938c14406f68af40bea864d4841f4932c5cdb9356 0
acli gettxout ef992e1486916263c48aa9a52952caf2f11707469f650d54aa26b4d4edb32164 1
```

All three return an unspent output of 0.50000000 with
`"type": "witness_v2_p2mr"` at the `tb1zjqeh...` address.

```bash
acli testmempoolaccept "[\"$(cat evidence/bad_lowbit.hex)\"]"
acli testmempoolaccept "[\"$(cat evidence/bad_wrongkey.hex)\"]"
acli testmempoolaccept "[\"$(cat evidence/bad_wrongroot.hex)\"]"
```

```json
[{ "txid": "f38e7029d57ecf0abe97074397003423b46cf719b95704f89e000bed6394af1d",
   "wtxid": "1f966b1e9ca2913d0fd13ca0d1627380470b68b3b64616e0719737a308f530c5",
   "allowed": false,
   "reject-reason": "mempool-script-verify-flag-failed (Last bit of P2MR control block first byte must be 1)" }]

[{ "txid": "a1a344b29367dcdf60ea42d3570cac5464c1d56d00c96645651f6f29622968ca",
   "wtxid": "cada695ee529f823bf5f1cc29fe9345f16628bd372f1f74511a0822764d24cee",
   "allowed": false,
   "reject-reason": "mempool-script-verify-flag-failed (Invalid Schnorr signature)" }]

[{ "txid": "f28ea4de3731f44e0b1a0f97ef33fc796426af1a97f31546bf1b21d542117967",
   "wtxid": "7fddf1c89fe7fc175c28a9c89a6994962517259cb947cd470388b09d8fb07986",
   "allowed": false,
   "reject-reason": "mempool-script-verify-flag-failed (Witness program hash mismatch)" }]
```

`reject-details` on each adds the input index and the outpoint, for example:

```
mempool-script-verify-flag-failed (Last bit of P2MR control block first byte must be 1), input 0 of f38e7029d57ecf0abe97074397003423b46cf719b95704f89e000bed6394af1d (wtxid 1f966b1e9ca2913d0fd13ca0d1627380470b68b3b64616e0719737a308f530c5), spending 348ecdb32fc14674799707ac1019308ef21fbfde961d5afa414c5105cf77b5af:1
```

The three reasons match the block-level ones one for one. The prefix differs
because the mempool applies `SCRIPT_VERIFY_P2MR` as policy on every chain,
while block validation applies it only after activation.

## 9. Shut down

```bash
acli stop
```

The data directory can be deleted; nothing in these steps writes outside it.

## Running it in a container

The validation that produced every output above ran in a throwaway Kubernetes
pod from the `p2mr-node:v31.1-p2mr` image built by `contrib/k3s`, with the
snapshot and the evidence files mounted read-only and a fresh data directory on
an `emptyDir`. Two things are worth knowing if you do the same:

- The node needs to bind its RPC port on loopback inside the container. Some
  hardened container runtimes deny socket creation to non-root users, which
  shows up as `libevent: socket: Operation not permitted` followed by `Unable
  to start HTTP server`. That is an environment restriction and has nothing to
  do with P2MR; running the container as root, or relaxing the restriction,
  resolves it.
- `invalidateblock` and `submitblock` change nothing on disk that outlives the
  pod, so the whole sequence is safe to repeat.

# Evidence files

Eight files: one confirmed P2MR spend, three malformed spends, the three blocks
that carried them, and the demo script tree. Every response quoted here was
produced by a node built from this patch series and restored from
`../snapshot/ark0-blocks-1264.dat` with no network access, except where a
response is attributed to the live nodes. `../REPRODUCE.md` is the procedure
that produces them.

All of these files contain public data only: raw transactions, raw blocks,
public keys, script hashes and Merkle roots. No private key material is
included in this directory, and none is needed to verify anything here.

Reference points used throughout:

| Height | Block hash |
|---|---|
| 122 | `000000f1247a873abc39c561ff5554468e9146aef2d6ffcb2792c713165978b0` (parent of 123) |
| 123 | `000001531e5c2e64414868c90b2f550f6f3536e319625c1607f7961bd95641fd` |
| 126 | `0000027bc9931451aa5c1d88b4298ea4d602b0c413c5f1d143497c82cbd7812b` |
| 127 | `000001db43c12e5027f9d6fc9be7efb5d3e127a6bccc1eb7c1c8ff004a24a953` (the valid one) |
| 1264 | `00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4` (snapshot tip) |

---

## 1. `spend_raw.hex`: the confirmed P2MR script-path spend

The transaction this whole exercise exists to produce: a spend of a
`witness_v2_p2mr` output through one leaf of its script tree, accepted by
consensus and confirmed.

| Field | Value |
|---|---|
| txid | `dfc5cdd473c6e7dcb73d046e0fde3950dbb52aeed7bcc583d57658976bf4131a` |
| wtxid | `e5e2048fa1fe791f5ad24a8eadabc8c6907da8a92a42105c91f240bca6b7414f` |
| Confirmed in | height **123**, block `000001531e5c2e64414868c90b2f550f6f3536e319625c1607f7961bd95641fd` |
| Spends | `90792f87c306b13b84bce39ba959c6429f6b914c4e6639099503f4477f091a29:0`, created at height 122 |
| Size / vsize / weight | 231 / 129 / 513 |
| Fee | 0.00010000 |

The witness has the three-element script-path shape: signature, leaf script,
control block.

```
1693036a32c2a6263f35b60d3f5440c201cb931aad2c8f5e9727cbbf1305b08387479883b744a6194e039b717b5a8587342fd7413be9e7e5025a540ebd8bf372
20692d6b7074760066c2ca4f131fc787afb4d5bf368b56298d9a3380b1c3dbb264ac
c146c7eccffefd2d573ec014130e508f0c9963ccebd7830409f7b1b1301725e9fa
```

The control block is 33 bytes: `0xc1` (leaf version `0xc0`, low bit set as
BIP-360 requires) followed by one 32-byte sibling hash, so `m = 1` and the tree
has two leaves.

`getrawtransaction <txid> 2 <blockhash>` reports the prevout, and this is the
line that matters: the node classifies the output being spent as
`witness_v2_p2mr`.

```json
"prevout": {
  "generated": false,
  "height": 122,
  "value": 0.50000000,
  "scriptPubKey": {
    "asm": "2 903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00",
    "desc": "addr(tb1zjqeh0j8a7w8xju2kh4z2h6s6lk36garufwqct0uguq9z3nhtfvqqn7yq64)#szww6ff6",
    "hex": "5220903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00",
    "address": "tb1zjqeh0j8a7w8xju2kh4z2h6s6lk36garufwqct0uguq9z3nhtfvqqn7yq64",
    "type": "witness_v2_p2mr"
  }
}
```

`decodescript 5220903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00`
classifies the same script standalone:

```json
{
  "asm": "2 903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00",
  "desc": "addr(tb1zjqeh0j8a7w8xju2kh4z2h6s6lk36garufwqct0uguq9z3nhtfvqqn7yq64)#szww6ff6",
  "address": "tb1zjqeh0j8a7w8xju2kh4z2h6s6lk36garufwqct0uguq9z3nhtfvqqn7yq64",
  "type": "witness_v2_p2mr"
}
```

Block 123 carries exactly two transactions, the coinbase and this spend:

```json
"nTx": 2,
"tx": [
  "5efee47e8d359a76722d986012487efe4037d1c82fb9aab7070b775dafd1d889",
  "dfc5cdd473c6e7dcb73d046e0fde3950dbb52aeed7bcc583d57658976bf4131a"
]
```

## 2. `p2mr_tree.json`: the demo script tree

The two-leaf tree behind that address. Public data only: an x-only public key,
two leaf scripts, their leaf hashes, the Merkle root, and the control block for
each leaf.

| Field | Value |
|---|---|
| Address | `tb1zjqeh0j8a7w8xju2kh4z2h6s6lk36garufwqct0uguq9z3nhtfvqqn7yq64` |
| scriptPubKey | `5220903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00` |
| Merkle root | `903377c8fdf38e697156bd44abea1afda3a4747c4b8185bf88e00a28ceeb4b00` |
| Demo x-only pubkey | `692d6b7074760066c2ca4f131fc787afb4d5bf368b56298d9a3380b1c3dbb264` |
| Leaf A (`leaf_csig`) | `<pubkey> OP_CHECKSIG`, leaf version `0xc0`, leaf hash `26ee5139bd2d5b7e0c5d15f6f743b3b6ed891227da5f14e08376ff73a7123413` |
| Leaf B (`leaf_ret`) | `OP_RETURN`, leaf version `0xc0`, leaf hash `46c7eccffefd2d573ec014130e508f0c9963ccebd7830409f7b1b1301725e9fa` |

Leaf A is the one spent at height 123; its control block carries leaf B's hash
as the sibling, which is why `c1` is followed by `46c7ecc...`. Unlike taproot,
the P2MR Merkle root is committed to directly, with no key tweak: the witness
program *is* the root.

`demo_xonly_pubkey` is a public key. The corresponding secret stays on the
machine that produced the chain and is deliberately absent from this
repository.

---

## The three malformed spends

Each one takes the valid spend and breaks exactly one P2MR rule, leaving
everything else well-formed, so the reason the node gives identifies the rule.
Each spends a separate output of the same `tb1zjqeh...` address, all three of
which are unspent in the snapshot: `gettxout` finds every one of them at the
snapshot tip with `"type": "witness_v2_p2mr"` and value 0.50000000.

The `reject-reason` strings below come from `testmempoolaccept` on the restored
node.

### 3. `bad_lowbit.hex`: control block parity bit cleared

The control block's first byte is `0xc0` instead of `0xc1`. BIP-360 requires
the low bit of `c[0]` to be set for every leaf version; taproot uses that bit
for output key parity, and P2MR, which has no tweak and so no parity to encode,
fixes it at 1.

| Field | Value |
|---|---|
| txid | `f38e7029d57ecf0abe97074397003423b46cf719b95704f89e000bed6394af1d` |
| wtxid | `1f966b1e9ca2913d0fd13ca0d1627380470b68b3b64616e0719737a308f530c5` |
| Spends | `348ecdb32fc14674799707ac1019308ef21fbfde961d5afa414c5105cf77b5af:1`, created at height 124 |
| Carried by | `badblock_lowbit.hex`, height 127 |

```
reject-reason: mempool-script-verify-flag-failed (Last bit of P2MR control block first byte must be 1)
```

### 4. `bad_wrongkey.hex`: signature under the wrong key

The witness is structurally valid and the control block is correct; the Schnorr
signature was made with a different key, so `OP_CHECKSIG` in the leaf fails.
This is the control case: it shows the leaf script really runs, rather than the
Merkle proof alone deciding the outcome.

| Field | Value |
|---|---|
| txid | `a1a344b29367dcdf60ea42d3570cac5464c1d56d00c96645651f6f29622968ca` |
| wtxid | `cada695ee529f823bf5f1cc29fe9345f16628bd372f1f74511a0822764d24cee` |
| Spends | `a0e747ae78d5634b1809b49938c14406f68af40bea864d4841f4932c5cdb9356:0`, created at height 126 |
| Carried by | `badblock_wrongkey.hex`, height 127 |

```
reject-reason: mempool-script-verify-flag-failed (Invalid Schnorr signature)
```

### 5. `bad_wrongroot.hex`: sibling hash replaced with zeros

The control block keeps `0xc1` but its 32-byte sibling hash is all zeros, so
folding the leaf hash with it does not reproduce the Merkle root in the witness
program.

| Field | Value |
|---|---|
| txid | `f28ea4de3731f44e0b1a0f97ef33fc796426af1a97f31546bf1b21d542117967` |
| wtxid | `7fddf1c89fe7fc175c28a9c89a6994962517259cb947cd470388b09d8fb07986` |
| Spends | `ef992e1486916263c48aa9a52952caf2f11707469f650d54aa26b4d4edb32164:1`, created at height 125 |
| Carried by | `badblock_wrongroot.hex`, height 127 |

```
reject-reason: mempool-script-verify-flag-failed (Witness program hash mismatch)
```

---

## The three rejected blocks

Each file is a complete block at height 127 building on height 126, with a
valid signet solution and proof of work that meets `nBits = 1e0377ae`. They are
real blocks in every respect except the one P2MR rule their payload transaction
breaks. Rejection therefore happens in `ConnectBlock`, not in the cheap header
checks.

All three share the same parent, `0000027bc9931451aa5c1d88b4298ea4d602b0c413c5f1d143497c82cbd7812b`
at height 126, and each is 572 bytes with two transactions.

| File | Block hash | Merkle root | Time | Nonce |
|---|---|---|---|---|
| `badblock_lowbit.hex` | `000001cca855f22bffd3f031c014613f292af754b06989306e10c42d62e1c0d6` | `9e4d962812fe9b462b72021a0ba1623e5104b7d0df712394aa28583500d903bd` | 1789451644 | 19833241 |
| `badblock_wrongkey.hex` | `000001acc57234a1812326172e511d46797971e457fd925cc1220fd4f0b0d3b7` | `7ede3511e890d3cc7fcfff5cbab0fcb6bf08af66d0094569fe89011ddd65a0f6` | 1789451645 | 7670044 |
| `badblock_wrongroot.hex` | `000001dc88aca9b245f36030b0052cd9e3ed2b0fe902972b87ebe306538a82df` | `fd4972035eda55247fee68853b10a8b99c35b7977ccbba06100b0120efb84fa3` | 1789451645 | 5934406 |

### What `submitblock` returns

With the node's tip forced back to height 126 (`invalidateblock` on the valid
height-127 block), each block is a candidate for the tip, so it is fully
connected and fully validated. `submitblock` returns:

```
badblock_lowbit.hex      block-script-verify-flag-failed (Last bit of P2MR control block first byte must be 1)
badblock_wrongkey.hex    block-script-verify-flag-failed (Invalid Schnorr signature)
badblock_wrongroot.hex   block-script-verify-flag-failed (Witness program hash mismatch)
```

The `block-` prefix rather than `mempool-` is the point. In v31.1 a script
failure is labelled by whether the failing flag is in
`STANDARD_NOT_MANDATORY_VERIFY_FLAGS`; patch 9 puts `SCRIPT_VERIFY_P2MR` into
`MANDATORY_SCRIPT_VERIFY_FLAGS`, so a P2MR failure at block level is reported
as a consensus failure and not misreported as a policy one.

`debug.log` records the same three, each followed by the tip staying put:

```
Block validation error: block-script-verify-flag-failed (Last bit of P2MR control block first byte must be 1), input 0 of f38e7029d57ecf0abe97074397003423b46cf719b95704f89e000bed6394af1d (wtxid 1f966b1e9ca2913d0fd13ca0d1627380470b68b3b64616e0719737a308f530c5), spending 348ecdb32fc14674799707ac1019308ef21fbfde961d5afa414c5105cf77b5af:1
[error] ConnectTip: ConnectBlock 000001cca855f22bffd3f031c014613f292af754b06989306e10c42d62e1c0d6 failed, block-script-verify-flag-failed (Last bit of P2MR control block first byte must be 1), input 0 of f38e7029d57ecf0abe97074397003423b46cf719b95704f89e000bed6394af1d (wtxid 1f966b1e9ca2913d0fd13ca0d1627380470b68b3b64616e0719737a308f530c5), spending 348ecdb32fc14674799707ac1019308ef21fbfde961d5afa414c5105cf77b5af:1
InvalidChainFound: invalid block=000001cca855f22bffd3f031c014613f292af754b06989306e10c42d62e1c0d6  height=127  log2_work=29.206105
InvalidChainFound:  current best=0000027bc9931451aa5c1d88b4298ea4d602b0c413c5f1d143497c82cbd7812b  height=126  log2_work=29.194789
```

with `Invalid Schnorr signature` for `000001acc57234a1...` and `Witness program
hash mismatch` for `000001dc88aca9b2...`.

### `getchaintips`

After the three submissions the restored node keeps them as three stored,
invalid side branches:

```json
[
  { "height": 127, "hash": "000001dc88aca9b245f36030b0052cd9e3ed2b0fe902972b87ebe306538a82df", "branchlen": 1, "status": "invalid" },
  { "height": 127, "hash": "000001acc57234a1812326172e511d46797971e457fd925cc1220fd4f0b0d3b7", "branchlen": 1, "status": "invalid" },
  { "height": 127, "hash": "000001cca855f22bffd3f031c014613f292af754b06989306e10c42d62e1c0d6", "branchlen": 1, "status": "invalid" }
]
```

Those are the same three hashes, with the same `invalid` status, that the two
live nodes have carried since the blocks were first submitted to them on
2026-09-15. On the live nodes `getchaintips` shows them beside an `active` tip
that keeps advancing; on the restored node the active tip stays at 1264.

`reconsiderblock` on the valid height-127 block puts the restored node back
where it started, at height 1264 and best block
`00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4`, with the
three invalid branches still listed. The check is repeatable and leaves nothing
behind.

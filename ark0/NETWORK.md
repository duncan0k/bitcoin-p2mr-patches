# Ark-0

Ark-0 is a private, experimental **custom signet** whose nodes run the BIP-360
Pay-to-Merkle-Root (P2MR) spending rules, enforced in block validation, on
Bitcoin Core v31.1 patched with the ten-commit series in `patches/`.

It exists to answer one question with evidence rather than assertion: *do these
rules actually hold on a running chain?* The material in this directory lets
anyone rebuild the node, replay the chain offline and re-derive every claim
below without contacting the network.

## What it is

- A custom signet: its own `-signetchallenge`, therefore its own network magic
  and its own block history, disjoint from every other Bitcoin network.
- A chain on which `DEPLOYMENT_P2MR` is a buried deployment active from height
  1, so every block after the genesis block is validated under the P2MR rules.
- A chain that carries a real P2MR script-path spend (height 123) and three
  side-branch blocks, each with valid proof of work and a valid signet
  solution, that both nodes rejected for a P2MR script failure (height 127).

## What it is not

- **Not mainnet and not the public signet.** The patch series sets `P2MRHeight`
  to `INT_MAX` on mainnet, testnet3, testnet4 and the default signet, so the
  deployment never activates there and `getdeploymentinfo` does not list it.
- **Not a public network.** There is no published endpoint, seed node, faucet
  or explorer, and none is planned. The snapshot in `snapshot/` is the only
  distribution channel for its blocks.
- **Not post-quantum.** Milestone M0 implements the P2MR *spending rules* only.
  The leaves in the demo tree are ordinary `OP_CHECKSIG` tapscript leaves
  signed with Schnorr over secp256k1. No post-quantum signature scheme is
  implemented, used, or validated anywhere in this series.

### What the evidence does not prove

Running chain or not, this material does **not** establish that Ark-0 is:

- the first, the only, or the canonical implementation of BIP-360;
- "the real bc1z" — the `tb1z` addresses here are signet bech32m witness-v2
  addresses under a locally activated deployment, not an address type that any
  public network recognises;
- mainnet-ready, production-ready, or reviewed to the standard a consensus
  change for a public network would require;
- a quantum-resistant network, or evidence that any quantum-resistant scheme
  works.

Describe it as: an experimental signet implementing the BIP-360 v0.12.1 P2MR
spending rules on Bitcoin Core v31.1, enforced at block validation,
independently reproducible.

## Parameters

| Parameter | Value |
|---|---|
| Chain type | signet, custom challenge |
| Signet challenge | `5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae` |
| Challenge decoded | `OP_1 <pubkey> OP_1 OP_CHECKMULTISIG` (1-of-1) |
| Challenge pubkey | `026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da` |
| Message start (network magic) | `40e8e404` |
| Genesis block hash | `00000008819873e925422c1ff0f99f7cc9bbb232af63a077a480a3633bee1ef6` |
| P2MR activation | buried deployment, active from height 1 |
| Target block interval | 90 s |
| `nBits` | `1e0377ae` (difficulty 0.001126515290698186) |
| Node version | `/Satoshi:31.1.0/` (v31.1.0) |
| Address prefix | `tb1z` (bech32m, witness v2, 32-byte program) |
| Output type name | `witness_v2_p2mr` |

### On the genesis hash

Bitcoin Core uses one genesis block for every signet; only the challenge
differs between them. The hash above is therefore the same on the public
signet and on Ark-0, and it is **not** what separates the two. What separates
them is the challenge, and with it the message start: Core derives the magic
from the challenge, so a node configured with a different challenge will not
even parse another signet's messages or block files.

The derivation is the first four bytes of the double SHA-256 of the
length-prefixed challenge script:

```
SHA256d( compact_size(37) || challenge )[0:4]
  = SHA256d( 25 5121026fc5...51ae )[0:4]
  = 40e8e404
```

## How it was produced

1. Bitcoin Core `v31.1` (tag `v31.1`, commit `9be056a`) was checked out.
2. The ten patches in `patches/` were applied with `git am`, in order; they add
   `SCRIPT_VERIFY_P2MR`, the witness-v2 program validation, the
   `WITNESS_V2_P2MR` output type and bech32m addresses, the buried
   `DEPLOYMENT_P2MR` with its per-chain heights, and the tests. `apply.sh`
   automates the whole step, `contrib/k3s` does the same inside Kubernetes.
3. Two nodes were started with `-signet` and the `-signetchallenge` above, so
   `P2MRHeight = 1` for this chain.
4. Blocks were produced with the upstream `contrib/signet/miner` at a 90-second
   cadence, signed with the challenge key.
5. A P2MR output was funded and then spent through a script path (height 123),
   and three malformed P2MR spends were mined into competing blocks at height
   127, each with valid proof of work and a valid signet solution.

## Snapshot

`snapshot/ark0-blocks-1264.dat` is the full chain from the genesis block to the
tip at the moment of export, in the external block file format that Bitcoin
Core's `-loadblock` reads: for each block, 4 bytes of message start, then the
block size as a 32-bit little-endian integer, then the raw block, with blocks
in height order.

| Field | Value |
|---|---|
| Export height | 1264 |
| Blocks in file | 1265 (heights 0 to 1264) |
| Genesis hash | `00000008819873e925422c1ff0f99f7cc9bbb232af63a077a480a3633bee1ef6` |
| Best block hash | `00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4` |
| Chain work at 1264 | `000000000000000000000000000000000000000000000000000000016cd0f6d4` |
| Tip block time | 1789554384 (2026-09-16 10:26:24 UTC) |
| File size | 444 796 bytes |
| SHA-256 | `fe7de2bbfc811bce433db40bed803c8ec332069ab79c1cf58bd41d00513b38c4` |

Every record was checked at export: the double SHA-256 of each 80-byte header
reproduces the hash the node reported for that height, and each header's
previous-block field matches the hash of the record before it.

The three rejected blocks are **not** in the snapshot. They were never part of
the active chain, so a height-ordered export cannot contain them; they ship
separately as raw hex under `evidence/`, and `REPRODUCE.md` shows how to feed
them back to a node and read the rejection.

Note that the live chain keeps advancing at 90-second intervals. The snapshot
is a point-in-time export, not the current tip, and a node restored from it
will sit at height 1264 with no peers.

## Contents of this directory

| Path | What it is |
|---|---|
| `NETWORK.md` | this file |
| `REPRODUCE.md` | step-by-step reproduction, with the output each step produced |
| `snapshot/ark0-blocks-1264.dat` | the chain, heights 0 to 1264 |
| `evidence/EVIDENCE.md` | what each evidence file is and what the node said about it |
| `evidence/*.hex` | the confirmed spend, three malformed spends, three rejected blocks |
| `evidence/p2mr_tree.json` | the demo script tree, public data only |
| `SHA256SUMS` | checksums for everything above |

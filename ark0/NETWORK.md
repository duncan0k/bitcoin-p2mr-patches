# Ark-0

Ark-0 is an experimental **custom signet** whose nodes run the BIP-360
Pay-to-Merkle-Root (P2MR) spending rules, enforced in block validation, on
Bitcoin Core v31.1 patched with the ten-commit series in `patches/`. Every
node the operator runs also carries the one patch in `patches-spacing/` (the
first two since 2026-09-22), which sets how the chain retargets from height
8064 on; see "Parameters" below.

It exists to answer one question with evidence rather than assertion: *do these
rules actually hold on a running chain?* The material in this directory lets
anyone rebuild the node, replay the chain offline and re-derive every claim
below without contacting the network. Since 2026-09-24 the network also has
one public node, and [`JOIN.md`](JOIN.md) describes how to follow the live
chain with a node of your own.

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
- **Not an open network in the usual sense.** Anyone can run a node and follow
  it (`JOIN.md`), but every block is produced by one party, the operator, who
  holds the key of the 1-of-1 challenge. There is one public node, and a block
  explorer and a faucet, all run by the operator. It is a test network for
  trying things out, and it may be reset at any time.
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
| Configured miner cadence | 90 s |
| Consensus target spacing (`nPowTargetSpacing`) | 600 s (signet default, unchanged) |
| Retarget target spacing | 600 s up to and including the retarget at 6048; 90 s from the retarget at 8064 on (`-signetpowtargetspacing=90@8064`, `patches-spacing/`) |
| `nBits` | `1e0377ae` (difficulty 0.001126515290698186) at the start; the retarget moves it, see below |
| Node version | `/Satoshi:31.1.0/` (v31.1.0) |
| Address prefix | `tb1z` (bech32m, witness v2, 32-byte program) |
| Output type name | `witness_v2_p2mr` |

The cadence and the two target spacings are separate things, and it is worth
not reading one for another. The cadence is an operational choice: the
interval this chain's block producer was told to wait between blocks. The
consensus target spacing is `nPowTargetSpacing`, which every signet inherits
as ten minutes; no patch in this repository touches it, and Core still uses
it for timeouts and estimates. The retarget target spacing is what the
difficulty adjustment measures each period against: the same 600 s on every
signet, and on this one 90 s from the retarget at 8064 on. That change, made
by the patch in `patches-spacing/`, is the one consensus change on this
network besides P2MR. Producing blocks faster than ten minutes is what a
signet is for: the signet solution decides who may produce a block, while
proof of work still paces how fast, as the next paragraphs record.

What the difference does over time was not written down here until it had
been observed. Signet keeps Bitcoin's difficulty adjustment: every 2016
blocks the target is multiplied by the time the last period actually took
over the time it was expected to take (2016 × 600 s), with that ratio
clamped to between a quarter and four. While blocks come less than 150 s
apart on average, as in the first two periods, a period takes less than a
quarter of the expected time, so the difficulty rises by the full factor of
four at each retarget; it keeps rising, by less
each time, until the proof of work on top of the producer's 90 s pause
brings the average spacing up to 600 s. Observed on this chain (node A,
2026-09-22):

| Heights | `nBits` | Difficulty | Average spacing |
|---|---|---|---|
| 0–4031 | `1e0377ae` | 0.0011 | 94.9 s (over 2016–4031) |
| 4032–6047 | `1e00ddeb` | 0.0045 | 116.3 s |
| 6048– | `1d377ac0` | 0.0180 | 168.4 s (over 6048–6295) |

So the `nBits` row above is the starting value, not a constant, and the
cadence row is the pause the producer keeps, not the spacing the chain
shows. Left alone, the retarget at 8064 would have raised the difficulty
again, and the spacing would have settled at ten minutes.

Instead, both nodes have run with `-signetpowtargetspacing=90@8064` since
2026-09-22, well before height 8064, so the retargets from 8064 on measure
each period against 90 s per block (2016 × 90 s = 181,440 s) rather than
against two weeks. A period that takes longer than that lowers the
difficulty, by up to a factor of four, back towards `powLimit`, where the
spacing is the producer's pause plus a moment of proof of work. The retarget
interval is still 2016 blocks. A node that replays or follows this chain
past height 8063 needs the patch in `patches-spacing/` and the same option;
without them it rejects the block at 8064, whose difficulty the two rules
set differently.

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

Note that the live chain keeps advancing, with the producer pausing 90 s
between blocks and the spacing following the difficulty (see "Parameters").
The snapshot is a point-in-time export, not the current tip, and a node
restored from it will sit at height 1264 with no peers. To follow the live
chain instead, see `JOIN.md`.

## Contents of this directory

| Path | What it is |
|---|---|
| `NETWORK.md` | this file |
| `REPRODUCE.md` | step-by-step reproduction, with the output each step produced |
| `JOIN.md` | how to build a node, connect it to the public node and follow the live chain |
| `snapshot/ark0-blocks-1264.dat` | the chain, heights 0 to 1264 |
| `evidence/EVIDENCE.md` | what each evidence file is and what the node said about it |
| `evidence/*.hex` | the confirmed spend, three malformed spends, three rejected blocks |
| `evidence/p2mr_tree.json` | the demo script tree, public data only |
| `SHA256SUMS` | checksums for everything above |

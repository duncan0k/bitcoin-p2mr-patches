# Ark-0

Ark-0 is an experimental **custom signet** whose nodes run the BIP-360
Pay-to-Merkle-Root (P2MR) spending rules, enforced in block validation, on
Bitcoin Core v31.1 patched with the ten-commit series in `patches/`. Every
node the operator runs also carries the one patch in `patches-spacing/` (the
first two since 2026-09-22), which sets how the chain retargets from height
8064 on; see "Parameters" below. Since 2026-09-25 they also carry the wallet
series in `patches-m05/` and the M1 series in `patches-m1/`, which adds one
experimental rule from height 8600; see "Rule change at height 8600 (M1)".
Since 2026-09-30 they also carry the M2 series in `patches-m2/`, which from
height 20000 requires an ML-DSA-44 signature on every block; see "Rule change
at height 20000 (M2)". Since 2026-10-12 they also carry the M3 series in
`patches-m3/`, which from height 30000 runs P2MR leaves of version `0xe0`,
whose elliptic curve signature checks stop working once one of two
designated outputs is spent; see "Rule change at height 30000 (M3)". Since
2026-10-19 they also carry the M4-S series in `patches-m4/`, which from
height 32000 adds an SLH-DSA signature check to P2MR leaves; see "Rule
change at height 32000 (M4-S)". Since [ROLL DATE] they also carry the M4-R
series in `patches-m4r/`, which from height 63000 opens a rescue lane in P2MR
leaves of version `0xe0`, a hard fork; see "Rule change at height 63000
(M4-R)".

It exists to answer one question with evidence rather than assertion: *do these
rules actually hold on a running chain?* The material in this directory lets
anyone rebuild the node, replay the chain offline and re-derive every claim
below without contacting the network, apart from what happened after height
1264, where the snapshot ends: the retarget at 8064 and the rule changes from
8600 on need a node that follows the live chain. Since 2026-09-24 the network
has one public node, and [`JOIN.md`](JOIN.md) describes how to follow the live
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
  holds the key of the 1-of-1 challenge and the ML-DSA-44 block key that M2
  requires from height 20000. There is one public node, and a block explorer,
  a faucet and a web wallet, all run by the operator. It is a test network for
  trying things out, and it may be reset at any time.
- **Not post-quantum.** Milestone M0 implements the P2MR *spending rules* only.
  The leaves in the demo tree are ordinary `OP_CHECKSIG` tapscript leaves
  signed with Schnorr over secp256k1. No post-quantum signature scheme is
  implemented, used, or validated anywhere in that series, `patches/`.
- **One experimental exception from height 8600 (M1).** Since that height the
  nodes built with `patches-m1/`, the operator's among them, also check one
  post-quantum signature scheme, ML-DSA-44, in outputs that opt into it.
  Outputs that do not opt in are unchanged. See "Rule change at height 8600
  (M1)" below.
- **A second one from height 20000 (M2).** From that height the nodes built
  with `patches-m2/` also require an ML-DSA-44 signature by one fixed key on
  every block, next to the solution of the classical signet challenge. The
  proof-of-work rules and the rules for spending outputs are unchanged. See
  "Rule change at height 20000 (M2)" below.
- **Not evidence that any elliptic curve key is broken (M3).** From height
  30000 the nodes built with `patches-m3/` also run P2MR leaves of an
  experimental leaf version, `0xe0`, and refuse elliptic curve signatures in
  them from the block after one of two designated outputs is spent. The
  operator holds the key of one of these outputs and spent it on
  2026-10-30, at the announced time, as a drill. Leaves of version `0xc0`,
  and outputs without leaves of version `0xe0`, are unchanged. See "Rule
  change at height 30000 (M3)" below.
- **A third exception from height 32000 (M4-S).** From that height the nodes
  built with `patches-m4/` also check a second post-quantum signature scheme,
  SLH-DSA-SHA2-128s, in outputs that opt into it. Outputs that do not opt in
  are unchanged. See "Rule change at height 32000 (M4-S)" below.
- **Not a general way to recover frozen coins (M4-R).** From height 63000 the
  nodes built with `patches-m4r/` also accept, in leaves of version `0xe0`, a
  zero-knowledge proof that the spender knows the seed behind a key in place of
  an elliptic curve signature (the mempool takes it only once the tripwire has
  disabled those signatures). It helps only keys whose secret key is
  `HMAC-SHA256(K, x || suffix)` of a seed `x`, as the Ark-0 web wallet derives
  its Schnorr keys; its proof system has had no external review; and it relaxes
  the rules: an M3 or M4-S node stops at the first block that uses it. See
  "Rule change at height 63000 (M4-R)" below.

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

## Rule change at height 8600 (M1)

From height 8600 Ark-0 has one more rule, an experimental one, called
milestone M1 here, which the nodes built with `patches-m1/` enforce, the
operator's among them. In a P2MR leaf that is executed (leaf version
`0xc0`, at depth one or more), byte `0xf0` becomes `OP_CHECKMLDSA44`. It takes
eight stack elements: a 1312-byte ML-DSA-44 public key (FIPS 204) in three,
which the two standard leaf scripts push from the leaf itself, and a 2420-byte
signature (2421 with an explicit hash type) in five, from the witness. It
checks the signature over the BIP-341 tapscript signature message computed
with `key_version` 1, with the context string `P2MR/MLDSA44/EXP0`. A non-empty
signature that does not verify makes the script fail, so a spend that carries
one is invalid, and so is any block that contains it. An empty signature
makes the opcode push false, as `OP_CHECKSIG` does.

- **Below height 8600, and outside such leaves, nothing changes.** In tapscript
  the byte is `OP_SUCCESS240`, which makes a script succeed without running
  it. Until the rule applies, an output whose leaf uses the opcode can be
  spent without the ML-DSA-44 signature by anyone who knows that leaf and its
  path in the tree.
- **It is a soft fork.** A node without M1 keeps following the chain, but it
  does not check these signatures, and it does not relay spends that use the
  opcode: to such a node they are non-standard.
- **Once the rule applies, of the leaves that are executed, only two scripts
  with the opcode are relayed:** the public key followed by `OP_CHECKMLDSA44`
  (S1), and S1 followed by `OP_VERIFY <x-only key> OP_CHECKSIG` (S2). Other
  scripts with the opcode are not relayed, but a block may still contain them,
  and some of them pass without a valid signature by a committed key: with
  an empty one, where the script accepts a false result, or with a signature
  under a key the spender chooses and puts in the witness.
- **The tree decides the rest, and a node cannot see it.** A spend reveals one
  leaf, and other leaves of the tree may provide other ways to spend the
  output. M1 restricts the spending of the whole output to committed
  ML-DSA-44 keys only if every spendable leaf requires a valid signature under
  a key fixed by that leaf, as S1 and S2 do; for example, a tree of exactly
  two leaves, both at depth one and both of leaf version `0xc0`: an S1 or S2
  leaf and an `OP_RETURN` leaf. At another leaf version an `OP_RETURN` leaf is
  not executed, and a spend through it needs no signature; a single leaf sits
  at depth zero and is spent without being executed. Once the rule applies, an
  executed S1 or S2 leaf protects the spending path of its own output and
  nothing more.
- **It is not part of BIP-360**, which defines no post-quantum signature check,
  and it is not a proposal for Bitcoin. Blocks on Ark-0 are authorized by the
  classical signet challenge, and from height 20000 by M2's block signature as
  well.
- **The height is a rule of this network and will not change.** The series is
  in `patches-m1/` (`README.md`, "Experimental M1 series"), and
  `doc/p2mr-mldsa44.md` in the patched tree specifies the rule.

### What the chain shows

Two outputs of the protected shape above were funded for the change at height
7278, in `bb7be821d552491aaa89111b22b8e6acbd07c7bb3ceefeeb173d44a000e516b9`
(outputs 0 and 2, 1 coin each, locked to test keys): an S1 leaf and an
`OP_RETURN` leaf, both of version `0xc0` at depth one. Output 0 was spent
below the height with a signature that does not verify; output 2 above it,
first in a block that the operator's nodes refused, then with a valid
signature:

| Height | Block | What happened |
|---|---|---|
| 7481 | `0000000d5b3e0b1524c593d31bba696c383dd6d19958be2225c430908592e220` | Below 8600: a spend of output 0, `7cbbe7f3d6d7a0f752b39b8d85fa4f803e3176d4839995df92abf775450dae40`, is valid although its ML-DSA-44 signature does not verify (a valid signature with bit 0 of byte 100 flipped, as at 8714 below), because the byte is still `OP_SUCCESS240`. No node relayed it (`OP_SUCCESSx reserved for soft-fork upgrades`); the operator put it into this block, which all four nodes the operator runs accepted. |
| 8600 | `0000001d55f182b7fe90f69a6ed37e21169d99607416184d8fffee7a01afff95` | The first block under the rule, 2026-09-26 23:38:02 UTC. |
| 8714 | `00000054a2b26fd2cfb2d4ab0a271b619ec0ba382c2216ce85e4b88ac1841182`, not in the chain | A block holding a spend of output 2, `467300d5b4bfdcf94c9f2127b59dd5e71829228f3ab9ab9dbdc06de0dcd191db`, whose ML-DSA-44 signature is a valid one with bit 0 of byte 100 (counting from 0) flipped. Each of the four nodes the operator runs validated it and refused it: `block-script-verify-flag-failed (Invalid ML-DSA-44 signature)`. |
| 8715 | `0000004813f99aa8205b33255208fd9191d989fbe6b2efd9f58aadac17a4e481` | The same spend with a valid signature, made separately (signing is randomized, so it differs from the one above in more than that bit), sent to the public node, relayed by it to the other nodes, and mined. |

The refused block is `evidence/m1_badblock_mldsa44.hex`. A node built with
`patches-m1/` that follows the chain refuses it too, once its tip is back at
8713. Do this only on a node you can rewind for a moment: from the first
command to the last, it disconnects every block above 8713. Run the commands
in this directory, with that node's `bitcoin-cli` (and its `-datadir`, if it
has one of its own):

```bash
bitcoin-cli invalidateblock 0000005be044ace3b4571df44aba0cc9e47a5629e37ba134baf9f715a8425a4b
bitcoin-cli submitblock "$(cat evidence/m1_badblock_mldsa44.hex)"
bitcoin-cli reconsiderblock 0000005be044ace3b4571df44aba0cc9e47a5629e37ba134baf9f715a8425a4b
```

The first command sets aside the block the chain has at 8714. `submitblock`
answers `block-script-verify-flag-failed (Invalid ML-DSA-44 signature)`, or
`duplicate-invalid` on a node that has refused the block before; either way
`bitcoin-cli getchaintips` then lists it as `invalid`. The last command returns
the node to the chain it followed.

## Rule change at height 20000 (M2)

From height 20000 every block on Ark-0 also needs an ML-DSA-44 (FIPS 204)
signature by one fixed public key, next to the solution that the classical
signet challenge requires. This experimental rule is called milestone M2 here.
The nodes built with `patches-m2/` enforce it, the operator's among them, and
`doc/signet-pqblock.md` in the patched tree specifies it.

- **Where the signature is.** In the coinbase output that holds the witness
  commitment, right after the commitment: one 2425-byte push of the tag
  `PQB1`, a version byte `01` and the 2420-byte signature. The signet solution
  follows it, and nothing else may. From height 20000 on, a block whose output
  has any other form is invalid (`bad-signet-pqblk`).
- **What it covers.** The block's version, previous block and time, and every
  transaction, the coinbase included, apart from the signature bytes and the
  signet solution push. A node with the rule therefore accepts no block whose
  contents the holder of this key did not sign: with the classical challenge
  key alone, no block with contents of its own can be made that such a node
  accepts. Like the signet solution, the signature does not
  cover `nNonce` and `nBits`, and it does not cover the signet solution either,
  so a block with the same contents can appear under other hashes.
- **The key and the height are fixed in the code for this network's
  challenge** (`ARK0_PQ_BLOCK_PUBKEY` and `ARK0_PQ_BLOCK_HEIGHT`). The key is
  the operator's, like the classical challenge key, and a block needs both.
  A node built with `patches-m2/` logs the height and the key's SHA256 when it
  starts:

  ```
  Signet blocks need an ML-DSA-44 block signature from height 20000, by the key whose SHA256 is eee8548f51f25492de3ae430b7ce7ec549edfaa663b8f577f026ce0498fdd6ad
  ```

- **Below height 20000, M2 rejects no block.** The operator's block producer
  signs every block from height 18000 on, and a node started with
  `-debug=validation` logs, for each block below 20000 that it validates
  other than the genesis block, whether its signature is valid, invalid, or
  absent or not in the required form. So the signatures can be checked on the
  chain before they are enforced.
- **It is a soft fork.** A node without `patches-m2/` accepts every block a
  node with it accepts and keeps following the chain, but does not check the
  signature. A node that accepted blocks at or above height 20000 before it
  upgraded has not checked them; one start with `-reindex-chainstate`
  (`-reindex` on a pruned node) checks them.
- **What it does not change.** Who produces blocks: the operator, as before,
  now with two keys instead of one. The proof-of-work rules and the rules for
  spending outputs are unchanged; what M2 adds is the push in the coinbase, and
  a node without the rule checks nothing new. It is not part of BIP 325 or
  BIP-360, and it is not a proposal for Bitcoin.
- **The height is a rule of this network and will not change.** The series is
  in `patches-m2/` (`README.md`, "Experimental M2 series").

### What the chain shows

The block producer has signed every block since height 18000. For each block
from 18000 to 19999, nodes A and B, the two the operator runs in its cluster,
logged that its ML-DSA-44 block signature was valid, and so did the public node
and the relay, which had the same logging switched on for the purpose, for the
blocks from 18000 to 18032:

```
CheckSignetPQBlock: block <hash> below height 20000, ML-DSA-44 block signature valid
```

| Height | Block | What happened |
|---|---|---|
| 18000 | `000000100bb3026a2303ce46839febbe47c3d50150cec0d137b2324a3f493dfb` | The first block with an ML-DSA-44 block signature, 2026-10-08 20:48:23 UTC. |
| 20000 | `000001153177f182a96b4ad6903ed9279cef82b0cfd340f85b55d1ac81ccc1d4` | The first block under the rule, 2026-10-11 04:41:28 UTC. It is `evidence/m2_block_20000.hex`. |

From height 20000 a node with the rule logs no line for a valid block
signature: it accepts the block or refuses it. All four nodes the operator runs
accepted 20000 and the blocks after it.

At height 20095 the operator built five blocks on
`00000107c067fe63e95dab9c05783e221b8be0024a7c79059c9811bd7a40695f` (20094), on
2026-10-11, each with valid proof of work and a valid classical signet
solution, and with an ML-DSA-44 block signature that is missing or wrong in
one of five ways. Each of the four nodes the operator runs refused each of them
with `bad-signet-pqblk` and kept nothing of any, neither a chain tip nor a
header:

| File | Block | The push after the witness commitment |
|---|---|---|
| `evidence/m2_badblock_nopush.hex` | `00000001283c2155356bf9afdb2886941e35ff4433d6d5b42fa484bcba44f3b1` | absent: the signet solution follows the commitment directly |
| `evidence/m2_badblock_flipped.hex` | `0000007f9ed4affe71837767c9172689b674562e0c9395c53e0dc62290db1e31` | the block's valid push, with bit 0 of its byte 100 (counting from 0; byte 95 of the signature) flipped |
| `evidence/m2_badblock_otherkey.hex` | `0000011cb625f3b9fc44c6d93a7eeb911669fbe71e47c12b6260e544ccec9a60` | a valid signature of the block by another key, derived from a public string; it controls nothing |
| `evidence/m2_badblock_trailing.hex` | `000000fbb48c81097e88ed87183c466f68207966ce7b91a4dcc91141a7d59811` | the block's valid push, followed by `OP_0` |
| `evidence/m2_badblock_nonpush.hex` | `00000054caad64dc405a3c084faa341d1b847af0a9adbf3a0737bc20b421cc59` | `OP_1NEGATE` in place of the push |

The block producer kept running meanwhile, and block 20095 in the chain is the
one it made,
`000000cbc10a3a5314bc52f7c0a5d44c566e505ebb42c0eb65043573d2859b5f`.

Give these blocks only to a node built with `patches-m2/`. A node without the
rule checks nothing new and takes each of them as a valid block. A node with
the rule refuses each of them at any height of its own chain and as often as
it is given it, with nothing to set aside first: it reads the height from the
coinbase (BIP 34) and checks the signature before it stores anything, and it
keeps no record of a block that fails. Run, in this directory, with that
node's `bitcoin-cli` (and its `-datadir`, if it has one of its own):

```bash
for f in evidence/m2_badblock_*.hex; do bitcoin-cli submitblock "$(cat "$f")"; done
```

Each answer is `bad-signet-pqblk`. For each hash in the table,
`bitcoin-cli getblockheader <hash>` then still answers `Block not found`, and
`bitcoin-cli getchaintips` lists none of them.

## Rule change at height 30000 (M3)

From height 30000 Ark-0 has one more experimental rule, called milestone M3
here: a drill of disabling elliptic curve signature checks inside one P2MR
leaf version, `0xe0`. From that height leaves of this version are executed,
and their elliptic curve signature checks stop working from the block after
one of two designated outputs, the tripwire outputs, is spent. The operator
holds the key of one of them and spent it in a drill on 2026-10-30, at the
announced time. The nodes built with `patches-m3/` enforce the rule, the
operator's among them, and `doc/p2mr-tripwire.md` in the patched tree
specifies it.

The two tripwire outputs were created at height 15748. Each is a P2MR output
of two leaves of version `0xc0` at depth one, `<P> OP_CHECKSIG` and
`OP_RETURN`:

| Label | Outpoint | `P` |
|---|---|---|
| `BIP-341 NUMS` | `4319a38c0842846474ff038b54f22aec844147a97c2abc462bfb92957c1bc3a0:1` | `50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0`, the NUMS point `H` of BIP-341, whose private key nobody knows: nobody can spend this output |
| `drill` | `8991bb83673474b7c324f303365479c093d0b34f5e7d7ddf0648bcfdb1d42495:0` | `2f605bb3d127c17ff8e88e214587876b0c7623dbad79f8a1115fa172bda0315f`, whose private key the operator holds |

- **Leaves of version `0xe0` run from height 30000.** From there a leaf of
  this version at depth one or more (its control block starts with `0xe1`)
  is executed exactly as a leaf of version `0xc0`: the tapscript rules, the
  validation weight budget and `OP_CHECKMLDSA44` included. The tapleaf hash
  commits to the leaf version, so a signature made for a leaf of one version
  is not valid in a leaf of the other. Below the height such a leaf is not
  executed: anyone who knows it and its path in the tree can spend it
  without a signature. Nodes do not relay such spends, and no coins should
  be paid to these leaves before then. A single leaf sits at depth zero and
  is spent without being executed, at any height.
- **When the checks stop working.** A tripwire output is present while it is
  unspent and was created below height 30000; otherwise it is missing. From
  height 30000 on, in every block whose parent leaves either output missing,
  `OP_CHECKSIG`, `OP_CHECKSIGVERIFY` and `OP_CHECKSIGADD` in an executed leaf
  of version `0xe0` fail the script when they meet a 32-byte public key and a
  non-empty signature, valid or not. The block that spends a tripwire output
  is still checked with the checks working. They fail from the next block
  on, or from height 30000 if the spend comes before it. From then on the
  mempool refuses a transaction that makes such a check, and a block that
  contains one is invalid, both with the reason
  `Elliptic curve signature checks are disabled in this leaf`. Only a
  reorganization that undoes the spend gives the checks back.
- **What it does not change.** Who produces blocks: the operator, as before.
  Empty signatures, `OP_CHECKMLDSA44`, leaves of version `0xc0`, P2TR,
  witness v0 and legacy scripts are not affected, and neither are M2's block
  signature and the proof-of-work rules.
- **The drill.** The operator spent the drill output on 2026-10-30, as
  announced for 08:00 UTC, in block 36897 (see "What the chain shows"
  below). From block 36898 on, the checks fail on this chain for as long as
  no reorganization undoes block 36897. Nodes with the rule remove from
  their mempools the spends that fail from then on, with their descendants.
  Nobody can spend the NUMS output.
- **What it shows is narrow.** A node with the rule refuses, at the block
  level, any elliptic curve signature in a leaf of version `0xe0` from the
  block after the one that spends a tripwire output, or from height 30000 if
  that comes later. It does not show that any elliptic curve key is broken,
  and it cannot tell when one will be. On a signet with a single block
  signer, it says nothing about how miners or a wider network would adopt
  such a rule.
- **The mixed address.** Bitcoin Core's wallet does not make or sign leaves
  of version `0xe0`. Once the chain is six blocks past height 30000, the
  operator's web wallet, <https://wallet.ark0.si>, adds a third
  address to its Schnorr and ML-DSA-44 ones: template S3 of
  `doc/p2mr-tripwire.md`, a tree of exactly two leaves of version `0xe0` at
  depth one, `<x-only key> OP_CHECKSIG` and S1 (see "Rule change at height
  8600 (M1)"). The wallet spends from it through the Schnorr leaf while the
  checks work, and through the ML-DSA-44 leaf once the node reports them
  disabled for the next block, so its coins stay spendable after the drill.
  The wallet avoids two things. An output whose leaves of version `0xe0` all
  require an elliptic curve signature cannot be spent through them after the
  drill. A leaf that requires both kinds of signature, as M1's S2 would in a
  leaf of version `0xe0`, cannot be spent at all.
- **What a node logs and reports.** A node built with `patches-m3/` logs the
  height and the two outputs when it starts, with the SHA256 of the drill
  output's key:

  ```
  Signet P2MR tripwire rules from height 30000 in leaf version 0xe0; tripwire outputs 4319a38c0842846474ff038b54f22aec844147a97c2abc462bfb92957c1bc3a0:1 (BIP-341 NUMS) and 8991bb83673474b7c324f303365479c093d0b34f5e7d7ddf0648bcfdb1d42495:0 (drill key SHA256 077556bdd8807450ae70c372505543fdce8f6e97b51cc827a576a17233839cc2)
  ```

  It also logs the state of the outputs at startup and when the chain
  reaches height 30000, and each spend of a tripwire output once the block
  that spends it is in its active chain:

  ```
  P2MR tripwire <txid:vout> spent by <txid> in block <hash> (height <N>): elliptic curve checks in leaf version 0xe0 are disabled from height <H>
  P2MR tripwire <txid:vout> spent by <txid> in block <hash> (height <N>): elliptic curve checks in leaf version 0xe0 are already disabled
  ```

  `<H>` is `N + 1`, or 30000 if the spend comes before that height. The
  second form is logged when the checks were already off before the block.

  `bitcoin-cli gettripwireinfo` returns the tip it reads the state at, the
  height and the leaf version, each output with its key, label and `state`,
  `unspent` or `missing`, and `ec_disabled_next_block`.
- **It is a soft fork.** A node without `patches-m3/` accepts every block a
  node with it accepts and keeps following the chain, but does not execute
  leaves of version `0xe0` and does not check the rule. A node that accepted
  blocks at or above height 30000 before it upgraded has not checked them;
  one start with `-reindex-chainstate` (`-reindex` on a pruned node) checks
  them.
- **It is not part of BIP-360 or BIP-361**, and it is not a proposal for
  Bitcoin. It follows the tripwire idea (P2XX-T) in Pieter Wuille's mail of
  2026-06-25,
  [Giving teeth to expected EC disabling: P2XX(-T)(-ML)](https://groups.google.com/g/bitcoindev/c/aWYtPLVPZ3U),
  with two differences: what the rule constrains is one experimental leaf
  version, and the trigger is the spend of designated outputs, where the
  mail's trigger is any input that successfully executes
  `<NUMS> OP_CHECKSIG`. Anthony Towns's reply in
  [that thread](https://gnusha.org/pi/bitcoindev/aj9SkwXqdRbuVZxH@erisian.com.au/T/)
  proposes a proof in the coinbase instead, and Sjors Provoost's
  [Sjors/bitcoin#121](https://github.com/Sjors/bitcoin/pull/121) implements
  one in a node.
- **The height and the outputs are rules of this network and will not
  change.** The series is in `patches-m3/` (`README.md`, "Experimental M3
  series").

### What the chain shows

The drill took place on 2026-10-30, as announced for 08:00 UTC: the
operator sent the spend of the drill output at 08:02:52, and block 36897
confirmed it.

| Height | Block | What happened |
|---|---|---|
| 36897 | `000001e1b8fa2d090999db861599132b86f465c47331f9ae64ad162e25478114` | Transaction `2885e18bf4e99bd37b35b106edf2ef9ac499ad26c520a45d6a94cd2708b389c9` spends the drill output; header time 08:03:39 UTC. The block is `evidence/m3_block_36897.hex`. |
| 36900 | `0000008a4bf4e555e39f991540e1d581d100f1c5d0e5f180c44abd88654c5c5b` | A block the operator built to be refused, with an elliptic curve signature in a leaf of version `0xe0`. It is not in the chain. It is `evidence/m3_badblock_36900.hex`. |
| 36901 | `000001f6da9f32e8fdba25bd46aed30f3d488635a44ad5cbc4550d0fc18f835f` | Transaction `38038977cb3d4c0f89d6e6c3a596b4a528b6c2e24b3d7b9cf579dabe33fc38ed` spends an output of a mixed address through its ML-DSA-44 leaf (below); header time 08:10:03 UTC. |

Each of the four nodes the operator runs logged the spend at 08:03:41 UTC,
when block 36897 joined its active chain:

```
P2MR tripwire 8991bb83673474b7c324f303365479c093d0b34f5e7d7ddf0648bcfdb1d42495:0 spent by 2885e18bf4e99bd37b35b106edf2ef9ac499ad26c520a45d6a94cd2708b389c9 in block 000001e1b8fa2d090999db861599132b86f465c47331f9ae64ad162e25478114 (height 36897): elliptic curve checks in leaf version 0xe0 are disabled from height 36898
```

Since then `bitcoin-cli gettripwireinfo` on each of them reports the drill
output `missing`, the NUMS output `unspent` and `ec_disabled_next_block`
`true`.

After the drill block the operator tried both leaves on an output of its
own: vout 3 of
`0e4fe95eb985a7f8d1ccd61f584fce36fa7a73920db03fe4143ad7cf0bd8c5f6`, an
output of a mixed address it funded on 2026-10-22 (see "The mixed address"
above).

- **In the mempool.** `testmempoolaccept` on each of the four nodes refused
  a spend of it through the Schnorr leaf,
  `95eb88e25c25ede01c6d4c0d87c875afe356b673857179df7db1c671d5e53020`, with
  `mempool-script-verify-flag-failed (Elliptic curve signature checks are
  disabled in this leaf)`.
- **In a block.** The operator built block 36900 on block 36899,
  `0000011b2608f136fa7faf18b6f079bbdf2b3c98d8d8ed22770aeb6f2784bb27`, the
  way the block producer builds every block, with proof of work, the
  signet solution and the ML-DSA-44 block signature, and added that spend
  to it. The operator submitted it to node A first. The other three
  received it over the network, as a compact block, and checked it when it
  arrived, before the operator submitted it to them as well. Each logged,
  at 08:07:34 or 08:07:35 UTC:

  ```
  [error] ConnectTip: ConnectBlock 0000008a4bf4e555e39f991540e1d581d100f1c5d0e5f180c44abd88654c5c5b failed, block-script-verify-flag-failed (Elliptic curve signature checks are disabled in this leaf), input 0 of 95eb88e25c25ede01c6d4c0d87c875afe356b673857179df7db1c671d5e53020 (wtxid fa9afe21c983edef8110f9cca67b1b3fc408a139292848f4f2612bf5cd8a997d), spending 0e4fe95eb985a7f8d1ccd61f584fce36fa7a73920db03fe4143ad7cf0bd8c5f6:3
  ```

  Each holds the block only as an `invalid` chain tip, and each kept its
  chain. Block 36900 in the chain is an ordinary block from the producer,
  `000000e5ce261022cdd714095e50981a38ea5fbfd1df02ba59a9552346aea975`.
- **Through the ML-DSA-44 leaf.** Each of the four allowed a spend of the
  same output through the mixed address's ML-DSA-44 leaf, and block 36901
  holds it.

The refused block is there for the record, not to be replayed: the chain
has moved past height 36900, and a node given the block now does not run
its scripts, as the block is not on its best chain.

**The witness transaction.** On 2026-10-22 the operator signed a spend of
vout 2 of the same transaction through the Schnorr leaf, and
`testmempoolaccept` on each of the four nodes allowed it then; it was never
broadcast. Since the drill each refuses it, with the reason above. It is
`evidence/m3_witness_tx.hex`, transaction
`9f954b1a61011562dab57bbec62acdcb107046e7089609b7ee2ed211e704bf33`, and the
output it spends stays unspent until at least 2026-11-30, so a node with
the rule can judge it for itself:

```bash
bitcoin-cli testmempoolaccept "[\"$(cat evidence/m3_witness_tx.hex)\"]"
```

The answer has `"allowed": false` and the reason above.

**How many such leaves were spent.** Until an output is spent through one of
its leaves, nothing on the chain tells a mixed address from any other P2MR
output, so only spends can be counted. From height 30000 to the drill
block, 367 outputs were spent through a leaf of version `0xe0`, each by a
transaction of its own: 366 through a leaf `<x-only key> OP_CHECKSIG` and 1
through an ML-DSA-44 leaf. Two of them, at heights 30061 and 30063, were
the operator's demonstration of the two leaves. From block 36898 to 36928,
two outputs were spent through a leaf of version `0xe0`, both through an
ML-DSA-44 leaf, one of them the operator's (in 36901). The 367 spends came
from 361 distinct scriptPubKeys, that is, 361 addresses. When block 36897
was made, these addresses held 6 unspent outputs; at height 36928, 4 of
those 6 remain unspent, the output the witness transaction spends included.
Outputs at mixed addresses that were never spent from are not counted.

**Witnesses.** Reports from people who watched the drill with nodes of
their own are not counted yet. At 08:56 UTC on 2026-10-30, after the drill,
the public node had three peers: the operator's other three nodes,
connected since 2026-10-21 and 2026-10-27. The node does not log inbound
connections, so a node that connected and left before then does not show.

## Rule change at height 32000 (M4-S)

From height 32000 Ark-0 has one more experimental rule, called milestone
M4-S here: a second post-quantum signature check in P2MR leaves,
`OP_CHECKSLHDSA128S`, which verifies an SLH-DSA-SHA2-128s signature
([FIPS 205](https://doi.org/10.6028/NIST.FIPS.205)). It sits next to M1's
`OP_CHECKMLDSA44`, and the two rest on different assumptions: ML-DSA on the
hardness of lattice problems, SLH-DSA on its hash function alone. The nodes
built with `patches-m4/` enforce it, the operator's among them, and
`doc/p2mr-slhdsa128s.md` in the patched tree specifies it. The chain reached
height 32000 on 2026-10-24 at 21:10 UTC (`contrib/k3s/ARK0-MIGRATION.md`,
"Height 32000: the M4-S rule applies (2026-10-24)").

In a P2MR leaf that is executed (leaf version `0xc0`, or `0xe0` from height
30000, at depth one or more), byte `0xf1` becomes `OP_CHECKSLHDSA128S`. It
takes seventeen stack elements: a 32-byte public key, which the one standard
leaf script pushes itself, and a 7,856-byte signature in sixteen elements from
the witness, fifteen of 520 bytes and one of 56 (57 with an explicit hash
type), so that each signature has exactly one encoding. It checks the signature
as pure SLH-DSA over the BIP-341 tapscript signature message computed with
`key_version` 2, with the context string `P2MR/SLHDSA128S/EXP0`. A non-empty
signature that does not verify makes the script fail, so a spend that carries
one is invalid, and so is any block that contains it. Sixteen empty elements
make the opcode push false, as `OP_CHECKSIG` does with an empty signature.

- **Below height 32000, and outside such leaves, nothing changes.** In
  tapscript the byte is `OP_SUCCESS241`, which makes a script succeed
  without running it. Until the rule applies, an output whose leaf uses the
  opcode can be spent without the signature by anyone who knows that leaf
  and its path in the tree. Nodes do not relay such spends, and no coins
  should be paid to these leaves before then.
- **Once the rule applies, one script with the opcode is relayed:** the public
  key followed by `OP_CHECKSLHDSA128S`, 34 bytes, called SLH1. A spend through
  it at depth one has a witness of 7,972 bytes. Other scripts with the opcode
  are not relayed, but a block may still contain them, and some of them pass
  without a valid signature by a committed key: with sixteen empty elements,
  which push false and `OP_NOT` turns into true, or with a signature under a
  key the spender chooses and puts in the witness. What M1 says about the tree
  holds here too: an SLH1 leaf protects the spending path of its own output and
  nothing more, so a wallet should put it next to an `OP_RETURN` leaf of the
  same version, both at depth one.
- **Each check costs 5,000 units of validation weight.** BIP 342 gives
  every input a budget of its witness size plus 50, and a Schnorr check
  takes 50 of it. A single SLH1 spend has a budget of 8,022, so its check
  fits. The value rests on measurements, on three machines, of the slowest an
  SLH-DSA-SHA2-128s verification can be made to run, compared with a Schnorr
  check, with SHA-256 code that does not use the processor's SHA instructions
  (`README.md`, "Experimental M4-S series").
- **What it does not change.** Who produces blocks: the operator, as before.
  M2's block signature, the tripwire rule of leaf version `0xe0` and its
  drill, `OP_CHECKMLDSA44`, P2TR, witness v0 and legacy scripts are not
  affected, and M3's disabled elliptic curve checks do not touch this opcode.
- **What it shows is narrow.** A node with the rule refuses, at the block
  level, any spend through an SLH1 leaf that lacks a valid SLH-DSA-SHA2-128s
  signature under the leaf's key. SLH-DSA does not bind the message
  (FIPS 205, section 11): the holder of a key can make one signature that is
  valid for two messages, so a signature must never be taken as a
  commitment to the message it signs. On a signet with a single block
  signer, the rule says nothing about how miners or a wider network would
  adopt it.
- **Wallets.** Bitcoin Core's wallet does not make or sign SLH1 leaves. Once
  the chain is six blocks past height 32000, the operator's web wallet,
  <https://wallet.ark0.si>, adds a fourth address, the SLH1 address, to its
  other three: a tree of exactly two leaves of version `0xc0` at depth one,
  SLH1 and `OP_RETURN`. It makes the key from its seed (`WEB-WALLET.md`) and
  signs in the browser. Another wallet that makes such addresses should also
  hand them out only from then on.
- **What a node logs and reports.** A node built with `patches-m4/` logs
  this line when it starts:

  ```
  P2MR SLH-DSA-SHA2-128s rules active from height 32000 (leaf versions 0xc0, 0xe0)
  ```

  `bitcoin-cli getdeploymentinfo` lists `p2mr_slhdsa128s` as a buried
  deployment with height 32000, and from that height `P2MR_SLHDSA128S`
  among the script flags.
- **It is a soft fork.** A node without `patches-m4/` treats the byte as
  `OP_SUCCESS241`, accepts every block an M4-S node accepts and keeps
  following the chain (an M3 build only up to the first rescue spend of M4-R,
  below), but does not check these signatures and does not relay
  spends that use the opcode: to such a node they are non-standard. A node that
  accepted blocks at or above height 32000 before it upgraded has not checked
  them; one start with `-reindex-chainstate` (`-reindex` on a pruned node)
  checks them.
- **It is not part of BIP-360**, which defines no post-quantum signature
  check, and it is not a proposal for Bitcoin.
- **The height and the validation weight are rules of this network and
  will not change.** The series is in `patches-m4/` (`README.md`,
  "Experimental M4-S series").

## Rule change at height 63000 (M4-R)

From height 63000 Ark-0 has one more experimental rule, called milestone
M4-R here: a rescue lane in P2MR leaves of version `0xe0`. M3 disables the
elliptic curve signature checks in those leaves from the block after a
tripwire output is spent, so a leaf such as `<P> OP_CHECKSIG` has no valid
witness any more, and an output with no other way to be spent is frozen. The
lane gives such a leaf one: in place of the signature, the spender proves in
zero knowledge that they know the seed the key was derived from. The nodes
built with `patches-m4r/` enforce it, the operator's among them, and
`doc/p2mr-rescue.md` in the patched tree specifies it. The height was
announced on [ROLL DATE], with the release of the build, about four weeks
ahead. At about 890 blocks a day, the rate of recent weeks, the chain reaches
height 63000 around 2026-11-28 14:00 UTC. It cannot do so before 2026-11-26
04:00 UTC, even if blocks came every 90 seconds, the producer's configured
pause.

From height 63000, in an executed P2MR leaf of version `0xe0` at depth one or
more, an `OP_CHECKSIG`, `OP_CHECKSIGVERIFY` or `OP_CHECKSIGADD` with a 32-byte
key whose signature element is a **rescue token** is a rescue check.
The token carries the statement: a context string `K`, a suffix and `y`, where
`y = HMAC-SHA256(K, x || suffix)` for the seed `x`. The input's annex carries
the proof, a VOLE-in-the-head proof of 130,218 bytes. The check passes when
`y` is the secret key of the checked key and the proof verifies. The proof is
bound to the transaction by a signature message that commits to everything a
BIP 341 `SIGHASH_ALL` message commits to except the annex, which holds the
proof, and to this network by the SHA-256 of its signet challenge. As with a
BIP 341 signature, a third party can change what it does not cover, such as
the control block, which changes the wtxid. An input makes at most one rescue
check, so a leaf with two keys that need rescuing, a 2-of-2 for example,
cannot be rescued.

- **It relaxes the rules: a hard fork.** A frozen leaf such as `<P>
  OP_CHECKSIG` has no valid witness under the earlier rules; the lane makes
  some valid. A node built with `patches-m3/` but without `patches-m4r/` (an
  M3 or M4-S build) refuses the first block that contains a rescue spend, marks
  it invalid and stops following the chain there. Once upgraded, it continues
  after `bitcoin-cli reconsiderblock <hash of that block>`. A build without
  `patches-m3/` does not run these leaves; it takes the block and keeps
  following, checking less. Every M3 or M4-S node that is to follow Ark-0 past
  that block needs this build; upgrade before height 63000.
- **Below height 63000, and outside such leaves, nothing changes.** Below the
  height a token is a signature like any other, which can never be valid, so
  a node with this build checks every block below it as an M4-S node does, and
  an M4-S node needs no reindex when it upgrades (an M3 node still needs the
  `-reindex-chainstate` that M4-S asks for, `JOIN.md` section 5). Leaf version
  `0xc0`, P2TR, witness v0 and
  legacy scripts, `OP_CHECKMLDSA44` and `OP_CHECKSLHDSA128S` are not affected.
- **From the height on, a token counts whether or not the checks are
  disabled.**
  Whether or not the tripwire has disabled the elliptic curve checks, a rescue
  check is decided by its proof. The mempool is stricter: it takes a rescue
  spend only when the checks are disabled for the next block.
- **Publishing a rescue spend publishes the key.** `y` is the Schnorr secret
  key of the key the leaf checks. While the elliptic curve checks in the leaf
  work, anyone who sees a rescue spend can spend the same output with an
  ordinary signature. Once they are disabled, the output stays safe only as long as no
  reorganization undoes the tripwire spend.
- **Each rescue check costs 130,000 units of validation weight,** after the
  50 that BIP 342 charges for the signature. BIP 342
  gives every input a budget of its witness size plus 50. The smallest valid
  rescue input has a witness of 130,333 bytes, so it pays for its check, and a
  block holds about 30 of them. The value rests on measurements, on two
  machines, of the slowest a check can be made to run compared with a Schnorr
  check, and of whole blocks of rescue checks against the slowest block of
  Schnorr checks (`README.md`, "Experimental M4-R series").
- **One template is relayed, R1:** a leaf `<32-byte key> OP_CHECKSIG` of
  version `0xe0` at depth one or more, spent with exactly the token, the
  script, the control block and the annex, with at most three rescue inputs
  in a transaction. Over P2P a node takes rescue spends only from peers with
  `noban` permission; `sendrawtransaction` is not limited by this. So a rescue
  spend relayed from a node of one's own reaches the operator's nodes only if
  they give that node `noban`. Other uses of the lane are valid in a block but
  not relayed.
- **What it does not change.** Who produces blocks: the operator, as before.
  M2's block signature and the tripwire rule itself are unchanged: an
  ordinary elliptic curve signature in a leaf of version `0xe0` still fails
  once the checks are disabled.
- **What it rests on is narrow.** The proof system is that of
  [ark0-rescue](https://github.com/duncan0k/ark0-rescue) `SPEC.md` v0.2,
  statement `0x01`. Its security rests on an argument in the classical random
  oracle model with a 128-bit target. Its security against quantum attackers
  is only conjectured; against the known quantum attacks its strength is NIST
  category 1 (about `2^64` Grover iterations). It has had no external review;
  two reviews by AI models of the final version found no forgery, no
  malleability of the proof encoding and no way around the rules. A seed with
  less than 128 bits of
  entropy makes the lane weaker than the proof system. The operator accepts
  this for a test network; it is no basis for using the lane anywhere else.
- **Wallets.** Bitcoin Core's wallet does not make rescue spends. The proofs
  are made off the node, with the prover of ark0-rescue, and only with seeds
  used on this signet alone. A wallet has to rescue all frozen outputs of a key
  in one transaction (more than three need one that the block producer
  includes directly), first move every output the same key can spend another
  way, never reuse the key, its secret key or the seed on another network or
  protocol, and pay nothing more to the key (`doc/p2mr-rescue.md`, "Wallets
  and operators").
- **The rescue drill.** The operator holds two outputs of 100,000 sats, each
  with two leaves of version `0xe0`, `<R> OP_CHECKSIG` and `OP_RETURN`,
  created for it at height 30068
  (`12875980e1cd709e3bf930fce984089b580790683eed05d3d03112447f0f3753:0` and
  `:1`). After height 63000 it will rescue both in one transaction.
- **What a node logs and reports.** A node built with `patches-m4r/` logs
  this line when it starts:

  ```
  Signet P2MR rescue lane from height 63000 for elliptic curve checks disabled by the tripwire (VOLEitH v0.2, statement 0x01); network id afd1fcbb77d445f14e06eb1a7dff52807c71d7765c311e6181e698734337b38e
  ```

  `bitcoin-cli getdeploymentinfo` lists `p2mr_rescue` as a buried deployment
  with height 63000, and `gettripwireinfo` has a `rescue` object. When a block
  joins the active chain, each rescue spend in it through template R1 is
  logged:

  ```
  P2MR rescue of <txid:vout> by <txid> in block <hash> (height <N>): statement 01, key <P>
  ```
- **It is not part of BIP-360 or BIP-361**, and it is not a proposal for
  Bitcoin.
- **The height and the validation weight are rules of this network and will
  not change.** If a way to forge proofs is found, or a verifier bug that
  accepts an invalid one, the operator can close the lane again from a later
  height with the soft fork in `patches-m4r-killswitch/`, prepared and tested
  for that case; rescues already in the chain stay valid. The series is in
  `patches-m4r/` (`README.md`, "Experimental M4-R series").

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
| `OP_CHECKMLDSA44` (M1) | from height 8600, see "Rule change at height 8600 (M1)" |
| ML-DSA-44 block signature (M2) | from height 20000, by the key whose SHA256 is `eee8548f51f25492de3ae430b7ce7ec549edfaa663b8f577f026ce0498fdd6ad`, see "Rule change at height 20000 (M2)" |
| Leaf version `0xe0` (M3) | executed from height 30000, with the tripwire outputs `4319a38c0842846474ff038b54f22aec844147a97c2abc462bfb92957c1bc3a0:1` and `8991bb83673474b7c324f303365479c093d0b34f5e7d7ddf0648bcfdb1d42495:0`, see "Rule change at height 30000 (M3)" |
| `OP_CHECKSLHDSA128S` (M4-S) | from height 32000, 5,000 units of validation weight per check, see "Rule change at height 32000 (M4-S)" |
| Rescue lane of leaf version `0xe0` (M4-R) | from height 63000, 130,000 units of validation weight per rescue check, a hard fork, see "Rule change at height 63000 (M4-R)" |

The cadence and the two target spacings are separate things, and it is worth
not reading one for another. The cadence is an operational choice: the
interval this chain's block producer was told to wait between blocks. The
consensus target spacing is `nPowTargetSpacing`, which every signet inherits
as ten minutes; no patch in this repository touches it, and Core still uses
it for timeouts and estimates. The retarget target spacing is what the
difficulty adjustment measures each period against: the same 600 s on every
signet, and on this one 90 s from the retarget at 8064 on. That change, made
by the patch in `patches-spacing/`, is one of six consensus changes on this
network besides P2MR; the others are M1, M2, M3, M4-S and M4-R (the five
"Rule change" sections above). Producing blocks faster than ten minutes is
what a signet is for: the signet solution decides who may produce a block,
while proof of work still paces how fast, as the next paragraphs record.

What the difference does over time was not written down here until it had
been observed. Signet keeps Bitcoin's difficulty adjustment: every 2016
blocks the target is multiplied by the time the last period actually took
over the time it was expected to take (2016 × 600 s), with that ratio
clamped to between a quarter and four. While blocks come less than 150 s
apart on average, as in the first two periods, a period takes less than a
quarter of the expected time, so the difficulty rises by the full factor of
four at each retarget; it keeps rising, by less
each time, until the proof of work on top of the producer's 90 s pause
brings the average spacing up to 600 s. Observed on this chain (node A; the
first two rows on 2026-09-22, the others after the retarget at 8064):

| Heights | `nBits` | Difficulty | Average spacing |
|---|---|---|---|
| 0–4031 | `1e0377ae` | 0.0011 | 94.9 s (over 2016–4031) |
| 4032–6047 | `1e00ddeb` | 0.0045 | 116.3 s |
| 6048–8063 | `1d377ac0` | 0.0180 | 171.8 s |
| 8064– | `1d69df08` | 0.0094 | 127.2 s (over 8064–8600) |

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

That is what the retarget at 8064 did. Blocks 6048 to 8063 took 346,241 s,
1.91 times 181,440 s, so the target of block 8064,
`000000471b85a422db9f1d1be893acb4ca5f3fc594beaa01b739c643effcfee5`, is that
many times the target before it: `nBits` `1d69df08`, difficulty 0.0094, the
last row above. Every node the operator runs holds that block.

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
| `JOIN.md` | how to get a node, prebuilt or built, connect it to the public node and follow the live chain |
| `WEB-WALLET.md` | how the operator's web wallet makes its keys and addresses from a seed, and the limits of that derivation |
| `web-wallet-vectors.json` | test vectors for the other keys and addresses in `WEB-WALLET.md`, and for the login |
| `web-wallet-slh-vectors.json` | test vectors for the SLH-DSA-SHA2-128s key and the SLH1 address in `WEB-WALLET.md` |
| `release-signing-key.asc` | the public key that signs the `SHA256SUMS` of the prebuilt node's releases |
| `snapshot/ark0-blocks-1264.dat` | the chain, heights 0 to 1264 |
| `evidence/EVIDENCE.md` | what each evidence file is and what the node said about it |
| `evidence/*.hex` | the confirmed spend, three malformed spends, three rejected blocks, the block refused under M1, block 20000, the five blocks refused under M2, and M3's drill block, the block refused after it and the witness transaction |
| `evidence/p2mr_tree.json` | the demo script tree, public data only |
| `SHA256SUMS` | checksums for everything above |

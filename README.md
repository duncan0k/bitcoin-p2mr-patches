# bitcoin-p2mr-patches

Patch series implementing the BIP-360 Pay-to-Merkle-Root (P2MR) spending rules on **Bitcoin Core v31.1**
(milestone M0: no post-quantum signatures). Purpose: give a custom signet P2MR rules that are enforced in
**block validation**, reproducibly.

- Base: `bitcoin/bitcoin` tag `v31.1` (commit `9be056a`)
- Specification: BIP-360 v0.12.1 at `bitcoin/bips` commit `620871a7a442e276a058b487cd8743775fb499a4`
- License: same as Bitcoin Core (MIT)
- Contact: duncan0k <dev@ark0.si>
- Status: experimental. **Not deployed on, and not intended for, mainnet or the public signet** (the
  deployments never activate there).
- Experimental extensions for the Ark-0 test signet. None of them is proposed for Bitcoin.
  - The M1 series in `patches-m1/` adds an ML-DSA-44 signature check in P2MR leaves,
    `OP_CHECKMLDSA44`; BIP-360 defines no post-quantum signature check. It is active on Ark-0 from
    height 8600 and on regtest by default, and never on the default signet, a testnet or mainnet.
    Another custom signet gets it only if it sets `-signetmldsa44`, an option `patches-m3/` adds.
    See "Experimental M1 series" below.
  - The M2 series in `patches-m2/` requires an ML-DSA-44 signature by a fixed key on every block
    from a height on, next to the signet solution: on Ark-0 from height 20000, and on another custom
    signet only if it sets `-signetpqblock`. See "Experimental M2 series" below.
  - The M3 series in `patches-m3/` adds a P2MR leaf version, `0xe0`, whose elliptic curve signature
    checks stop working once one of a few designated outputs is spent. On Ark-0 this applies from
    height 30000, and the operator holds the key of one of its two such outputs and will spend it in
    an announced drill. Another custom signet gets it only if it sets `-signettripwire`. See
    "Experimental M3 series" below.
  - The M4-S series in `patches-m4/`, not released, adds an SLH-DSA-SHA2-128s signature check in
    P2MR leaves, `OP_CHECKSLHDSA128S`. It is not active on Ark-0 (its height there is not set yet),
    and its validation weight is provisional. Another custom signet gets it only if it sets
    `-signetslhdsa128s`. See "Experimental M4-S series" below.

## Layout

```
patches/            10 git format-patch files, apply in order with git am
patches-m05/        24 further patches, the M0.5 wallet series (see below)
patches-spacing/    1 patch, the retarget spacing Ark-0 uses from height 8064 (see below)
patches-m1/         13 further patches, the experimental M1 series, on Ark-0 from height 8600 (see below)
patches-m2/         6 further patches, the experimental M2 series and a later test of it, on Ark-0 from height 20000 (see below)
patches-m2a/        the first four M2 patches alone: candidate A, without Ark-0's height and key
patches-m2b/        4 patches on top of patches-m2a/: candidate B, not used on Ark-0 (see below)
patches-m3/         11 further patches, the experimental M3 series, on Ark-0 from height 30000 (see below)
patches-m4/         12 further patches, the experimental M4-S series, not released and not active on Ark-0 (see below)
SHA256SUMS          checksums of the patches
SHA256SUMS-m05      checksums of the M0.5 patches
SHA256SUMS-spacing  checksum of the retarget spacing patch
SHA256SUMS-m1       checksums of the M1 patches
SHA256SUMS-m2       checksums of the M2 patches; SHA256SUMS-m2a and SHA256SUMS-m2b for the candidates
SHA256SUMS-m3       checksums of the M3 patches
SHA256SUMS-m4       checksums of the M4-S patches
apply.sh            clone v31.1, verify, apply the consensus series, build, run the core tests
ark0/               evidence pack and joining guide for the experimental custom signet (see below)
contrib/k3s/        build, test and run the series inside a k3s cluster
contrib/cross/      cross-compile the M3 node for macOS and Linux, as the witness program ships it
contrib/windows/    cross-compile the M3 node for 64-bit Windows, as the witness program ships it
contrib/m4-measure/ the measurement-only build of the M4-S validation weight; never part of a node (see below)
witness/            Ark0Witness, a small program for watching the M3 drill with a node of one's own
```

## Usage

```bash
bash apply.sh            # clone, patch, build and test in ./bitcoin
# or by hand:
git clone --branch v31.1 --depth 1 https://github.com/bitcoin/bitcoin.git && cd bitcoin
git am ../bitcoin-p2mr-patches/patches/*.patch
cmake -B build -DBUILD_TESTS=ON -DENABLE_WALLET=ON -DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF
cmake --build build -j"$(nproc)"
./build/bin/test_bitcoin
build/test/functional/test_runner.py -j8 feature_p2mr feature_p2mr_signet p2p_segwit feature_taproot
```

## What the series does

| # | Commit | Content |
|---|---|---|
| 1 | script: add P2MR (BIP 360) witness v2 program validation | `SCRIPT_VERIFY_P2MR`; the v2/32-byte branch of `VerifyWitnessProgram` (stack and annex rules, control block `1 + 32m` with `m <= 128`, low bit of `c[0]` checked for every leaf version, TapLeaf/TapBranch folding without a tweak, root match, `m = 0` succeeds immediately, non-`0xc0` leaves succeed, `0xc0` leaves run tapscript); `PrecomputedTransactionData::Init` recognises `OP_2` inputs; buried deployment `DEPLOYMENT_P2MR` with per-chain heights; `GetBlockScriptFlags` wiring |
| 2 | policy: add P2MR (BIP 360) output type, address and relay rules | `TxoutType::WITNESS_V2_P2MR`, `WitnessV2P2MR` destination and bech32m v2 addresses, standardness rules, RPC output, `doc/p2mr.md` |
| 3 | test: add BIP-360 P2MR script_tests vectors and their generator | `test_framework/p2mr.py`, `test/util/generate_p2mr_script_tests.py`, P2MR rows in `script_tests.json` with real Schnorr signatures, the upstream construction vectors vendored |
| 4 | test: add feature_p2mr.py covering BIP-360 activation and consensus | before/after activation, rejection by both the mempool and `submitblock`, non-`0xc0` leaves and annexes, mixed P2TR + P2MR inputs, reorg across the activation height |
| 5–7 | test: integration fixes | error names aligned with the implementation (`P2MR_WRONG_CONTROL_PARITY`, `WITNESS_PROGRAM_WITNESS_EMPTY`), confirmation via block hash, the v31 `block-script-verify-flag-failed` label, `m = 128` control blocks are standard |
| 8 | p2mr: policy checks control block shape; signet activation test; m=0 future-leaf vector | follow-ups from review |
| 9 | policy: SCRIPT_VERIFY_P2MR is a consensus (mandatory) flag | v31.1 labels a script failure by `flags & STANDARD_NOT_MANDATORY_VERIFY_FLAGS`; a consensus flag has to be in `MANDATORY_SCRIPT_VERIFY_FLAGS` or block-level failures are misreported as non-standard |
| 10 | test: p2p_segwit uses 33-byte v2 programs as future versions | 32-byte v2 programs are P2MR now; same treatment taproot gave to v1 |

## Activation

| Chain | `P2MRHeight` |
|---|---|
| mainnet / testnet3 / testnet4 / default signet | `INT_MAX` (never; `getdeploymentinfo` does not list `p2mr`) |
| custom signet (own `-signetchallenge`) | `1` |
| regtest | `0`, overridable with `-testactivationheight=p2mr@N` |

`SCRIPT_VERIFY_P2MR` is in both `MANDATORY_SCRIPT_VERIFY_FLAGS` and `STANDARD_SCRIPT_VERIFY_FLAGS`; the
mempool applies the P2MR rules as policy on every chain (as taproot was handled around 0.21.0), block
validation only after activation.

## Verification record (2026-09-15, Ubuntu 24.04 / g++ 13.3 / 64 cores)

- Full `test_bitcoin` passes; `script_tests.json` carries 33 P2MR rows (m = 0/1/2/128 succeed, m = 129 and
  wrong lengths fail, parity bit, non-`0xc0` with and without the discourage flag, stack shapes and annex,
  root mismatch, `OP_SUCCESSx`, three sighash types, `OP_CODESEPARATOR`, wrong signature).
- Default functional test set **270/270**, extended set **273/273** (including `feature_p2mr.py` and
  `feature_p2mr_signet.py`).
- Independent review (Codex, gpt-6-astra, high effort): ship for an experimental custom signet, no high
  findings; left for later: descriptor inference, wallet output type and change selection, a dedicated
  P2MR fuzz target, resource-accounting stress tests.
- A custom signet built from this series has enforced the rules on a real chain: a P2MR script-path spend
  confirmed, and blocks carrying malformed P2MR spends (valid proof of work and signet solution) were
  rejected by both nodes with `block-script-verify-flag-failed`.

## Out of scope / wording

- No activation on mainnet or the public signet: this holds for everything in this repository. No
  post-quantum signatures in `patches/`, `patches-m05/` or `patches-spacing/`; the post-quantum
  signature checks are the experimental M1 and M2 series in `patches-m1/` and `patches-m2/`, which
  Ark-0 activates from heights 8600 and 20000 (`ark0/NETWORK.md`); another custom signet can switch
  M2 on with `-signetpqblock`, and M1 with `-signetmldsa44`, which `patches-m3/` adds.
- The **M0 consensus series** in `patches/` is spending rules only: no `tmr()` descriptor, no wallet
  signing, no address generation. Those are the M0.5 series in `patches-m05/`, described below, which
  is a separate series applying on top.
- Describe it as: "an experimental signet implementing the BIP-360 v0.12.1 P2MR spending rules on
  Bitcoin Core v31.1, enforced at block validation, independently reproducible". It is not "the first",
  "the only", "the real bc1z", "mainnet-ready", or "a quantum-resistant network".
- Describe M1 as: "an experimental ML-DSA-44 signature check in P2MR leaves, active on the Ark-0 test
  signet only" (regtest activates it too, for tests, and with `patches-m3/` another custom signet can
  switch it on with `-signetmldsa44`). It is not part of BIP-360, it is not a proposal
  for Bitcoin, and it does not make Ark-0 a quantum-resistant network: it checks ML-DSA-44 signatures
  only in the P2MR leaves that use the opcode, and it restricts the spending of an entire output to
  committed ML-DSA-44 keys only if every spendable leaf requires a valid signature under a key fixed by
  that leaf, as S1 and S2 do.
- Describe M2 as: "an experimental ML-DSA-44 block signature, required on the Ark-0 test signet from
  height 20000". It is not part of BIP 325, it is not a proposal for Bitcoin, and it does not make
  Ark-0 a quantum-resistant network either: blocks still need the classical signet solution and proof
  of work, one party holds both block keys, and M2 changes no rule for spending an output.
- Describe M3 as: "a drill of disabling elliptic curve checks inside one experimental P2MR leaf
  version, on the Ark-0 test signet: from the block after the drill tripwire is spent, elliptic curve
  signature checks in leaves of version `0xe0` stop working". It follows the P2XX-T idea of Pieter
  Wuille's mail of 2026-06-25, but what it constrains is one experimental leaf version, and its
  tripwires are two designated outputs. It is not part of BIP-360 or BIP-361, it is not a proposal
  for Bitcoin, and it does not mean that any elliptic curve key has been broken, or tell when one
  will be. It disables no elliptic curve check outside leaves of version `0xe0`: leaves of version
  `0xc0`, P2TR and the other output types are not affected.
- Show only `tb1z` (signet) / `bcrt1z` (regtest) addresses. Do not generate single-leaf (m = 0) outputs
  from tooling: consensus accepts them per the BIP, and they are anyone-can-spend.

## Ark-0 evidence pack (`ark0/`)

`ark0/` holds what an outside reader needs to check the custom-signet claims offline: the network
parameters (`NETWORK.md`); the confirmed P2MR spend and the three rejected blocks as raw hex, with the
node responses they produced (`evidence/`); a block file covering genesis to height 1264 that a fresh
patched node replays with `-loadblock` (`snapshot/`); and the step-by-step `REPRODUCE.md`. Since
2026-09-24 the network also has one public node: `ark0/JOIN.md` says how to get a node, connect it
and follow the live chain. Every block is still produced by the operator alone. What the chain showed
at the rule change at height 8600 (M1), with the block the nodes refused after it, at 8714, is in `NETWORK.md`
and `evidence/`; checking it takes a node that follows the live chain.

`ark0/WEB-WALLET.md` says how the operator's web wallet, <https://wallet.ark0.si>, makes its keys and
addresses from a seed, with test vectors in `ark0/web-wallet-vectors.json`, and lists the limits of that
derivation.

## Follow-up series: M0.5 wallet support (`patches-m05/`)

A second series, shipped in this tree alongside the consensus one and applied on top of it. It is not
part of M0 and does not change a consensus rule: everything it adds is wallet, descriptor and PSBT
code, and the `tmr()` descriptor it introduces is explicitly provisional. Apply `patches/` alone for
the consensus rules; apply both for a wallet that can hold and spend P2MR outputs.

`patches-m05/` (checksums in `SHA256SUMS-m05`) adds:

- a **provisional** `tmr(TREE)` descriptor (same tree grammar as `tr()`, no internal key, single-leaf
  trees rejected because they are anyone-can-spend, depth limited to 128);
- P2MR spend data in the signing provider and script-path signing in the wallet (`send`, PSBT);
- a deployment gate: importing or deriving `tmr()` receiving addresses is refused on chains where the
  P2MR deployment is disabled (mainnet, testnets, the default signet), since such outputs would be
  anyone-can-spend there;
- fee estimation that treats P2MR inputs as witness inputs, control-block validation before signing,
  a dedicated fuzz target, `wallet_p2mr.py` and `wallet_p2mr_signet.py`;
- functional coverage of cross-wallet 2-of-2 multisig leaves signed through PSBT (`wallet_p2mr_multisig.py`),
  CLTV/CSV timelocked leaves (`wallet_p2mr_timelock.py`), the edits that invalidate a signed P2MR
  spend (amount, input order, control block, `witness_utxo`, another input's scriptPubKey), which
  leaf a control block names however the input says what it spends, and that merging leaves a
  `tr()` input's control blocks alone.

Review status: five rounds of independent review; the second round found the earlier high and medium
findings fixed. The third found that the control blocks a wallet recovers while signing were dropped
on export and when combining, which left a separate finalizer unable to complete the input. The
fourth found the repair for it too broad in two ways: a control block names one leaf and not several,
and the wider merging behaviour had to stop at P2MR inputs rather than reach `tr()` ones. The fifth
found the remaining half of the first: an input that says what it spends with the whole previous
transaction rather than the output alone was still exporting without it. Six patches cover those
three rounds (24 patches in total).

It has been exercised on the experimental signet, which is why it is in this tree rather than waiting
outside it. From 2026-09-16 to 2026-09-25 the verifying node of the Ark-0 signet ran a build of this
series while the block producing node ran the consensus series without it, so the two were continuously
checked against each other on a live chain; a divergence would have been the finding. From 2026-09-22
both also carried `patches-spacing/`, described below; from 2026-09-25 both ran one build of all four
sets, `patches-m1/` included, from 2026-09-30 one of all five, `patches-m2/` included, and since 2026-10-12
they run one of all six, `patches-m3/` included. A job every six hours funds a P2MR
address from one node and spends it back from the other through the PSBT flow, asserting the witness
dimensions each time. `contrib/k3s/ARK0-MIGRATION.md` records the 2026-09-25 roll, the build it came
from, and the first round trip it produced.

```bash
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch
```

`patches-m05/` is regenerated from the `p2mr-m05` branch of the Core tree with the range that
*includes* its first commit, the one right after the last consensus commit `be23b12`:

```bash
git format-patch --start-number 11 be23b12..p2mr-m05 -o patches-m05
(cd patches-m05 && sha256sum -b *.patch) > SHA256SUMS-m05
```

## Ark-0 retarget spacing (`patches-spacing/`)

One patch, applied on top of `patches/`, with or without `patches-m05/`. It is not part of P2MR. It
adds `-signetpowtargetspacing=<seconds>[@<height>]`, which makes a custom signet's difficulty
retargets at or above `<height>` measure each 2016-block period against `<seconds>` per block instead
of 600. Ark-0's producer pauses 90 s between blocks, and signet's own retarget had been raising the
difficulty since height 4032, which `ark0/NETWORK.md` records with the numbers. Every Ark-0 node the
operator runs, the public one included, carries the patch with `-signetpowtargetspacing=90@8064`, so
the retargets from height 8064 on aim at 90 s per block. It is a consensus rule of that network: a node without the patch and the option follows
Ark-0 up to height 8063 and rejects the block at 8064.

The patch also makes every signet node check at startup that each retarget header in its block index
has the difficulty its current rule gives it, and refuse to start otherwise, so a node cannot keep
headers it accepted before the option was added, removed or changed. `doc/signet-target-spacing.md`,
added by the patch, describes the option and what a running network has to do to adopt it.

```bash
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-spacing/*.patch
# or on top of both series
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch \
       ../bitcoin-p2mr-patches/patches-spacing/*.patch
```

Verified on 2026-09-22 in the k3s cluster (`contrib/k3s`), in the two builds the Ark-0 nodes ran until
2026-09-25, which `contrib/k3s/ARK0-MIGRATION.md` records: with `patches/`, `test_bitcoin` passed 736 of 742 test
cases with 5 skipped, and `feature_p2mr`, `feature_p2mr_signet`, `feature_signet`, `tool_signet_miner`
and `p2p_segwit` passed; with `patches/` and `patches-m05/`, `test_bitcoin` passed 739 of 745 with 5
skipped, and the default functional suite passed, 273 tests with 17 skipped. The patch is exported
from a branch of the Core tree based on the last consensus commit `be23b12`:

```bash
git format-patch --start-number 35 -1 <commit> -o patches-spacing
(cd patches-spacing && sha256sum -b *.patch) > SHA256SUMS-spacing
```

## Experimental M1 series (`patches-m1/`)

Thirteen patches, applied on top of `patches/`, `patches-m05/` and `patches-spacing/`, in that order.
They add `OP_CHECKMLDSA44`, an ML-DSA-44 (FIPS 204) signature check in P2MR leaves that are executed,
as an experiment of the Ark-0 test signet. It is not part of BIP-360, which defines no post-quantum
signature check, and it is not proposed for Bitcoin. `doc/p2mr-mldsa44.md`, added by the series, is
its specification. `ark0/NETWORK.md`, "Rule change at height 8600 (M1)", says what it protects on
Ark-0 and what it does not: in short, a spend through one of two leaf templates, S1 and S2, needs a
valid ML-DSA-44 signature, and M1 restricts the spending of an entire output to committed ML-DSA-44
keys only if every spendable leaf requires a valid signature under a key fixed by that leaf, as S1 and
S2 do.

| # | Commit | Content |
|---|---|---|
| 36 | crypto: vendor mldsa-native v2.0.0 for ML-DSA-44 signature verification | [mldsa-native](https://github.com/pq-code-package/mldsa-native) v2.0.0 (Apache-2.0 OR ISC OR MIT) in `src/crypto/mldsa44/`, its portable C code only, compiled for verification only |
| 37 | test: check ML-DSA-44 verification against the ACVP and Wycheproof vectors | the NIST ACVP sigVer cases and all 180 Wycheproof cases for pure ML-DSA-44, and one signature made by another implementation |
| 38 | bench: time ML-DSA-44 verification next to BIP-340 verification | |
| 39 | script: add OP_CHECKMLDSA44 for ML-DSA-44 checks in P2MR leaves | byte `0xf0` in an executed leaf (version `0xc0`, depth one or more); a 1312-byte key in three pushes from the leaf, a 2420-byte signature (2421 with a hash type) in five witness elements; the BIP-341 tapscript signature message with `key_version` 1, context string `P2MR/MLDSA44/EXP0`; 250 units of validation weight per check; a signature cache domain of its own |
| 40 | consensus: activate OP_CHECKMLDSA44 at a height fixed for the Ark-0 signet | buried deployment `p2mr_mldsa44`: height 8600 under Ark-0's challenge, never on any other signet, testnet or mainnet; regtest from genesis or `-testactivationheight=p2mr_mldsa44@N` |
| 41 | policy: relay OP_CHECKMLDSA44 spends only once active, and in two templates | nothing that executes the opcode is relayed while the next block is below the height; after that only the templates S1 and S2, byte for byte; a reorganization back below the height removes such spends from the mempool |
| 42–45 | test: functional framework, script tests and signature message vectors, `feature_p2mr_mldsa44.py`, a fuzz target | 39 rows in `script_tests.json` with real ML-DSA-44 signatures; activation, relay, reorganization and block-level rejection on regtest |
| 46 | bench: time the whole path of P2MR ML-DSA-44 and Schnorr checks | |
| 47 | doc: describe OP_CHECKMLDSA44 | `doc/p2mr-mldsa44.md` |
| 48 | test: add OP_CHECKMLDSA44 script test rows signed by another implementation | 16 rows signed by p2mr-ts, a separate TypeScript implementation |

It is a soft fork: a node built without `patches-m1/` treats the byte as `OP_SUCCESS240`, accepts every
block a node with it accepts, and keeps following Ark-0, but does not check these signatures and does
not relay spends that use the opcode. Bitcoin Core's wallet does not create or sign these leaves; the
wallet side, which builds S1 and S2 outputs and signs spends of them, is in
[p2mr-ts](https://github.com/duncan0k/p2mr-ts), whose README describes it.

Verified in the k3s cluster (`contrib/k3s`) on the build the Ark-0 nodes ran from 2026-09-25 to
2026-09-30, from a fresh clone, which `contrib/k3s/ARK0-MIGRATION.md` records: `test_bitcoin` passed
744 of 750 test cases with 5 skipped; 13 P2MR and neighbouring functional scripts passed,
`feature_p2mr_mldsa44.py` among them; and the default functional suite passed, 274 tests with 17
skipped. Two more builds from
fresh clones with an empty compiler cache, the second from the files in `patches-m1/` as published
here, produced the same five binaries, byte for byte.

Review status: the design was reviewed in five rounds before it was frozen, the series in five more,
and the agreement between this series and p2mr-ts in three. Every round was an independent review by
Codex (gpt-6-sol at maximum effort), and gpt-6-astra also checked the consensus commits. The
validation weight of 250 per check, and the measurements behind it, are in `doc/p2mr-mldsa44.md`; the
measurements come from one machine.

```bash
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch \
       ../bitcoin-p2mr-patches/patches-spacing/*.patch ../bitcoin-p2mr-patches/patches-m1/*.patch
```

The series is exported from the `p2mr-m1` branch of the Core tree, which carries the consensus series,
the M0.5 series and the spacing patch below the thirteen M1 commits; `51be7ca` is the spacing commit
on that branch:

```bash
git format-patch --start-number 36 51be7ca..p2mr-m1 -o patches-m1
(cd patches-m1 && sha256sum -b *.patch) > SHA256SUMS-m1
```

## Experimental M2 series (`patches-m2/`)

Six patches, applied on top of `patches/`, `patches-m05/`, `patches-spacing/` and `patches-m1/`, in
that order: five that make the rule, and a test added later. From a height on, they require every block of a signet to carry an ML-DSA-44 (FIPS 204)
signature by a fixed public key, in the coinbase output that holds the witness commitment, next to the
signet solution: on a custom signet as `-signetpqblock=<pubkey>@<height>` gives them, and on Ark-0
from height 20000 by a key fixed in the code, where the option is refused. It is an experiment of the
Ark-0 test signet, a test network for trying things out that may be reset at any time. It is not part
of BIP 325, which defines the signet solution and no other
signature, and it is not proposed for Bitcoin. `doc/signet-pqblock.md`, added by the series, is its
specification. `ark0/NETWORK.md`, "Rule change at height 20000 (M2)", says what it changes on Ark-0
and what it does not.

| # | Commit | Content |
|---|---|---|
| 49 | signet: require an ML-DSA-44 block signature from a height on (-signetpqblock) | the option; from the height, the witness commitment output must be exactly `OP_RETURN`, the commitment, the PQ push (`PQB1`, version 1, the 2420-byte signature) and at most one signet solution push; the signature covers `TaggedHash("SignetPQBlock", SHA256(challenge) ‖ nVersion ‖ hashPrevBlock ‖ merkle root ‖ nTime)` with context string `SIGNET/PQBLOCK/EXP0`; checked in `CheckBlock` on the same condition as the signet solution, so `ConnectBlock` checks it again and templates are left alone; the height from the BIP34 push; below the height, a verdict per non-genesis block in the `validation` log |
| 50 | test: add signet_pqblock_tests and its vectors | `test_framework/signet_pq.py` and a generator for fixed vectors; every form of the output the rule rejects, every length of the BIP34 height push, the option; a block signed by p2mr-ts, a separate TypeScript implementation that computes the message on its own |
| 51 | test: add feature_signet_pqblock.py | activation, malformed blocks, a node without the rule following the chain, `-reindex-chainstate` and a late upgrade; and blocks made the way the Ark-0 producer makes them, under a 1-of-1 challenge like Ark-0's |
| 52 | doc: describe -signetpqblock | `doc/signet-pqblock.md` |
| 53 | signet: require the ML-DSA-44 block signature on the Ark-0 signet from height 20000 | `ARK0_PQ_BLOCK_HEIGHT` and `ARK0_PQ_BLOCK_PUBKEY`, whose SHA256 is `eee8548f51f25492de3ae430b7ce7ec549edfaa663b8f577f026ce0498fdd6ad` |
| 54 | test: add feature_signet_pqblock_edges.py | three edge cases on an `OP_TRUE` signet: a pruned node that accepted a block without the signature before it got the rule (it fails the block check at startup; `-reindex` recovers it, `-reindex-chainstate` does not), a signed block of exactly the maximum weight and one weight unit over it, and copies of a signed block that change only a witness |

54 was added after the release of 2026-09-30. It changes only the functional tests, so it changes no
binary; the M2 build released that day was made before it.

It is a soft fork: a node built without `patches-m2/` accepts every block a node with it accepts and
keeps following Ark-0, but does not check the block signatures. The operator's block producer,
`contrib/k3s/ark0/producer/ark0.py`, signs with the functional test framework's `signet_pq.py`.

Two candidates were built for M2 and run on two staging signets before one was chosen for Ark-0.
`patches-m2a/` is the first: the four general patches above, without Ark-0's height and key.
`patches-m2b/`, four patches on top of it, is the second: a signet whose challenge is derived from an
ML-DSA-44 key, so that the ML-DSA-44 signature is the signet solution itself
(`-signetpqchallenge`, `doc/signet-pqchallenge.md`). Ark-0 took the first because it keeps the
network: a new challenge would have been a new chain, and a node that does not know the second
rule accepts any block of such a signet. The second is kept as an experiment and is not used on Ark-0.

Verified in the k3s cluster (`contrib/k3s`) on the build the Ark-0 nodes have run since 2026-09-30,
which `contrib/k3s/ARK0-MIGRATION.md` records: `test_bitcoin` passed 750 of 756 test cases and one
more with a warning, and skipped 5; 7 P2MR and signet functional scripts passed,
`feature_signet_pqblock.py` among them; and the default functional suite passed 275 tests and skipped
17. A second build, from a fresh clone with an empty compiler cache and the files in `patches-m2/` as
published here, produced the same five binaries, byte for byte.

Review status: the design went through three rounds of review and the series through four, by Codex
(gpt-6-sol at maximum effort) and Claude (Fable 5.1) independently; Fable passed the series in the
second round, gpt-6-sol in the fourth. The Ark-0 commit, 53, and the block producer were reviewed by
gpt-6-sol, in four rounds, and gpt-6-astra.

```bash
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch \
       ../bitcoin-p2mr-patches/patches-spacing/*.patch ../bitcoin-p2mr-patches/patches-m1/*.patch \
       ../bitcoin-p2mr-patches/patches-m2/*.patch
```

The series is exported from the `p2mr-m2` branch of the Core tree, which carries the four earlier
patch sets below the five M2 commits, and 54 from `p2mr-m2-tests`, one commit on top of it; `01541e4`
is the last M1 commit on that branch:

```bash
git format-patch --start-number 49 01541e4..p2mr-m2 -o patches-m2
git format-patch --start-number 54 p2mr-m2..p2mr-m2-tests -o patches-m2
(cd patches-m2 && sha256sum -b *.patch) > SHA256SUMS-m2
```

## Experimental M3 series (`patches-m3/`)

Eleven patches, applied on top of `patches/`, `patches-m05/`, `patches-spacing/`, `patches-m1/` and
`patches-m2/`, in that order.

They add a P2MR leaf version, `0xe0`. From a set height, a leaf of this version is executed as a leaf
of version `0xc0` is. Whether its elliptic curve signature checks work depends on a few designated
outputs, the tripwire outputs: in every block whose parent leaves one of them missing from the UTXO
set, those checks fail. On Ark-0 the height is 30000 and the two outputs are fixed in the code.
Another custom signet sets both with `-signettripwire=<height>@<txid:vout>[,<txid:vout>...]`, which
needs `-signetmldsa44=<height>` with a lower height; the series adds that option too, so that such a
signet can activate `OP_CHECKMLDSA44` as well. Ark-0 refuses both options.

It is an experiment of the Ark-0 test signet, a test network for trying things out that may be reset
at any time. It is a drill: elliptic curve checks are disabled inside one leaf version, and the
operator holds the key of a tripwire output and spends it at an announced time. It follows the
tripwire idea (P2XX-T) of Pieter Wuille's mail of 2026-06-25, which `ark0/NETWORK.md` cites with the
other earlier work. It is not part of BIP-360 or BIP-361, and it is not proposed for Bitcoin.
`doc/p2mr-tripwire.md`, added by the series, is its specification;
`ark0/NETWORK.md`, "Rule change at height 30000 (M3)", says what it changes on Ark-0 and what it does not.

| # | Commit | Content |
|---|---|---|
| 55 | script: add P2MR leaf version 0xe0, whose elliptic curve checks can be disabled | `P2MR_LEAF_TRIPWIRED`, control byte `0xe1`: with `SCRIPT_VERIFY_P2MR_TRIPWIRE`, a leaf of this version at depth one or more is executed as one of `0xc0`, `OP_CHECKMLDSA44` included, and without it, or in P2TR, it is an unknown leaf version; with `SCRIPT_VERIFY_P2MR_EC_DISABLED` too, `OP_CHECKSIG`, `OP_CHECKSIGVERIFY` and `OP_CHECKSIGADD` in such a leaf fail the script on a 32-byte key and a non-empty signature (`P2MR_EC_DISABLED`), after the validation weight is charged and before the signature or the signature cache is looked at |
| 56 | consensus: deploy P2MR leaf version 0xe0 with tripwire outputs | buried deployment `p2mr_tripwire` and the tripwire outputs as consensus parameters, left unset for Ark-0 here; `-signettripwire` and `-signetmldsa44` for other custom signets (`-signettripwire` on regtest too), refused on Ark-0 and on the chains they are not for; regtest from genesis without tripwire outputs; never on the default signet, the testnets or mainnet; the rules in the startup log |
| 57 | validation: fail elliptic curve checks in leaf version 0xe0 once a tripwire output is missing | `ConnectBlock` adds `SCRIPT_VERIFY_P2MR_EC_DISABLED` once per block, before any of its transactions, when a tripwire output is not present in the UTXO set the block builds on; present means unspent and created below the height, so an output that never existed, or that a deep reorganization moved to the height or above, counts as missing |
| 58 | policy: relay spends of leaf version 0xe0 once active, and check them again when a tripwire goes | no spend through such a leaf at depth one or more while the next block is below the height, `-acceptnonstdtxn` or not; the standardness rules of `0xc0` leaves at every height; the mempool checks against the next block's state, and when a block disables the checks, it checks the spends through such leaves again and removes those that fail, with their descendants, during a reorganization once the disconnected transactions are back |
| 59 | rpc, validation: report the tripwire outputs (gettripwireinfo) and log their spends | `gettripwireinfo`; the state in the log at startup and when the chain reaches the height; a log line for each spend of a tripwire output, once its block is in the active chain |
| 60 | test: add script tests for P2MR leaf version 0xe0 | 83 rows of `script_tests.json` with real Schnorr, ECDSA and ML-DSA-44 signatures, under each combination of `P2MR_TRIPWIRE` and `P2MR_EC_DISABLED`, and their generator |
| 61 | test: add unit tests for the P2MR tripwire parameters, options and state | the heights and outputs of each network, the options and every way they are refused, which outputs are present, and that `ConnectBlock` reads the state from the view it is given |
| 62 | test: add P2MR leaf version 0xe0 script test rows signed by another implementation | 14 rows signed by p2mr-ts: S3 spent through either leaf with the checks working and disabled, signatures made for the other leaf version, and S2 in a leaf of version `0xe0` |
| 63 | test: add feature_p2mr_tripwire.py | on regtest, with three tripwire outputs and six nodes: activation, the trigger, the mempool, reorganizations, the caches, restarts, pruning, assumeutxo, a late upgrade, the logs and the RPC |
| 64 | doc: describe P2MR leaf version 0xe0 and its tripwire outputs | `doc/p2mr-tripwire.md` |
| 65 | consensus: set the two P2MR tripwire outputs of the Ark-0 signet | `ARK0_TRIPWIRE_OUTPOINTS`, the two outputs created at height 15748, and `ARK0_TRIPWIRE_HEIGHT`, 30000; a unit test rebuilds each output's scriptPubKey from its key and compares it with the one on that chain |

It is a soft fork. A node built without `patches-m3/` accepts every block a node with it accepts and
keeps following Ark-0; to it, `0xe0` is an unknown leaf version, so it neither executes these leaves
nor checks the rule. Bitcoin Core's wallet does not create or sign these leaves. The wallet side is
in [p2mr-ts](https://github.com/duncan0k/p2mr-ts): it builds outputs of S3, the one template the
specification recommends, and signs spends through either of its leaves, as its README describes.

Before the release, the series ran on a staging signet of its own, with `-signetmldsa44` and
`-signettripwire`, two nodes with the rules and one without: S3 outputs were spent through both
leaves; blocks with an invalid signature in a leaf of version `0xe0` were refused by the two and
accepted by the third; after a tripwire output was spent at height 788, Schnorr spends through such
leaves were refused in the mempool and in blocks while ML-DSA-44 spends went on; and the network ran
for 24 hours from that block with no node restarting.

Verified in the k3s cluster (`contrib/k3s`) on the build the Ark-0 nodes have run since 2026-10-12, which
`contrib/k3s/ARK0-MIGRATION.md` records: `test_bitcoin` passed 758 of 764 test cases and one more
with a warning, and skipped 5; 10 P2MR and signet functional scripts passed,
`feature_p2mr_tripwire.py` among them; and the default functional suite, with the extended tests,
passed 280 and skipped 17. A second build, from a fresh clone with an empty compiler cache and the
files in `patches-m3/` as published here, produced the same five binaries, byte for byte.

Review status: Codex (gpt-6.1-sol at maximum effort) reviewed the design in three rounds before it
was frozen and the series in three more, and passed it in the third; gpt-6-astra then reviewed the
whole series and passed it too. The first round found that a node with the rules could stop on an
assertion when the first block of a reorganization repeats every transaction of the block it
replaces, coinbase included, and the next block spends a tripwire output. The fix is in 58, and
`feature_p2mr_tripwire.py` covers it. Both reviewers then checked patch 65, with Ark-0's height set,
together with this repository's release files, in two more rounds. In the first, both found the
height the same in the code, its two static assertions and the documentation, with no test tied to
the old value, and asked for fixes to this README's export command and to `contrib/k3s/m3-dev/`.
Both passed the second without findings.

```bash
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch \
       ../bitcoin-p2mr-patches/patches-spacing/*.patch ../bitcoin-p2mr-patches/patches-m1/*.patch \
       ../bitcoin-p2mr-patches/patches-m2/*.patch ../bitcoin-p2mr-patches/patches-m3/*.patch
```

The series is exported from the Core tree, where the eleven M3 commits sit on the five earlier
patch sets; `924e9f7` is the last commit of `patches-m2/` there, and `dde712f`, the commit that sets
the height, is the last of the series:

```bash
git format-patch --start-number 55 924e9f7..dde712f -o patches-m3
(cd patches-m3 && sha256sum -b *.patch) > SHA256SUMS-m3
```

## Experimental M4-S series (`patches-m4/`)

Twelve patches, applied on top of `patches/`, `patches-m05/`, `patches-spacing/`, `patches-m1/`,
`patches-m2/` and `patches-m3/`, in that order. **Not released, and not active on Ark-0**: the
activation height of Ark-0, `ARK0_SLHDSA128S_HEIGHT`, is left unset (`std::numeric_limits<int>::max()`),
and the validation weight of 2,000 per check is provisional, both to be fixed before a release.

They add `OP_CHECKSLHDSA128S` (byte `0xf1`, `OP_SUCCESS241`), an SLH-DSA-SHA2-128s (FIPS 205)
signature check in P2MR leaves that are executed, of leaf version `0xc0` and, once it executes, `0xe0`:
part M4-S of the M4 design, `docs/m4/m4-design-2026-10-14.md` §2.1–§2.2. The public key, 32 bytes, is
pushed by the leaf; the 7,856-byte signature comes in sixteen witness elements, `520 x 15, 56` (57 with
an explicit hash type); the message is the BIP-341 signature message with key_version 2, and the check
is pure SLH-DSA with the context string `P2MR/SLHDSA128S/EXP0`. The one relayed template is SLH1,
`<pk> OP_CHECKSLHDSA128S`. Another custom signet activates it with `-signetslhdsa128s=<height>`, which
Ark-0 refuses. It is an experiment of the Ark-0 test signet; it is not part of BIP-360, and it is not
proposed for Bitcoin. `doc/p2mr-slhdsa128s.md`, added by the series, is its specification;
`docs/m4/C1-REPORT.md` maps each rule to the code and the tests.

| # | Commit | Content |
|---|---|---|
| 66 | crypto: vendor slhdsa-c for SLH-DSA-SHA2-128s signature verification | [slhdsa-c](https://github.com/pq-code-package/slhdsa-c) at `a0fc1ff2`, the commit liboqs 0.15.0 uses (Apache-2.0 OR ISC OR MIT, used under MIT), unmodified, the SHA2 files only, in `src/crypto/slhdsa128s/` with a README of every file's SHA-256, compiled with Core's hardening, Fortify and sanitizer options and a warning set of its own; `SLHDSA128SVerify` over a 32-byte message, which checks every length before it calls the library |
| 67 | test: check SLH-DSA-SHA2-128s verification against the ACVP vectors | NIST ACVP sigVer (14 external pure cases, 2 valid, 4 of a wrong signature length; 14 internal) and sigGen (14) for SLH-DSA-SHA2-128s, through a test-only adapter for their message lengths; no wrong length reaches the library; a signature made by @noble/post-quantum; other interfaces and malformed signatures |
| 68 | script: add OP_CHECKSLHDSA128S for SLH-DSA-SHA2-128s checks in P2MR leaves | byte `0xf1` with `SCRIPT_VERIFY_P2MR_SLHDSA128S` in executed P2MR leaves of version `0xc0` and `0xe0`; sixteen signature chunks; key_version 2; context `P2MR/SLHDSA128S/EXP0`; `VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK`, provisionally 2000; four new script errors; cache entries padded with `H`; `DeferringSignatureChecker` forwards the check |
| 69 | consensus: deploy OP_CHECKSLHDSA128S, not yet at a height on Ark-0 | buried deployment `p2mr_slhdsa128s`; `ARK0_SLHDSA128S_HEIGHT` left at `std::numeric_limits<int>::max()`; `-signetslhdsa128s` for other custom signets, refused on Ark-0 and on other chains; regtest from genesis or `-testactivationheight=p2mr_slhdsa128s@N`; the startup log |
| 70 | policy: relay OP_CHECKSLHDSA128S spends only once active, and in one template | no spend executing `0xf1` while the next block is below the height, `-acceptnonstdtxn` or not; removed from the mempool by a reorganization below the height; SLH1 the only standard leaf with the opcode, its sixteen chunks exempt from the 80-byte limit |
| 71 | test: sign and verify SLH-DSA-SHA2-128s in the functional test framework | an SLH-DSA-SHA2-128s implementation of its own, which passes the ACVP keyGen, sigGen and sigVer vectors of the parameter set, and the SLH1 helpers |
| 72 | test: add OP_CHECKSLHDSA128S script tests and signature message vectors | `p2mr_slhdsa128s_tests`: the parameters, signature messages for key_version 0, 1 and 2, and every rule with spends signed in the test by the vendored library |
| 73 | test: add feature_p2mr_slhdsa128s.py | activation, including the first active block in leaf versions `0xc0` and `0xe0`, relay, reorganization, block-level rejection of each new error, the validation weight, policy, mixed inputs |
| 74 | test: add fuzz targets for SLHDSA128SVerify and OP_CHECKSLHDSA128S | `slhdsa128s_verify`, `p2mr_slhdsa128s` |
| 75 | bench: time SLH-DSA-SHA2-128s verification and the whole path of its check | `VerifySLHDSA128S`, `VerifyScriptP2MRSLHDSA128S` |
| 76 | test: check OP_CHECKSLHDSA128S against the M4 vectors of p2mr-ts | the spend and construction vectors of the wallet side, pinned: signature messages for key_version 0, 1 and 2, signatures, witnesses through `VerifyScript`, keys, leaves, trees and addresses |
| 77 | doc: describe OP_CHECKSLHDSA128S | `doc/p2mr-slhdsa128s.md` |

It is a soft fork. A node built without `patches-m4/` accepts every block a node with it accepts; to it,
`0xf1` stays `OP_SUCCESS241`. Bitcoin Core's wallet does not create or sign these leaves.

Verified on 2026-10-15 in a cloud session (4 vCPU Xeon at 2.8 GHz, no SHA extensions, GCC 13.3), from a
fresh clone of v31.1 with all 77 patches applied as their `SHA256SUMS` files name them: `test_bitcoin`
passed 780 of 786 test cases and one more with a warning, and skipped 5 (22 test cases are new); the P2MR,
signet and related functional scripts of the M3 verification (16 runs) passed, and `feature_p2mr_slhdsa128s.py`
with them. The build had no compiler warnings. After the first review round the unit tests also ran in a
`-DSANITIZERS=address,undefined` build (all pass but `net_tests`, stopped by a UBSan report in upstream code
the patches do not touch), and both fuzz targets ran 10 minutes each in Core's own fuzz build
(`-DBUILD_FOR_FUZZING=ON -DSANITIZERS=fuzzer,address,undefined`, the vendored library instrumented too)
without a finding. After the third round (the C options chosen in the library's own CMake: Core's
hardening probed with the C compiler, Fortify's pair kept together, sanitizers as for libsecp256k1, a warning
set of its own), the same 77 patches gave the same unit test results from a fresh checkout, and seven
configurations (default, `-D_FORTIFY_SOURCE=2 -Werror` in `CMAKE_C_FLAGS`, `THREADS_HAVE_PTHREAD_ARG`,
`CC=clang CXX=g++`, a source path with a space, the fuzz build, ASan/UBSan) configure, each giving the
vendored files `_FORTIFY_SOURCE` 3 like Core's C++.
The first measurement of the validation weight gives a required C of 2,550 to 3,658 on that machine,
above the provisional 2,000; `docs/m4/C1-REPORT.md` has the numbers and the open questions.

The measurement needs a hook in the library that makes verification walk its slowest path. That hook
and the scripts are in `contrib/m4-measure/`, as a patch applied on top of the series in a separate
tree, for `bench_bitcoin` only: nothing applies it, it is not in `patches-m4/`, and with it applied
`bitcoind` fails to compile. Its README says how to repeat the measurement.

```bash
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch \
       ../bitcoin-p2mr-patches/patches-spacing/*.patch ../bitcoin-p2mr-patches/patches-m1/*.patch \
       ../bitcoin-p2mr-patches/patches-m2/*.patch ../bitcoin-p2mr-patches/patches-m3/*.patch \
       ../bitcoin-p2mr-patches/patches-m4/*.patch
```

The series is exported from the Core tree, where the twelve M4-S commits sit on the six earlier patch
sets as `git am` applies them; `d9c0c03` is the last commit of `patches-m3/` there:

```bash
git format-patch --start-number 66 d9c0c03..22f236f -o patches-m4
(cd patches-m4 && sha256sum -b *.patch) > SHA256SUMS-m4
```

## Prebuilt node

The release `ark0-node-m3` of this repository carries the build the Ark-0 nodes run, all six patch
sets: the five binaries for x86_64 Linux in an archive, and the same build as a container image,
`ghcr.io/duncan0k/ark0-node`. Its `SHA256SUMS` lists the archive, the binaries in it and the image
manifest, whose SHA-256 is the image digest, and `SHA256SUMS.asc` signs that file with the release key
in `ark0/release-signing-key.asc`. `ark0/JOIN.md`, section 1, says how to check both and what the
binaries need from the system.

The binaries are reproducible, on two conditions. In the build image that `contrib/k3s` uses, the
release build and a second build from a fresh clone, with an empty compiler cache and the files in
`patches-m3/` as published, produced the same five binaries byte for byte. That image comes from
`contrib/k3s/Dockerfile.builder`, which pins its Ubuntu 24.04 base by digest but not its package
versions: the archive's `BUILDER-PACKAGES.txt` lists the version of every package it had, and
`PROVENANCE.txt` the toolchain, so an image built later matches only if it installs those versions
(snapshot.ubuntu.com serves past ones). And the build path, `/work/src`, is part of the binaries, so a
rebuild elsewhere has to use it too.

The releases of the witness program (below) carry builds of the same code for its systems, inside its
archives and without the wallet, GUI, ZMQ, IPC and USDT: `bitcoind.exe` for 64-bit Windows; from
`ark0-witness-0.2.0` on also `bitcoind` for macOS 14 and later, one universal file for Apple silicon and
Intel, and `bitcoind` for 64-bit Linux with glibc 2.35 or later, on x86_64 and on aarch64. Each is Bitcoin
Core v31.1 with the six patch sets at the tag `ark0-node-m3`, cross-compiled with Core's own depends system
by a script that stops unless Core and this repository are at the commits it names.

`contrib/windows/build.sh` makes the Windows build, as root in a fresh Ubuntu 24.04 container or machine,
with the mingw-w64 toolchain with POSIX threads; it links without time stamps and strips with
`SOURCE_DATE_EPOCH=1`. Two clean builds gave the same bytes, and a third, with the script as published
here, gave them again. As with the Linux build, a later build matches only with the same package versions
and only under `/work/src`: `contrib/windows/packages.txt` lists every package of that third build (among
them mingw-w64 11.0.1 with GCC 13.2.0, binutils 2.41.90 and CMake 3.28.3), and the script writes the list
for its own build to `/work/out/packages.txt`. With `PHASE=full`, the default, it also installs Wine, which
its own list then includes, runs the unit tests and syncs the stripped `bitcoind.exe` with Ark-0 under Wine.

`contrib/cross/build.sh` makes the other builds, one `TARGET` at a time, as root in a fresh container:
`macos-arm64` and `macos-x86_64` in Debian 13 with clang and lld 19 and the macOS SDK that Core v31.1 pins,
which the script downloads from bitcoincore.org and checks by its SHA-256 and which is not redistributed;
`linux-x86_64` and `linux-aarch64` in Ubuntu 22.04 with GCC 12, linking the C++ runtime statically, and
failing if a binary needs a glibc newer than 2.35 or any library beyond libc, libm and the loader. The macOS
builds are not stripped: the linker signs the arm64 build ad hoc, and Apple silicon runs only signed code.
`contrib/cross/lipo.sh` checks both macOS builds, joins them into one universal file and checks that it
holds them unchanged; that the signatures are valid is checked on a Mac with `codesign --verify --strict
--all-architectures`. Two clean builds of each of the four targets gave the same bytes, and a third of each
Linux build, with the script as published here, gave them again. As for Windows, a later build matches only
with the same package versions and under `/work/src`: `contrib/cross/packages-*.txt` list the packages of the
published builds.

## Witness program (`witness/`)

Ark0Witness is a small program for Windows 10 and 11, macOS 14 and later and 64-bit Linux that runs the
build of the node above for its system and shows, on a page served on 127.0.0.1 only, what that node
reports during the M3 drill: how far it has synced, whether it is on Ark-0, block 30000 and the two
tripwires, and whether elliptic curve signatures in leaves of version `0xe0` are still accepted. It can also
have the node judge a raw transaction with `testmempoolaccept`; the transaction is never broadcast. The
release `ark0-witness-0.2.0` has a zip for Windows, a zip for macOS and a tarball each for Linux on x86_64
and on aarch64, their `SHA256SUMS` and the signature of that file by the release key. None of the programs
is code-signed, and the macOS ones are not notarized.
`witness/README.md` says what it does and does not do, how it is built (Go and its standard library only,
reproducibly) and how it is tested.

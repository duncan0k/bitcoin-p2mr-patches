# bitcoin-p2mr-patches

Patch series implementing the BIP-360 Pay-to-Merkle-Root (P2MR) spending rules on **Bitcoin Core v31.1**
(milestone M0: no post-quantum signatures). Purpose: give a custom signet P2MR rules that are enforced in
**block validation**, reproducibly.

- Base: `bitcoin/bitcoin` tag `v31.1` (commit `9be056a`)
- Specification: BIP-360 v0.12.1 at `bitcoin/bips` commit `620871a7a442e276a058b487cd8743775fb499a4`
- License: same as Bitcoin Core (MIT); patches authored by CipherScope <dev@cipherscope.io>
- Status: experimental. **Not deployed on, and not intended for, mainnet or the public signet** (the deployment
  never activates there).

## Layout

```
patches/            10 git format-patch files, apply in order with git am
patches-m05/        24 further patches, the M0.5 wallet series (see below)
patches-spacing/    1 patch, the retarget spacing Ark-0 uses from height 8064 (see below)
SHA256SUMS          checksums of the patches
SHA256SUMS-m05      checksums of the M0.5 patches
SHA256SUMS-spacing  checksum of the retarget spacing patch
apply.sh            clone v31.1, verify, apply the consensus series, build, run the core tests
ark0/               evidence pack for the experimental custom signet (see below)
contrib/k3s/        build, test and run the series inside a k3s cluster
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

- No post-quantum signatures, and no activation on mainnet or the public signet. These two hold for
  everything in this repository.
- The **M0 consensus series** in `patches/` is spending rules only: no `tmr()` descriptor, no wallet
  signing, no address generation. Those are the M0.5 series in `patches-m05/`, described below, which
  is a separate series applying on top.
- Describe it as: "an experimental signet implementing the BIP-360 v0.12.1 P2MR spending rules on
  Bitcoin Core v31.1, enforced at block validation, independently reproducible". It is not "the first",
  "the only", "the real bc1z", "mainnet-ready", or "a quantum-resistant network".
- Show only `tb1z` (signet) / `bcrt1z` (regtest) addresses. Do not generate single-leaf (m = 0) outputs
  from tooling: consensus accepts them per the BIP, and they are anyone-can-spend.

## Ark-0 evidence pack (`ark0/`)

`ark0/` holds what an outside reader needs to check the custom-signet claims offline: the network
parameters (`NETWORK.md`); the confirmed P2MR spend and the three rejected blocks as raw hex, with the
node responses they produced (`evidence/`); a block file covering genesis to height 1264 that a fresh
patched node replays with `-loadblock` (`snapshot/`); and the step-by-step `REPRODUCE.md`. The network
itself has no public endpoint; the pack is the way to verify it.

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
outside it. Since 2026-09-16 the verifying node of the Ark-0 signet runs a build of this series while
the block producing node runs the consensus series without it, so the two are continuously checked
against each other on a live chain; a divergence would be the finding. Since 2026-09-22 both also
carry `patches-spacing/`, described below. A job every six hours funds a P2MR
address from one node and spends it back from the other through the PSBT flow, asserting the witness
dimensions each time. `contrib/k3s/ARK0-MIGRATION.md` records that roll, the build it came from, and
the first round trip it produced.

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
difficulty since height 4032, which `ark0/NETWORK.md` records with the numbers. Both Ark-0 nodes run
the patch with `-signetpowtargetspacing=90@8064`, so the retargets from height 8064 on aim at 90 s
per block. It is a consensus rule of that network: a node without the patch and the option follows
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

Verified on 2026-09-22 in the k3s cluster (`contrib/k3s`), in the two builds the Ark-0 nodes run,
which `contrib/k3s/ARK0-MIGRATION.md` records: with `patches/`, `test_bitcoin` passed 736 of 742 test
cases with 5 skipped, and `feature_p2mr`, `feature_p2mr_signet`, `feature_signet`, `tool_signet_miner`
and `p2p_segwit` passed; with `patches/` and `patches-m05/`, `test_bitcoin` passed 739 of 745 with 5
skipped, and the default functional suite passed, 273 tests with 17 skipped. The patch is exported
from a branch of the Core tree based on the last consensus commit `be23b12`:

```bash
git format-patch --start-number 35 -1 <commit> -o patches-spacing
(cd patches-spacing && sha256sum -b *.patch) > SHA256SUMS-spacing
```

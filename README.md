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
patches/      10 git format-patch files, apply in order with git am
SHA256SUMS    checksums of the patches
apply.sh      clone v31.1, verify, apply, build, run the core tests
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

- No post-quantum signatures; no `tmr()` descriptor or automatic wallet signing (follow-up); no
  activation on mainnet or the public signet.
- Describe it as: "an experimental signet implementing the BIP-360 v0.12.1 P2MR spending rules on
  Bitcoin Core v31.1, enforced at block validation, independently reproducible". It is not "the first",
  "the only", "the real bc1z", "mainnet-ready", or "a quantum-resistant network".
- Show only `tb1z` (signet) / `bcrt1z` (regtest) addresses. Do not generate single-leaf (m = 0) outputs
  from tooling: consensus accepts them per the BIP, and they are anyone-can-spend.

## Follow-up series: M0.5 wallet support (branch `m05`, not part of the first release)

`patches-m05/` (checksums in `SHA256SUMS-m05`) applies on top of the ten consensus patches and adds:

- a **provisional** `tmr(TREE)` descriptor (same tree grammar as `tr()`, no internal key, single-leaf
  trees rejected because they are anyone-can-spend, depth limited to 128);
- P2MR spend data in the signing provider and script-path signing in the wallet (`send`, PSBT);
- a deployment gate: importing or deriving `tmr()` receiving addresses is refused on chains where the
  P2MR deployment is disabled (mainnet, testnets, the default signet), since such outputs would be
  anyone-can-spend there;
- fee estimation that treats P2MR inputs as witness inputs, control-block validation before signing,
  a dedicated fuzz target, `wallet_p2mr.py` and `wallet_p2mr_signet.py`.

Review status: two rounds of independent review; the second round found the earlier high and medium
findings fixed. Published separately once it has been exercised on the experimental signet.

```bash
git am ../bitcoin-p2mr-patches/patches/*.patch ../bitcoin-p2mr-patches/patches-m05/*.patch
```

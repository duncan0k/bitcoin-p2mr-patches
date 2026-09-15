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

---

# 中文说明

BIP-360 Pay-to-Merkle-Root（P2MR）花费规则在 **Bitcoin Core v31.1** 上的补丁系列（M0：不含任何后量子签名）。
目的：给一条 custom signet 提供在**区块验证层**真实生效的 P2MR 规则，可独立复现。

- 基线：`bitcoin/bitcoin` tag `v31.1`（commit `9be056a`）
- 规范：BIP-360 v0.12.1，`bitcoin/bips` commit `620871a7a442e276a058b487cd8743775fb499a4`
- 许可：与 Bitcoin Core 相同（MIT）；补丁署名 CipherScope <dev@cipherscope.io>
- 状态：实验性。**未经任何主网或公共 signet 部署，也不应部署到那里**（补丁在这些链上永不激活）。

## 目录

```
patches/      10 个 git format-patch 文件，按序 git am
SHA256SUMS    补丁校验和
apply.sh      克隆 v31.1、校验、打补丁、构建、跑核心测试
```

## 使用

```bash
bash apply.sh            # 在 ./bitcoin 里完成克隆、打补丁、构建与测试
# 或手工：
git clone --branch v31.1 --depth 1 https://github.com/bitcoin/bitcoin.git && cd bitcoin
git am ../bitcoin-p2mr-patches/patches/*.patch
cmake -B build -DBUILD_TESTS=ON -DENABLE_WALLET=ON -DBUILD_GUI=OFF -DWITH_ZMQ=OFF -DENABLE_IPC=OFF
cmake --build build -j"$(nproc)"
./build/bin/test_bitcoin
build/test/functional/test_runner.py -j8 feature_p2mr feature_p2mr_signet p2p_segwit feature_taproot
```

## 补丁内容（按序）

| # | 提交 | 内容 |
|---|---|---|
| 1 | script: add P2MR (BIP 360) witness v2 program validation | `SCRIPT_VERIFY_P2MR` 标志；`VerifyWitnessProgram` 的 v2+32 分支（栈元素/annex 三条判定、控制块 `1+32m`、`m≤128`、`c[0]` 末位对所有叶版本、TapLeaf/TapBranch 折叠无 tweak、根匹配、m=0 立即成功、非 0xC0 成功、0xC0 走 tapscript）；`PrecomputedTransactionData::Init` 识别 `OP_2` 输入；buried deployment `DEPLOYMENT_P2MR` 与各链高度；`GetBlockScriptFlags` 接线 |
| 2 | policy: add P2MR (BIP 360) output type, address and relay rules | `TxoutType::WITNESS_V2_P2MR`、`WitnessV2P2MR` 目的地与 bech32m v2 地址、标准性规则、RPC 显示、`doc/p2mr.md` |
| 3 | test: add BIP-360 P2MR script_tests vectors and their generator | `test_framework/p2mr.py`、`test/util/generate_p2mr_script_tests.py`、`script_tests.json` 新增 P2MR 向量（含真实 Schnorr 签名）、官方 9 条构造向量 vendored |
| 4 | test: add feature_p2mr.py covering BIP-360 activation and consensus | 激活前后、mempool 与 `submitblock` 双重拒绝、非 0xC0/annex、混合 P2TR+P2MR、跨激活高度重组 |
| 5–7 | test: 合并修正 | 错误码命名对齐（`P2MR_WRONG_CONTROL_PARITY`、`WITNESS_PROGRAM_WITNESS_EMPTY`）、区块哈希确认、v31 的 `block-script-verify-flag-failed` 标签、m=128 控制块是标准的 |
| 8 | p2mr: policy checks control block shape; signet activation test; m=0 future-leaf vector | Codex 评审后续：策略层控制块形状检查、`feature_p2mr_signet.py`、m=0+未来叶版本向量 |
| 9 | policy: SCRIPT_VERIFY_P2MR is a consensus (mandatory) flag | v31.1 用 `flags & STANDARD_NOT_MANDATORY_VERIFY_FLAGS` 决定失败标签，共识标志必须进 MANDATORY，否则区块级失败被误标为非标准 |
| 10 | test: p2p_segwit uses 33-byte v2 programs as future versions | 32 字节 v2 程序已是 P2MR，沿用 taproot 当年对 v1 的处理 |

## 激活参数

| 链 | `P2MRHeight` |
|---|---|
| mainnet / testnet3 / testnet4 / 默认 signet | `INT_MAX`（永不激活；`getdeploymentinfo` 不列出 `p2mr`） |
| custom signet（自定 `-signetchallenge`） | `1` |
| regtest | `0`，可用 `-testactivationheight=p2mr@N` 覆盖 |

`SCRIPT_VERIFY_P2MR` 同时在 `MANDATORY_SCRIPT_VERIFY_FLAGS` 与 `STANDARD_SCRIPT_VERIFY_FLAGS`；mempool 在所有链上按 P2MR 规则做策略校验（与 0.21.0 时代的 taproot 一致），区块验证只在激活后执行。

## 验证记录（2026-09-15，Ubuntu 24.04 / g++ 13.3 / 64 核）

- 全量 `test_bitcoin` 通过；`script_tests.json` 含 33 条 P2MR 向量（m=0/1/2/128 成功、m=129 与长度错误、奇偶位、非 0xC0 与 DISCOURAGE 标志、栈形状与 annex、根不匹配、`OP_SUCCESSx`、三种 sighash 类型、`OP_CODESEPARATOR`、错签名）。
- 默认功能测试全集 **270/270 通过**（含新增的 `feature_p2mr.py`、`feature_p2mr_signet.py`；`--extended` 扩展项未跑）。
- Codex（gpt-6-astra，xhigh）红队结论：SHIP（以此起一条 custom signet 做实验），High 0；留到 M0.5 的项：descriptor 推断、钱包输出类型/找零、P2MR 专用 fuzz target、资源计量压力测试。

## 不做 / 不能说

- 不做后量子签名；不做 `tmr()` 描述符与钱包自动签名；不做主网或公共 signet 激活。
- 对外只说：「按 BIP-360 v0.12.1 固定 commit 在 Bitcoin Core v31.1 上实现 P2MR 花费规则的实验 signet，规则在区块层生效，可独立复现」。不说「首个 / 唯一 / 真正的 bc1z / 主网可用 / 抗量子网络」。
- 地址只展示 `tb1z`（signet）/ `bcrt1z`（regtest）。工具端禁止产出单叶（m=0）地址：共识按规范接受它，但它是 anyone-can-spend。

# bitcoin-p2mr-patches

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
- `feature_p2mr.py`、`feature_p2mr_signet.py`、`p2p_segwit.py`、`feature_taproot.py` 等通过；默认功能测试全集 268 项通过（`feature_dbcrash` 等扩展项未跑）。
- Codex（gpt-6-astra，xhigh）红队结论：SHIP（以此起一条 custom signet 做实验），High 0；留到 M0.5 的项：descriptor 推断、钱包输出类型/找零、P2MR 专用 fuzz target、资源计量压力测试。

## 不做 / 不能说

- 不做后量子签名；不做 `tmr()` 描述符与钱包自动签名；不做主网或公共 signet 激活。
- 对外只说：「按 BIP-360 v0.12.1 固定 commit 在 Bitcoin Core v31.1 上实现 P2MR 花费规则的实验 signet，规则在区块层生效，可独立复现」。不说「首个 / 唯一 / 真正的 bc1z / 主网可用 / 抗量子网络」。
- 地址只展示 `tb1z`（signet）/ `bcrt1z`（regtest）。工具端禁止产出单叶（m=0）地址：共识按规范接受它，但它是 anyone-can-spend。

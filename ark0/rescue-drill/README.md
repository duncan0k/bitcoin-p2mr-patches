# The rescue drill, step R3: a proof made off the chain

On 2026-10-30 the operator proved, off the chain, that it knows the seed of
`R`, the key of the two outputs of the rescue drill, and published the proof
here with its record. It is the kind of proof the rescue lane accepts from
height 51000 (`../NETWORK.md`, "Rule change at height 51000 (M4-R)"), for
the same statement, but it is made for a public message, not for a
transaction: it spends nothing, and no transaction can use it. The rescue
itself, step R4, comes after height 51000, with a new proof bound to the
rescue transaction.

| File | What it is |
|---|---|
| `r3-proof.bin` | the proof, 130,218 bytes, SHA-256 `9b8c07106b39d78ecc35e072ff1bb7bc4c0979c972be2769535d5f55906ec5fa` |
| `r3-record.json` | the record: the statement, the message, the SHA-256 of the proof and of the programs that made and checked it, the results of the three verifiers, and the scan made just before the record |

## The two outputs

The operator holds two outputs of 100,000 sats for the drill,
`12875980e1cd709e3bf930fce984089b580790683eed05d3d03112447f0f3753:0` and
`:1`, created at height 30068 at the address
`tb1z8lhdm44s849c4uyylwtzfzn0z4uemjrxtdz78vchw6ruynwrhlqqst7ypy`. Each has
two leaves of version `0xe0`, `<R> OP_CHECKSIG` and `OP_RETURN`. Since the
tripwire drill in block 36897 (`../NETWORK.md`, "What the chain shows")
disabled the elliptic curve checks in such leaves, neither output has a
valid witness.

## What is proved

The statement is `0x01` of [ark0-rescue](https://github.com/duncan0k/ark0-rescue)
`SPEC.md`: the prover knows a seed `x` with `HMAC-SHA256(K, x || suffix) = y`,
where

| Field | Value |
|---|---|
| `K` | `ark0-wallet v1 schnorr` |
| `suffix` | `0` |
| `y` | `52955b5022ed72de5b5fb8b63b8d2a3627ae71bdfb764acc209e0fca7f430a7e` |

`y` is the secret key of `R`: the x-coordinate of `y·G` is
`R = 907f297a12351f4a15126bd44dfc810baae2d7485871415bf5066419de480dd0`.

The proof is for this message of 96 bytes, `m_demo`:

```
SHA256("Ark-0 rescue drill R3: proof of seed knowledge" || H) || network_id || pk_commitment
```

- `H` is the hash of block 36897,
  `000001e1b8fa2d090999db861599132b86f465c47331f9ae64ad162e25478114`, as 32
  bytes in this order, not reversed;
- `network_id` is the SHA-256 of Ark-0's signet challenge,
  `afd1fcbb77d445f14e06eb1a7dff52807c71d7765c311e6181e698734337b38e`;
- `pk_commitment` is `TaggedHash("P2MR/Rescue/Key", 0x00 || R)`,
  `b506cfd491d80bac701c7ae6c6722887b89ea90963432bd87ce7092b33a12f9d`.

In hex, `m_demo` is:

```
2174cae36b0f4c1c66196047fb0282fe07a86c79c842504a1c9152501d161d02afd1fcbb77d445f14e06eb1a7dff52807c71d7765c311e6181e698734337b38eb506cfd491d80bac701c7ae6c6722887b89ea90963432bd87ce7092b33a12f9d
```

## How to check it

Run the three verifiers of ark0-rescue, at the commit the record names:

```bash
git clone https://github.com/duncan0k/ark0-rescue.git
cd ark0-rescue
git checkout 71b34baec9f3655733175881a4358284ffa18fe2
K='ark0-wallet v1 schnorr'
Y=52955b5022ed72de5b5fb8b63b8d2a3627ae71bdfb764acc209e0fca7f430a7e
M=2174cae36b0f4c1c66196047fb0282fe07a86c79c842504a1c9152501d161d02afd1fcbb77d445f14e06eb1a7dff52807c71d7765c311e6181e698734337b38eb506cfd491d80bac701c7ae6c6722887b89ea90963432bd87ce7092b33a12f9d
PROOF=/full/path/to/r3-proof.bin   # a full path: go -C go runs in go/
python3 -I python/tools/verify_cli.py --k "$K" --suffix 0 --y-hex "$Y" --msg-hex "$M" --proof "$PROOF"
go -C go run ./cmd/voleith-verify --k "$K" --suffix 0 --y-hex "$Y" --msg-hex "$M" --proof "$PROOF"
(cd rust && cargo build --profile cli --features cli &&
  target/cli/voleith-verify --k "$K" --suffix 0 --y-hex "$Y" --msg-hex "$M" --proof "$PROOF")
```

Each prints `valid` and exits with 0. With the last byte of `M` changed from
`9d` to `62`, each prints `invalid` and exits with 1. The Go verifier needs
Go 1.24 or later.

The record holds the same checks as the operator ran them: the Rust and
Python verifiers on its Windows machine, the Go one with go1.27.3 on Linux.
The proof was made with `voleith-prove.exe` and checked in Rust with
`voleith-verify.exe`, as ark0-rescue's `rust/build-windows.sh` builds them;
the SHA-256 of both in the record are those that `NOTES.md` there lists for
that build. In `r3-record.json` the paths on the operator's machines are
replaced by names in angle brackets, such as `<r3-proof.bin>`; nothing else
in it was changed.

## The scan before the record

The operator scanned the chain from 15:18:34 to 15:20:18 UTC and wrote the
record at 15:20:23 UTC. All that time its four nodes stayed at height 37167,
`000000bdd6276e33400c8ef25446e3f5f55be8ff88d69a2098db3a21d75fe17d`:

- `scantxoutset` on one of them found exactly the two drill outputs at the
  five addresses of the drill seed, listed below;
- no transaction in the mempools of the four spent either output or paid to
  one of the five addresses;
- each of the four reported the drill's tripwire output spent and the
  elliptic curve checks in leaves of version `0xe0` disabled for the next
  block.

## `y` is public, so `R` is burned

The proof publishes `y`, the secret key of `R`, so anyone can now sign for
`R` wherever elliptic curve checks work. Apart from the rescue, the operator
will not use `R` or the drill seed again. Do not send anything to the
addresses of the drill seed, the four the web wallet derives from it
(`../WEB-WALLET.md`) and the address of the two outputs:

| Address | What it is |
|---|---|
| `tb1zu0yle0celmkceg6d3774fnxsv53vs7272clw84ghh0jggmyhkjkq05advg` | Schnorr address |
| `tb1z9qpt7hn8ve7zqev0lg5nf2hpaw9gg3u0xyfks7qfsu8evps633xswuk42l` | ML-DSA-44 address |
| `tb1z3wuf5nmsgykkrj5x7dsthsmjy0utmthsnd99e54q4whxmzfwedtqp5le5y` | mixed address |
| `tb1zdhprtyx8pauvkdu3kcw6aqel2ckcvd0qsntdq97w8cvcw8e0dz9swa40xs` | SLH1 address |
| `tb1z8lhdm44s849c4uyylwtzfzn0z4uemjrxtdz78vchw6ruynwrhlqqst7ypy` | the two drill outputs |

Until the rescue, the two outputs are safe only while the tripwire holds.
The record says:

> From now until the rescue on chain (after height 51000), the two drill
> outputs stay safe only while elliptic curve checks in leaves of version
> 0xe0 stay disabled, that is, while the block producer signs no branch that
> leaves out the tripwire spend.

A reorganization that undid block 36897 would give the checks back, and
anyone could then spend both outputs with a signature by `R`. On Ark-0 only
the block producer, the operator, can make such a branch.

## What it does not show

- It moves nothing. The rescue lane opens at height 51000.
- It says nothing about whether the proof system is sound; the system has
  had no external review (`../NETWORK.md`, "What it rests on is narrow"). It
  shows only that a proof made with the Rust prover for a real seed passes
  all three verifiers.

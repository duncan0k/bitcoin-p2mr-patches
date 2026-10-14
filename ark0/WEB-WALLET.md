# The web wallet's keys (derivation version 1)

The operator's web wallet, <https://wallet.ark0.si>, keeps one 32-byte seed in
the browser's local storage and makes every key it uses from that seed. This
file says how, so that anyone can recompute a wallet's keys and addresses from
its backup code with code of their own. `web-wallet-vectors.json` has test
vectors: each value was computed twice, by the wallet's own code and by a
separate Python implementation, and the two agree. The last section lists
three limits of this derivation.

None of this is a consensus rule. The derivation is fixed: another label would
give other keys from the same seed, and another login text another seed from
the same account.

## Where the seed comes from

- **Create a wallet:** 32 bytes from the browser's cryptographic random
  source (`crypto.getRandomValues`).
- **Restore:** a backup code (below), and nothing else. The wallet does not
  import a private key, a mnemonic or another wallet's file, so every key it
  holds comes from a seed through HMAC-SHA256.
- **Log in with your wallet, until 2026-10-14:** an Ethereum-style account in
  a wallet app signed a fixed text with `personal_sign` (EIP-191). The page
  asked for the signature twice and went on only if both recovered to the
  account and gave the same `r || s` below. The seed is HMAC-SHA256 keyed with
  the ASCII text `ark0-wallet v2 login` over the 64 bytes `r || s`, each 32
  bytes big-endian, with `s` replaced by `n - s` when it is above `n / 2`,
  where `n` is the order of secp256k1. The text is in the vectors file. An
  earlier text, version 1, names the wallet's previous web address; its seed
  is made the same way with the key `ark0-wallet v1 login`. The page no longer
  offers this (see limit 3); a wallet opened this way still opens in a browser
  that keeps it.

## The backup code

`ark0w1-`, the seed in lowercase hex, `-`, and the first four bytes of
SHA-256 of the seed in lowercase hex. On input, spaces, line breaks and
letter case are ignored. The checksum catches typing mistakes and nothing
else.

## The keys

Below, `HMAC(k, m)` is HMAC-SHA256 keyed with the ASCII text `k` over `m`,
and `||` joins byte strings.

- **Schnorr (BIP-340):** the secret key is
  `HMAC("ark0-wallet v1 schnorr", seed || c)` for the first one-byte counter
  `c`, counting from 0, at which this HMAC, read as a big-endian number, lies
  in `1..n-1`. Counter 0 fails with a probability of about 2^-128. The
  addresses use its x-only public key.
- **ML-DSA-44 (FIPS 204):** `xi = HMAC("ark0-wallet v1 mldsa44", seed)`, and
  the key pair is `ML-DSA.KeyGen_internal(xi)` (Algorithm 6).
- **Declaration keys:** key `i`, for `i` from 0 to 255, is
  `ML-DSA.KeyGen_internal(HMAC("ark0-wallet v1 declaration", seed || i))`,
  with `i` as one byte. They sign the migration-readiness declaration on the
  wallet's site and never sign a transaction.

## The addresses

All three are P2MR outputs (witness version 2, bech32m, `tb1z...` on Ark-0)
whose witness program is the root of a tree of exactly two leaves at depth
one: `TapBranch` of the two `TapLeaf` hashes, as in BIP-341, with no key path
and no tweak.

- **Schnorr address:** leaf version `0xc0`, the leaves
  `<x-only key> OP_CHECKSIG` and `OP_RETURN` (`6a`).
- **ML-DSA-44 address:** leaf version `0xc0`, the leaves S1,
  `4d0802 <520 bytes> 4d0802 <520 bytes> 4d1001 <272 bytes> f0` (the
  1312-byte public key in three `OP_PUSHDATA2` pushes, then
  `OP_CHECKMLDSA44`, byte `0xf0`), and `OP_RETURN`. The wallet shows it once
  the chain is six blocks past height 8600 (M1).
- **Mixed address:** template S3 of `doc/p2mr-tripwire.md` in the patched
  tree: leaf version `0xe0`, the leaves `<x-only key> OP_CHECKSIG` and S1. The
  wallet shows it once the chain is six blocks past height 30000 (M3).

`NETWORK.md`, "Rule change at height 8600 (M1)", says why the first two trees
have an `OP_RETURN` leaf: a single leaf would sit at depth zero and be spent
without being executed.

## The vectors

`web-wallet-vectors.json` has two seeds and one login. For each it gives the
backup code, the Schnorr secret key and x-only public key, the ML-DSA-44
`xi`, public key and its SHA-256, the `xi` of declaration keys 0 and 255 and
the SHA-256 of key 0's public key, and the three addresses. The login gives
the version 2 text, a test account (its secret key is SHA-256 of a fixed
text), the account's signature of the text as RFC 6979 makes it, the same
signature with a high `s`, which must give the same seed, and the wallet both
open.

## Limits

1. **One key of each kind, and no new addresses.** A seed gives one Schnorr
   key and one ML-DSA-44 key for spending. Every coin the wallet receives
   goes to one of the same three addresses, so its payments are linked on the
   chain, and a key that is broken or leaked opens every coin under it.
2. **The two keys are shared between addresses.** The ML-DSA-44 key is in
   the ML-DSA-44 address and in the mixed address; the Schnorr key is in the
   Schnorr address and in the mixed address's Schnorr leaf. A spend through a
   leaf that holds a key shows that key. Until a tripwire output is spent, a
   Schnorr signature alone also spends the mixed address. After that, M3 makes
   elliptic curve signature checks fail only in leaves of version `0xe0`, so
   the same Schnorr key still spends the Schnorr address. The mixed address
   has no keys of its own.
3. **A wallet opened by logging in has no post-quantum protection.** Its seed
   is made from the account's ECDSA signature. Every signature the account
   makes shows its public key, and once the account has sent a transaction on
   any chain, that key is public. Whoever can work out the private key from
   the public key can make the same signature again (the page accepted only a
   signature that came out the same twice, as deterministic RFC 6979 signing
   gives), and with it get every key of the wallet, the ML-DSA-44 and
   declaration keys too. Anyone who gets the signature itself, for example a
   site that has the account sign the same text, gets the seed too, without
   breaking any key. The page stopped offering the login on 2026-10-14.
   It marks a wallet it knows was opened this way. When a wallet was restored
   from a backup code, or kept from before the page recorded how it was
   opened, the page cannot tell; on that wallet's ML-DSA-44 and mixed address
   cards it says that a wallet first opened by logging in has no post-quantum
   protection.

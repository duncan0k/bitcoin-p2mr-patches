# m4-crosscheck

Known-answer check of the vendored SLH-DSA-SHA2-128s verifier (`src/crypto/slhdsa128s/` and the wrapper
`SLHDSA128SVerify`) on several architectures, compilers and word sizes, including a big-endian one. Every
run must print the same lines and the same digest.

## What it runs

`gen_vectors.py` reads a Bitcoin Core v31.1 tree with all patch sets applied and writes the vector list:

- the NIST ACVP SLH-DSA-SHA2-128s sigVer vectors of the tree (external pure interface and internal
  interface), with their expected results; their messages are not 32 bytes long, so they go through
  `slh_verify` with the same length checks as the wrapper except the one on the message;
- the SLH-DSA signatures of `src/test/data/p2mr_ts_m4_spend.json` over their signature hashes, with context
  string `P2MR/SLHDSA128S/EXP0`;
- the fixture of `src/test/util/slhdsa128s_fixture.h`.

The last two kinds go through `SLHDSA128SVerify` itself, compiled unmodified from `src/crypto/slhdsa128s.cpp`
(C++20) behind `shim.cpp`. `crosscheck.c` (C11, plus the vendored files and nothing else from Core) verifies
every vector, adds for each valid signature a copy with one bit flipped (expected invalid), and prints one
line `ID EXPECTED GOT` per vector, then `sha256 DIGEST vectors N mismatches M`; DIGEST covers all lines
before it. The exit status is 1 on any mismatch.

## Running

```bash
bash contrib/m4-crosscheck/run.sh [work-dir]
```

Without `CORE=<patched tree>` the script clones Bitcoin Core v31.1 into the work directory and applies the
patch sets in the order of the top-level `README.md`. Targets (`TARGETS=...` selects a subset): `x86_64-gcc`,
`x86_64-clang`, `i386-gcc` (`-m32`), `aarch64`, `armhf` and `s390x` (big-endian), the last three with
cross GCC under qemu-user. Each is built at `-O0` and `-O3`, plain and with
`-fsanitize=undefined -fno-sanitize-recover=all`. The vendored files are always compiled with
`-Wall -Wextra` and `-Wcast-align=strict` (`-Wcast-align` for clang).

The script prints one line per configuration (vectors, vendored-file warnings, UBSan reports, digest),
checks each digest against `sha256sum` of the printed lines, compares all runs with the first, and exits
non-zero if anything differs. A target whose tools are missing is reported as skipped. On Ubuntu 24.04:
`apt-get install gcc-multilib g++-multilib clang libclang-rt-18-dev gcc-aarch64-linux-gnu
g++-aarch64-linux-gnu gcc-arm-linux-gnueabihf g++-arm-linux-gnueabihf gcc-s390x-linux-gnu
g++-s390x-linux-gnu qemu-user`.

## BoringSSL as a third implementation

`run-boringssl.sh` builds `crosscheck.c` a second time with `-DM4CC_BORINGSSL`, so that it verifies with
BoringSSL instead of the vendored files, and compares the two programs line by line. The inputs, the
output format and the length checks before each call (key 32 bytes, signature 7856 bytes, context at most
255 bytes, and 32 bytes of message for the vectors that go through `SLHDSA128SVerify`) are the same.

- BoringSSL is fetched at a pinned commit: `437b75f34c3899230cb3f3fcf05543cb104faf2b`, committed
  2026-10-15, built as `libcrypto.a` in Release mode.
- External (pure) vectors go through `SLHDSA_SHA2_128S_verify` of `<openssl/slhdsa.h>`.
- Internal-interface vectors go through `BCM_slhdsa_sha2_128s_verify_internal`. It is declared in
  BoringSSL's non-public `crypto/fipsmodule/bcm_interface.h` (C++, hence `bssl_shim.cc`) and exported by
  `libcrypto.a`, so a build outside its tree can reach it.

Result on x86-64 (gcc, `-O2`) and on aarch64 (cross gcc, `-O2`, qemu-user): 50 vectors per run, all as
expected, with the same lines and the digest
`ccabc7121df9e4bf66393ecea2426b2b947c7824667b1cd17f80c16391862848` for BoringSSL and for the vendored
files, which is also the digest of every run of the matrix above.

| Interface | Vectors | Of which bit-flipped copies |
|---|---|---|
| ACVP external (pure), any message length | 16 | 2 |
| ACVP internal | 16 | 2 |
| `SLHDSA128SVerify`-shaped (p2mr-ts signatures, fixture) | 18 | 9 |

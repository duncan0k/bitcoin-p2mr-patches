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

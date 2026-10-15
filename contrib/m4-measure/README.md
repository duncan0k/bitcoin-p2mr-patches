# M4-S measurement kit (not part of the series)

What fixes `VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK` (C) of `OP_CHECKSLHDSA128S` before a release, per
`docs/m4/m4-design-2026-10-14.md` §2.2 and §10 "资源": the single-check condition

    C >= 1.25 x 50 x p99(t_full, worst-case SLH-DSA check) / median(t_full, Schnorr check)

with a SHA-256 that uses no SHA extensions, and then the whole-block test, which decides. The series
(`patches-m4/`) carries C = 2,000, a provisional value; `docs/m4/C1-REPORT.md` has the first single-check
numbers, from a noisy cloud VM, which do not fix it.

**Nothing here is part of the series, and nothing applies it**: not `apply.sh`, not `contrib/k3s`, not any
node build. The worst-case hook in the vendored `slh_dsa.c` only exists under `SLH_MEASURE_WORST`, and
every executable that links the node or consensus code would get it (`bitcoin-cli` too, through
`bitcoin_common`); so with that option the patch's CMake refuses to configure unless `bench_bitcoin` is the
only executable or shared library target. It checks the targets the configuration defines, at the end of
the top-level `CMakeLists.txt`, not a list of options, so a target added by a new option is caught too; and
`bitcoind.cpp` and `qt/main.cpp` (the sources of the four node and GUI executables) also fail with `#error`.
Its `bench_bitcoin.cpp` refuses to compile without the macro.

## Files

- `measurement-build.patch`, on top of the full series (v31.1 + `patches/` … `patches-m4/`):
  - a CMake option `SLH_MEASURE_WORST` (off by default) that defines the macro of the same name, and
    stops the configuration if it defines any executable or shared library target but `bench_bitcoin`;
  - in `src/crypto/slhdsa128s/slh_dsa.c`, under the macro, a switch `slh_measure_worst` that takes every
    WOTS+ message digit as zero, recomputes the checksum from that (510 chain steps per layer, 3,894
    SHA-256 compressions per check, the theoretical worst case) and judges the hypertree valid, so the
    benchmark's assertion holds; the vendored file is otherwise unchanged;
  - in `src/bench/bench_bitcoin.cpp`, the environment variables `BENCH_SHA256=standard|sse4|sse4_avx2`
    (Core's SHA-256 for the signature message and the cache entry, through
    `SHA256AutoDetect(sha256_implementation::...)`, never SHA-NI) and `SLH_WORST=1` (the switch above);
  - in `src/bitcoind.cpp` and `src/qt/main.cpp`, an `#error` under the macro.
- `build.sh <core-tree> <work-dir>`: a worktree of the patched tree with the patch applied, built with
  `-DSLH_MEASURE_WORST=ON` and every other executable turned off, target `bench_bitcoin` only; it stops if
  the cache shows a node or GUI target enabled.
- `measure.sh <bench_bitcoin> <out-dir> [rounds] [cpu]`: the runs of the single-check condition:
  `VerifyScriptP2MRSchnorr` and `VerifyScriptP2MRSLHDSA128S` (the whole path of a check as a block
  validates it, 10,000 single checks per run) for both SHA-256 implementations, real signatures and the
  worst case, pinned to one CPU; and the library-level `Verify*` benchmarks.
- `analyze.py <out-dir>`: medians, p99, the bound `62.5 x p99(SLH) / median(Schnorr)` of each run to six
  decimals, and the C it asks for, the bound's ceiling.

## How to use it

```bash
bash contrib/m4-measure/build.sh /path/to/patched/core /tmp/m4-measure
bash contrib/m4-measure/measure.sh /tmp/m4-measure/build/bin/bench_bitcoin /tmp/m4-out 3 2
python3 contrib/m4-measure/analyze.py /tmp/m4-out
```

Use a quiet machine without SHA extensions (check that `/proc/cpuinfo` has no `sha_ni`, or on ARM no
`sha2`), with frequency scaling fixed; the design asks for the worse of two machines and for both
single-threaded and `-par` runs. The whole-block test (blocks that use up their budget on SLH-DSA
checks, Schnorr checks and a mix, connected through `submitblock` by a fresh node with
`-maxsigcachesize=0`) is not scripted here yet.

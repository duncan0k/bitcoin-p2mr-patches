# M4-S measurement kit (not part of the series)

How `VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK` (C) of `OP_CHECKSLHDSA128S` is measured. Two tests:

- the single-check condition, over the whole path of a check as a block validates it, with a SHA-256
  that uses no SHA extensions:

      C >= 1.25 x 50 x p99(t_full, worst-case SLH-DSA check) / median(t_full, Schnorr check)

  where 50 is the validation weight of a Schnorr check (BIP 342) and 1.25 a margin;
- the whole-block test: a block that uses up its weight on SLH-DSA checks, each taking the slowest
  path of the verification, against one that uses it up on Schnorr checks, the most checks per unit of
  budget, connected by a node without a signature cache, single-threaded and with the default script
  threads. With C set right, the SLH-DSA block should not take longer.

The series (`patches-m4/`) sets C = 5,000. The single-check condition asked for 2,550 to 3,658 on a
cloud virtual machine (4 vCPU Intel Xeon at 2.8 GHz, no SHA extensions, GCC 13.3, three runs per
SHA-256 implementation of Core); 5,000 leaves a margin of at least 1.37 times for weaker hardware and
stays below 8,022, the budget of a single-signature spend.

**Nothing here is part of the series, and nothing applies it**: not `apply.sh`, not `contrib/k3s`, not any
node build. The worst-case hook in the vendored `slh_dsa.c` only exists under `SLH_MEASURE_WORST`, and
every executable that links the node or consensus code would get it (`bitcoin-cli` too, through
`bitcoin_common`). So with that option the patch's CMake refuses to configure if it defines any executable
or shared library target but `bench_bitcoin` and, with `SLH_MEASURE_NODE`, `bitcoind_m4_measure`, the
measurement-only node of the whole-block script. It checks the targets the configuration defines, not a
list of options, so a target added by a new option is caught too: the check is deferred to the end of the
top-level `CMakeLists.txt` and queues itself again until no other deferred call of that directory is
pending, so it runs last. `bitcoind_m4_measure` has no install rule, `cmake --install` of a measurement
build fails, and `bitcoind.cpp` and `qt/main.cpp` (the sources of the four node and GUI executables) fail
with `#error` for any other target. Its `bench_bitcoin.cpp` refuses to compile without the macro.

## Files

- `measurement-build.patch`, on top of the full series (v31.1 + `patches/` … `patches-m4/`):
  - CMake options `SLH_MEASURE_WORST` (off by default), which defines the macro of the same name, and
    `SLH_MEASURE_NODE`, which adds the target `bitcoind_m4_measure`, and the target check above;
  - in `src/crypto/slhdsa128s/slh_dsa.c`, under the macro, a switch `slh_measure_worst` that takes every
    WOTS+ message digit as zero, recomputes the checksum from that (510 chain steps per layer, 3,894
    SHA-256 compressions per check, the theoretical worst case) and judges the hypertree valid, so the
    benchmark's assertion holds; the vendored file is otherwise unchanged;
  - in `src/bench/bench_bitcoin.cpp`, the environment variables `BENCH_SHA256=standard|sse4|sse4_avx2`
    (Core's SHA-256 for the signature message and the cache entry, through
    `SHA256AutoDetect(sha256_implementation::...)`, never SHA-NI) and `SLH_WORST=1` (the switch above);
  - in `src/bitcoind.cpp`, for `bitcoind_m4_measure` only, `SLH_WORST=1` and a banner on standard error;
    for every other target, as in `src/qt/main.cpp`, an `#error` under the macro.
- `build.sh <core-tree> <work-dir>`: a worktree of the patched tree with the patch applied and two build
  directories: `build/` (`bench_bitcoin`) and `build-node/` (`bitcoind_m4_measure`, without wallet),
  each with every other executable turned off; it stops if a cache shows a node or GUI target enabled.
- `measure.sh <bench_bitcoin> <out-dir> [rounds] [cpu]`: the runs of the single-check condition:
  `VerifyScriptP2MRSchnorr` and `VerifyScriptP2MRSLHDSA128S` (the whole path of a check as a block
  validates it, 10,000 single checks per run) for both SHA-256 implementations, real signatures and the
  worst case, pinned to one CPU; and the library-level `Verify*` benchmarks.
- `analyze.py <out-dir>`: medians, p99, the bound `62.5 x p99(SLH) / median(Schnorr)` of each run to six
  decimals, and the C it asks for, the bound's ceiling.
- `wholeblock.py`: the whole-block test on regtest. It builds the chain in Python with the functional test
  framework of the patched tree: blocks paying to `OP_TRUE`, one funding many P2MR outputs, and three
  blocks of about 95 % of the maximum weight:
  - `slh`: inputs that each check one SLH-DSA signature 40 times (copies of its sixteen chunks taken with
    `OP_PICK`), with as much padding in the leaf as their validation budget needs; with the hook on,
    every check takes the worst-case path, and any signature of the right length passes, so one fixed
    signature serves every input;
  - `schnorr`: inputs that each check one real Schnorr signature 2,000 times (`OP_DUP <key>
    OP_CHECKSIGVERIFY`), padded to their budget;
  - `mixed`: about half the weight of each.

  For each block and each `-par` setting (`1` and the default), it starts fresh nodes
  (`bitcoind_m4_measure -regtest -maxsigcachesize=0 -debug=bench`, `SLH_WORST=1`), submits the chain and
  then the block with `submitblock`, and reads the time to connect the block and to verify its inputs
  from the `-debug=bench` log. It prints each run as JSON, writes `results.json`, and ends with the
  medians and the ratio of the SLH-DSA block to the Schnorr block for each `-par` setting.

## How to use it

```bash
bash contrib/m4-measure/build.sh /path/to/patched/core /tmp/m4-measure
# single-check condition
bash contrib/m4-measure/measure.sh /tmp/m4-measure/build/bin/bench_bitcoin /tmp/m4-out 3 2
python3 contrib/m4-measure/analyze.py /tmp/m4-out
# whole-block test
python3 contrib/m4-measure/wholeblock.py --core /tmp/m4-measure \
    --bitcoind /tmp/m4-measure/build-node/bin/bitcoind_m4_measure --workdir /tmp/m4-blocks --rounds 5
```

Use a quiet machine without SHA extensions (check that `/proc/cpuinfo` has no `sha_ni`, or on ARM no
`sha2`), with frequency scaling fixed, and take the worse of two machines. `wholeblock.py --help` lists its
options (checks per input, fill, rounds, which blocks and which `-par` settings).

# M4-S measurement kit (not part of the series)

How `VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK` (C) of `OP_CHECKSLHDSA128S` is measured. Two tests:

- the single-check condition, over the whole path of a check as a block validates it, with a SHA-256
  that uses no SHA extensions:

      C >= 1.25 x 50 x p99(t_full, worst-case SLH-DSA check) / median(t_full, Schnorr check)

  where 50 is the validation weight of a Schnorr check (BIP 342) and 1.25 a margin;
- the whole-block gate, which decides: blocks filled to the maximum weight with inputs that use up their
  validation budget on SLH-DSA checks, on ML-DSA-44, SLH-DSA and Schnorr checks together, and on SLH-DSA
  checks whose last signature is invalid, each connected no slower than the median of a block that uses
  its weight up on Schnorr checks, the most checks per unit of budget.

The series (`patches-m4/`) sets C = 5,000. Single-check results so far:

- a cloud virtual machine (4 vCPU Intel Xeon at 2.8 GHz, no SHA extensions, GCC 13.3, three runs per
  SHA-256 implementation of Core): 2,550 to 3,658. 5,000 is at least 1.36 times the highest;
- a second machine, an AMD EPYC (Zen 2) virtual server with 8 vCPUs: worst-path SLH-DSA checks take
  1.03–1.06 ms (median), Schnorr checks about 54 us. The raw worst-path p99 of about 10.7 ms comes from
  the host, which stalls for about 10 ms every 90 ms or so: that hits about 1.2 % of the 1-ms SLH-DSA
  samples, more than the 1 % tail, but only about 0.06 % of the 54-us Schnorr samples, so on such a
  host the p99/median condition overstates the longer check by construction (raw: 11,878 to 12,506).
  Without the stalled samples the condition asks for 1,682 to 1,953; checks on the release path ask for
  1,092 to 1,535. So the raw result there is dominated by host stalls, the stall-free result passes, and
  the whole-block gate is the deciding test. (`analyze.py --stall-factor` gives the stall-free figures;
  on the first machine they differ from the raw ones by at most 19 units, 8 for the worst-path
  runs, as its tail is not made of stalls.)

**Nothing here is part of the series, and nothing applies it**: not `apply.sh`, not `contrib/k3s`, not any
node build. With the patch applied and the measurement options off, every target builds exactly as without
it.

- **The hook lives in its own library.** The worst-case hook and the counters are only in
  `bitcoin_slhdsa128s_measure`, built from copies CMake generates in the build directory from the
  unmodified vendored files (the hook and a verification counter inserted into `slh_dsa.c` by exact
  replacements that must each match once; the library's own SHA-256 compression counter enabled for its
  copy of `sha2_256.c` only). The production `bitcoin_slhdsa128s` always compiles the vendored files as
  they are, so no compile flag puts a hook into it (`-DAPPEND_CFLAGS=-DSLH_MEASURE_WORST` without the
  CMake option builds a `bitcoind` with no hook symbol). Only the measurement configuration
  (`-DSLH_MEASURE_WORST=ON`) defines the measurement library and links it instead.
- **Only measurement executables.** With `SLH_MEASURE_WORST`, the patch's CMake refuses to configure if it
  defines any executable or shared library target but `bench_bitcoin` and, with `SLH_MEASURE_NODE`,
  `bitcoind_m4_measure`. It checks the targets the configuration defines, not a list of options, so a
  target added by a new option is caught too: the check is deferred to the end of the top-level
  `CMakeLists.txt` and queues itself again until no other deferred call of that directory is pending.
  `bitcoind.cpp` and `qt/main.cpp` (the sources of the four node and GUI executables) also fail with
  `#error` for any other target.
- **Nothing installs.** `cmake --install` of a measurement build fails for every component
  (`ALL_COMPONENTS`, so `--component bench_bitcoin` too), `bench_bitcoin` has no install rule there, and
  `bitcoind_m4_measure` has none at all.
- **The measurement node never runs on a real network.** `bitcoind_m4_measure`, once the configuration has
  chosen the chain, refuses anything but regtest (mainnet, testnet3, testnet4, the default signet and
  custom signets, Ark-0 included, all stop at startup), and runs without P2P: it does not listen, makes no
  outbound connections, keeps the network inactive with every proxy pointed at a closed port, and once
  started interrupts its connection manager, which then refuses every connection, `setnetworkactive true`
  with `addnode` included.

## Files

- `measurement-build.patch`, on top of the full series (v31.1 + `patches/` … `patches-m4/`):
  - CMake options `SLH_MEASURE_WORST` (off by default) and `SLH_MEASURE_NODE`, the target check above, the
    install refusal;
  - in `src/crypto/CMakeLists.txt`, the measurement library (generated sources, the same compile options
    as the production library), linked by `bitcoin_crypto` and `bench_bitcoin` in a measurement
    configuration only;
  - in `src/bench/bench_bitcoin.cpp`, the environment variables `BENCH_SHA256=standard|sse4|sse4_avx2`
    (Core's SHA-256 for the signature message and the cache entry, through
    `SHA256AutoDetect(sha256_implementation::...)`, never SHA-NI) and `SLH_WORST=1` (the hook);
  - in `src/bitcoind.cpp`, for `bitcoind_m4_measure` only: `SLH_WORST=1`, a banner on standard error, the
    regtest-only and no-P2P rules, and on exit the counters (complete SLH-DSA verifications, SHA-256
    compressions of the vendored library); for every other target, as in `src/qt/main.cpp`, an `#error`.
- `build.sh <core-tree> <work-dir>`: a worktree of the patched tree with the patch applied and two build
  directories: `build/` (`bench_bitcoin`) and `build-node/` (`bitcoind_m4_measure`, without wallet),
  each with every other executable turned off; it stops if a cache shows a node or GUI target enabled,
  and checks that the node refuses mainnet.
- `measure.sh <bench_bitcoin> <out-dir> [rounds] [cpu]`: the runs of the single-check condition:
  `VerifyScriptP2MRSchnorr` and `VerifyScriptP2MRSLHDSA128S` (the whole path of a check as a block
  validates it, 10,000 single checks per run) for both SHA-256 implementations, real signatures and the
  worst case, pinned to one CPU; and the library-level `Verify*` benchmarks.
- `analyze.py <out-dir> [--stall-factor F]`: medians, p99, the bound `62.5 x p99(SLH) / median(Schnorr)` of
  each run to six decimals, and the C it asks for, the bound's ceiling. With `--stall-factor F` it also
  reports each run without host stalls: a sample is a stall if it took more than F times the median of
  its own distribution (F = 5 suits these checks); it prints the share of stalls in each distribution and
  the bound from the p99 of the SLH-DSA samples that are not stalls.
- `wholeblock.py`: the whole-block gate, below.

## Whole-block gate (`wholeblock.py`)

It builds the chain in Python with the functional test framework of the patched tree (blocks paying to
`OP_TRUE`, one block funding an output for every input to come) and four blocks, each filled to the maximum
block weight: full inputs first, then smaller ones for the rest. It asserts that every block fills at
least 99.5 % of what its coinbase leaves, and that every input's budget (witness size + 50) is used up by
its checks to within one check's cost.

- `slh`: inputs that each check one valid SLH-DSA-SHA2-128s signature many times (copies of its sixteen
  chunks taken with `OP_PICK`; 100 checks for a full input), padded to their budget;
- `schnorr`: inputs that each check one Schnorr signature many times (`OP_DUP <key> OP_CHECKSIGVERIFY`,
  2,000 for a full input), the reference;
- `mixed`: a third of the weight each of SLH-DSA, ML-DSA-44 (copies of its five signature chunks and three
  key chunks, 1,000 checks for a full input) and Schnorr inputs;
- `slh-invalid-last`: `slh` with one byte of the last input's signature changed (its last chunk, in the
  last hypertree layer): the time to reject it.

Each block is connected by a fresh node (`-regtest -maxsigcachesize=0 -debug=bench`, no P2P) through
`submitblock`, never through the mempool, `--rounds` times for `-par=1` and for the default `-par`, in two
builds: the release build, a normal `bitcoind` built from the series (`--bitcoind-release`), with real
signatures and real verification; and the hook build, `bitcoind_m4_measure` with the hook on, where every
SLH-DSA check takes the slowest path (`slh-invalid-last` is measured in the release build only, as under
the hook every signature of the right length passes). Once per block, `bitcoind_m4_measure` with the hook
off (`-par=1`, not timed) counts the real path. The counters are plain counters, exact only when one
thread checks the scripts (with several, concurrent increments get lost, which atomic counters would
avoid at a cost to the very timing measured), so the counts and compressions per check come from `-par=1`
runs.

Connect time is the time `submitblock` takes, the one measure a rejected block has too; the `-debug=bench`
"Connect block" time of accepted blocks is reported next to it. Per build and `-par`, the script prints for
each block: weight, fill, inputs, checks planned per kind, SLH-DSA verifications counted (hook / real),
SHA-256 compressions per SLH-DSA check (hook / real: 3,894 / about 2,194 expected), connect time median and
maximum, and the ratio of the maximum to the median of the `schnorr` block of the same build and `-par`.
**The gate passes when every ratio is at most 1.** It asserts that the verifications counted equal the
checks planned (for `slh-invalid-last`, up to and including the failing one), writes every run to
`results.json`, and exits with 1 if the gate fails.

Regtest blocks carry no ML-DSA-44 block signature, which Ark-0 blocks need from height 20000. That
signature is one ML-DSA-44 verification per block, the same for every block kind, so it adds the same
constant to both sides of the inequality and cannot change the outcome.

## How to use it

On a quiet machine without SHA extensions (check that `/proc/cpuinfo` has no `sha_ni`, or on ARM no
`sha2`), with frequency scaling fixed:

```bash
# the patched tree, with a normal release build of the series for the release side
cmake -S /path/to/patched/core -B /path/to/patched/core/build -DCMAKE_BUILD_TYPE=Release
cmake --build /path/to/patched/core/build -j"$(nproc)" --target bitcoind
# the measurement builds (bench_bitcoin, bitcoind_m4_measure)
bash contrib/m4-measure/build.sh /path/to/patched/core /tmp/m4-measure
# single-check condition
bash contrib/m4-measure/measure.sh /tmp/m4-measure/build/bin/bench_bitcoin /tmp/m4-out 3 2
python3 contrib/m4-measure/analyze.py /tmp/m4-out --stall-factor 5
# whole-block gate
python3 contrib/m4-measure/wholeblock.py --core /tmp/m4-measure \
    --bitcoind-release /path/to/patched/core/build/bin/bitcoind \
    --bitcoind-measure /tmp/m4-measure/build-node/bin/bitcoind_m4_measure \
    --workdir /tmp/m4-blocks --rounds 10
```

Take the worse of two machines. `wholeblock.py --help` lists its options (checks per full input, rounds,
which blocks, builds and `-par` settings, the least fill).

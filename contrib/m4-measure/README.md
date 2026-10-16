# M4-S measurement kit (not part of the series)

How `VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK` (C) of `OP_CHECKSLHDSA128S` is measured. Two tests:

- the single-check condition, over the whole path of a check as a block validates it, with a SHA-256
  that uses no SHA extensions:

      C >= 1.25 x 50 x p99(t_full, worst-case SLH-DSA check) / median(t_full, Schnorr check)

  where 50 is the validation weight of a Schnorr check (BIP 342) and 1.25 a margin;
- the whole-block gate: blocks filled to the maximum weight with inputs that use up their validation
  budget on SLH-DSA checks, on ML-DSA-44, SLH-DSA and Schnorr checks together, and on SLH-DSA checks that
  end in an invalid signature, each connected no slower than the median of a block that uses its weight up
  on Schnorr checks, the most checks per unit of budget.

The series (`patches-m4/`) sets C = 5,000. Results so far:

- a cloud virtual machine (4 vCPU Intel Xeon at 2.8 GHz, no SHA extensions, GCC 13.3, three runs per
  SHA-256 implementation of Core): the single-check condition asks for 2,550 to 3,658. 5,000 is at least
  1.36 times the highest. One round with CPU times on the same machine had steal time in every run, so the
  CPU bound gates there: 2,999 to 3,092 on the worst path (wall bound 3,093 to 3,364), 1,768 to 2,145 on
  the release path. Its whole-block gate, over the whole matrix with two rounds, passes: the largest
  ratio is 0.778 (`mixed`, hook build, default `-par`);
- a second machine, an AMD EPYC-Rome (Zen 2) virtual server: `lscpu` shows 12 vCPUs, and the
  measurements ran in a container limited to 8 CPUs. Its CPUs have SHA extensions (`sha_ni`), which
  change neither side of the single-check condition: the vendored SLH-DSA library never uses them, and
  the measurement build of `bench_bitcoin` forces Core's SHA-256 to `standard` or `sse4_avx2`. In the
  whole-block gate the node's own SHA-256 does use them, which makes the Schnorr reference block faster
  and so the gate's ratios conservative.
  - Single-check condition, first runs (wall time only, no CPU times recorded): worst-path SLH-DSA
    checks take 1.03–1.06 ms (median), Schnorr checks about 54 us; the raw worst-path p99 is about
    10.7 ms, and the raw condition asks for 11,878 to 12,506 (release path: 1,092 to 1,535). About
    1.2 % of the worst-path SLH-DSA samples took more than 5 times their median, against about 0.07 %
    of the Schnorr samples. Those runs cannot tell whether the slow samples were preemption or slow
    checks; `analyze.py --filter-factor 5` gives 1,682 to 1,953 without them, a diagnostic figure that
    gates nothing. The runs with CPU times are below: [DE1]
  - Whole-block gate: [DE1]

**Nothing here is part of the series, and nothing applies it**: not `apply.sh`, not `contrib/k3s`, not any
node build. Every change the patch makes to a source file is under `SLH_MEASURE_WORST` or one of the other
measurement options, and every change to a CMake file takes effect only with them: with the patch applied
and the measurement options off, every target, `bench_bitcoin` included, builds from the same code and
links the same libraries as without the patch: the source files it touches preprocess to the same text
with the same flags, but for `__LINE__` values in `init.cpp` (lock names), which shift with the lines it
adds.

- **The hook lives in its own library.** The worst-case hook and the counters are only in
  `bitcoin_slhdsa128s_measure`, built from copies CMake generates in the build directory from the
  unmodified vendored files: the hook (and, in the counting build only, a verification counter) inserted
  into `slh_dsa.c` by exact replacements that must each match once; in the counting build only, the
  library's own SHA-256 compression counter enabled for its copy of `sha2_256.c`. The SHA-256 of the
  vendored `slh_dsa.c` and `sha2_256.c` is pinned in the patch: both files are configure dependencies
  (`CMAKE_CONFIGURE_DEPENDS`), so a change to either reconfigures the build, and configuration stops with
  an error if either no longer has its pinned hash, so that the hook is reviewed again against the new
  code. The production `bitcoin_slhdsa128s` always compiles the vendored files as they are, so no compile
  flag puts a hook into it (`-DAPPEND_CFLAGS=-DSLH_MEASURE_WORST` without the CMake option builds a
  `bitcoind` with no hook symbol). Only the measurement configuration (`-DSLH_MEASURE_WORST=ON`) defines
  the measurement library and links it instead.
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
  outbound connections, keeps the network inactive with every proxy pointed at a closed port, and
  interrupts its connection manager during startup, before RPC leaves its warm-up, so before any RPC
  call is served: from then on its network threads have stopped and it refuses every connection,
  `setnetworkactive true` with `addnode` included. (`SLH_MEASURE_TEST_DELAY_MS`, read by this node only,
  holds its startup for that many milliseconds right after RPC leaves its warm-up, so that a test can try
  to connect in that window.)
- **Counting and timing are separate builds.** The counters are plain variables, so they are compiled only
  into the counting build (`SLH_MEASURE_COUNT`), which forces `-par=1`: one thread checks scripts, nothing
  else writes the counters, and the counts are exact. That build is never timed. The timing builds
  (`bench_bitcoin`, and `bitcoind_m4_measure` without `SLH_MEASURE_COUNT`) have no counters.

## Files

- `measurement-build.patch`, on top of the full series (v31.1 + `patches/` … `patches-m4/`):
  - CMake options `SLH_MEASURE_WORST` (off by default), `SLH_MEASURE_NODE` and `SLH_MEASURE_COUNT`, the
    target check above, the install refusal;
  - in `src/crypto/CMakeLists.txt`, the measurement library (generated sources, pinned vendored hashes,
    the same compile options as the production library), linked by `bitcoin_crypto` and `bench_bitcoin`
    in a measurement configuration only;
  - in `src/bench/bench_bitcoin.cpp`, in a measurement configuration only, the environment variables
    `BENCH_SHA256=standard|sse4|sse4_avx2` (Core's SHA-256 for the signature message and the cache entry,
    through `SHA256AutoDetect(sha256_implementation::...)`, never SHA-NI) and `SLH_WORST=1` (the hook);
    otherwise `SHA256AutoDetect()` as without the patch;
  - in `src/bench/m4_samples.h` and the two whole-path benchmarks, in a measurement configuration only:
    with `SLH_SAMPLES_DIR` set, the wall time (`steady_clock`) and the thread CPU time
    (`CLOCK_THREAD_CPUTIME_ID`) of every single check, written to `<SLH_SAMPLES_DIR>/<benchmark>.csv`;
  - in `src/bitcoind.cpp` and `src/init.cpp`, for `bitcoind_m4_measure` only: `SLH_WORST=1`, a banner on
    standard error that names the build (counting or timing), the regtest-only and no-P2P rules, and in
    the counting build `-par=1` and, on exit, the counters (complete SLH-DSA verifications, SHA-256
    compressions of the vendored library); for every other target, as in `src/qt/main.cpp`, an `#error`.
- `build.sh <core-tree> <work-dir>`: a worktree of the patched tree with the patch applied and three build
  directories: `build/` (`bench_bitcoin`), `build-node/` (`bitcoind_m4_measure`, timing) and
  `build-count/` (`bitcoind_m4_measure`, counting), without wallet for the node, each with every other
  executable turned off; it stops if a cache shows a node or GUI target enabled, and checks that both
  nodes refuse mainnet.
- `measure.sh <bench_bitcoin> <out-dir> [rounds] [cpu]`: the runs of the single-check condition:
  `VerifyScriptP2MRSchnorr` and `VerifyScriptP2MRSLHDSA128S` (the whole path of a check as a block
  validates it, 10,000 single checks per run) for both SHA-256 implementations, real signatures and the
  worst case, pinned to one CPU, with every sample's wall and CPU time in `samples-<run>/`, and the
  `/proc/stat` CPU lines (steal time included) of all CPUs and of the pinned one, where readable, before
  and after each run in `stat-<run>.txt`; then the library-level `Verify*` benchmarks.
- `analyze.py <out-dir>`: per run, for Schnorr and SLH-DSA, the median and p99 of the wall times and of the
  CPU times, the preempted share of each distribution, the steal time during the run, and the bound
  `62.5 x p99(SLH) / median(Schnorr)` from the wall times and from the CPU times, to six decimals, with the
  C each asks for (its ceiling) and which of the two gates (below). With `--filter-factor F` it also
  reports each run filtered (diagnostic only), without the samples that took more than F times the median
  of their distribution; the filtered figures gate nothing. Runs without per-sample files are reported
  from the benchmark's JSON output, wall times only.
- `wholeblock.py`: the whole-block gate, below.
- `wots_bound.py`: the numerical check of the bound argument below.

## Which single-check bound gates

A sample is preempted if its wall time exceeds its thread CPU time by more than 1 ms, or is more than
twice it (`--preempt-ms`, `--preempt-ratio`): for part of the check, the thread did not run. The wall
bound gates a run where its samples show no preemption: at most 0.1 % (`--max-preempted`) of the samples
of each distribution preempted, and no steal time during the run. Otherwise the CPU bound gates, and it is
reported with the preempted shares and the steal time. Thread CPU time does not count the time the thread
waited for a CPU; whether it counts time the host took from the virtual CPU depends on the guest kernel
accounting steal time, which is why steal is recorded and a run with steal never lets the wall bound gate
by default. The raw samples are always kept, and the gating figure is always reported next to the raw wall
bound.

## Why the hook path is the worst case

In each of the 7 hypertree layers the verifier completes 35 WOTS+ chains (w = 16): 32 message chains, the
chain of digit m_i running 15 - m_i steps, and 3 checksum chains. The checksum x = sum(15 - m_i), with
0 <= x <= 480, is shifted left by 4 bits before it is split into base-16 digits, so its three digits c_j
are the hex digits of x, and the checksum chains run sum(15 - c_j) = 45 - ds(x) steps, ds(x) being the
sum of the hex digits of x. A layer thus runs

    steps(x) = x + 45 - ds(x)

chain steps. As x grows by 1, ds(x) grows by at most 1, so steps(x) never decreases as x grows, and its
maximum is steps(480) = 480 + 45 - (1 + 14 + 0) = 510, reached only at x = 480 (`wots_bound.py` checks
every x). Everything else a verification does, the message hash, FORS and the authentication paths, the
compression of each WOTS+ public key, is the same work for every signature. The hook takes every message
digit as zero, so x = 480 in every layer: its path, 3,894 SHA-256 compressions per verification as counted
(7 x 510 = 3,570 of them chain steps), bounds every signature. And a rejected block does at most the work
of the all-valid block with the same checks, since a check stops no later than its valid twin and the
block stops at its first failure: so the hook build's `slh` block bounds the invalid kinds, which differ
from it only in what the final comparison of the root finds.

## Whole-block gate (`wholeblock.py`)

It builds the chain in Python with the functional test framework of the patched tree (blocks paying to
`OP_TRUE`, one block funding an output for every input to come) and five blocks, each filled to the
maximum block weight: full inputs first, then smaller ones for the rest. It asserts that every block fills
at least 99.5 % of what its coinbase leaves, and that every input's budget (witness size + 50) is used up
by its checks to within one check's cost.

- `slh`: inputs that each check one valid SLH-DSA-SHA2-128s signature many times (copies of its sixteen
  chunks taken with `OP_PICK`; 100 checks for a full input), padded to their budget;
- `schnorr`: inputs that each check one Schnorr signature many times (`OP_DUP <key> OP_CHECKSIGVERIFY`,
  2,000 for a full input), the reference;
- `mixed`: a third of the weight each of SLH-DSA, ML-DSA-44 (copies of its five signature chunks and three
  key chunks, 1,000 checks for a full input) and Schnorr inputs;
- `slh-invalid-last`: `slh` with one byte of the last input's signature changed (its last chunk, in the
  last hypertree layer): the time to reject it;
- `slh-invalid-tail`: inputs that each carry a valid signature and an invalid one (the valid one with one
  byte of its last chunk changed), perform all their valid checks first and then, as the last check of the
  same script, one check of the invalid signature; 100 checks for a full input, 99 valid and the last one
  invalid. Whatever input a script thread takes first, it does all of that input's valid checks before it
  fails.

Each block is connected by a fresh node (`-regtest -maxsigcachesize=0 -debug=bench`, no P2P) through
`submitblock`, never through the mempool, `--rounds` times for `-par=1` and for the default `-par`, in two
builds: the release build, a normal `bitcoind` built from the series (`--bitcoind-release`), with real
signatures and real verification; and the hook build, the timing build of `bitcoind_m4_measure`
(`--bitcoind-measure`, `build-node/`) with the hook on, where every SLH-DSA check takes the slowest path
(the invalid kinds are measured in the release build only, as under the hook every signature of the right
length passes).

The counts come only from the counting build (`--bitcoind-count`, `build-count/`, one script thread, never
timed): once per block with the hook off, the real path, and once per valid block with the hook on. The
script asserts that the verifications counted equal the checks planned, for the invalid kinds up to and
including the failing check: for `slh-invalid-last` every check of the first seven inputs and the first
of the last, for `slh-invalid-tail` all the checks of its first input. It reports the SHA-256
compressions per SLH-DSA check (hook / real: 3,894 / about 2,200).

Connect time is the time `submitblock` takes, the one measure a rejected block has too; the `-debug=bench`
"Connect block" time of accepted blocks is reported next to it. Per build and `-par`, the script prints for
each block: weight, fill, inputs, checks planned per kind, SLH-DSA verifications counted (hook / real),
SHA-256 compressions per SLH-DSA check (hook / real), connect time median and maximum, and the ratio of the
maximum to the median of the `schnorr` block of the same build and `-par`. **The gate passes when every
ratio is at most 1 and the matrix is complete**: every block kind, both builds, both `-par` settings and
the reference, each with `--rounds` samples (at least 1). A run with fewer kinds, builds or `-par` settings
prints `INCOMPLETE` and exits with 2, as does `--rounds 0`; a failing ratio prints `FAIL` and exits with 1.
Every run goes to `results.json`.

Regtest blocks differ from Ark-0 blocks from height 20000 in what the block signature adds, and the gate
does not measure that. Bounded: one ML-DSA-44 verification (about 0.1 ms; `VerifyMLDSA44` in the
library-level benchmarks), one rebuild of the Merkle root without the signature (tens of hashes for blocks
of 9 to 41 transactions), and about 2.4 KB of non-witness data in the coinbase, which every block kind
would give up in weight alike (about 0.24 % of its checks). Together that is well under 1 ms per block,
against blocks that take 300 to 5,000 ms to connect.

## How to use it

On a quiet machine without SHA extensions if there is one (check `/proc/cpuinfo` for `sha_ni`, or on ARM
`sha2`; on a machine with them, see above what they change), with frequency scaling fixed:

```bash
# the patched tree, with a normal release build of the series for the release side
cmake -S /path/to/patched/core -B /path/to/patched/core/build -DCMAKE_BUILD_TYPE=Release
cmake --build /path/to/patched/core/build -j"$(nproc)" --target bitcoind
# the measurement builds (bench_bitcoin, bitcoind_m4_measure timing and counting)
bash contrib/m4-measure/build.sh /path/to/patched/core /tmp/m4-measure
# single-check condition, with CPU times and steal; keep the whole output directory
bash contrib/m4-measure/measure.sh /tmp/m4-measure/build/bin/bench_bitcoin /tmp/m4-out 3 2
python3 contrib/m4-measure/analyze.py /tmp/m4-out --filter-factor 5
python3 contrib/m4-measure/wots_bound.py
# whole-block gate
python3 contrib/m4-measure/wholeblock.py --core /tmp/m4-measure \
    --bitcoind-release /path/to/patched/core/build/bin/bitcoind \
    --bitcoind-measure /tmp/m4-measure/build-node/bin/bitcoind_m4_measure \
    --bitcoind-count /tmp/m4-measure/build-count/bin/bitcoind_m4_measure \
    --workdir /tmp/m4-blocks --rounds 10
```

In a container limited to fewer CPUs than the host has, pin `measure.sh` to a CPU the container may use
(its fourth argument). Take the worse of two machines. `wholeblock.py --help` lists its options (checks
per full input, rounds, which blocks, builds and `-par` settings, the least fill).

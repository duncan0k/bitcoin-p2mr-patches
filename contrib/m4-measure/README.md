# M4-S measurement kit (not part of the series)

How `VALIDATION_WEIGHT_PER_SLHDSA128S_CHECK` (C) of `OP_CHECKSLHDSA128S` is measured. Two tests:

- the single-check condition, over the whole path of a check as a block validates it, with a SHA-256
  that uses no SHA extensions:

      C >= 1.25 x 50 x p99(t_full, worst-case SLH-DSA check) / median(t_full, Schnorr check)

  where 50 is the validation weight of a Schnorr check (BIP 342) and 1.25 a margin;
- the whole-block gate: blocks filled to the maximum weight with inputs that use up their validation
  budget on SLH-DSA checks, on ML-DSA-44, SLH-DSA and Schnorr checks together, and on SLH-DSA checks that
  end in an invalid signature, spread over many inputs or all in one, each connected no slower than the
  median of a block of the same layout that uses its weight up on Schnorr checks, the most checks per unit
  of budget.

The series (`patches-m4/`) sets C = 5,000. Results so far:

- a cloud virtual machine (4 vCPUs, guest kernel with steal time accounting, GCC 13.3):
  - on an Intel Xeon at 2.8 GHz without SHA extensions, the single-check condition, first runs (three per
    SHA-256 implementation of Core, an earlier kit, wall time only): 2,550 to 3,658; 5,000 is at least
    1.36 times the highest. The whole-block gate there, the whole matrix with two rounds, passes: the
    largest ratio is 0.715 (`mixed`, hook build, default `-par`); the one-input SLH-DSA kinds take 0.167
    to 0.276 of `schnorr-one`;
  - with the current kit and patch 0078 (two rounds, continuous and paused), after the machine had moved
    to an Intel Xeon at 2.1 GHz with AVX-512 and SHA extensions: every run accepted, the largest CPU time
    over wall time -0.2 to -0.3 us; steal time on the pinned CPU in every run, so the CPU bound gates. The
    paused runs ask for 2,199 to 2,554 on the worst path and 1,620 to 1,623 on the release path with
    `standard`, and 2,185 to 2,296 and 956 to 1,688 with `sse4_avx2`; the continuous runs 2,048 to 2,769
    and 888 to 1,536. Library-level `VerifySLHDSA128S`: 0.48 to 0.59 ms with every setting. Without 0078
    the vendored library ran 2 to 2.9 times slower on that host in a process that had enabled Core's AVX2
    SHA-256: that code left the upper halves of the vector registers dirty, which slows the library's
    SSE code there. A node on that host uses the SHA extensions and never runs it, and the whole-block
    gate there (release `bitcoind`, two rounds) passes, each SLH-DSA block taking 0.89 to 1.30 times the
    share of its Schnorr block it took on the 2.8 GHz host.
- a second machine, an AMD EPYC-Rome (Zen 2) virtual server: `lscpu` shows 12 vCPUs, and the
  measurements ran in a container limited to 8 CPUs. Its CPUs have SHA extensions (`sha_ni`), which
  change neither side of the single-check condition: the vendored SLH-DSA library never uses them, and
  the measurement build of `bench_bitcoin` forces Core's SHA-256 to `standard` or `sse4_avx2`. In the
  whole-block gate the node's own SHA-256 does use them, which makes the Schnorr reference blocks faster
  and so the gate's ratios conservative.
  - Its guest kernel (6.12) has neither `CONFIG_PARAVIRT_TIME_ACCOUNTING` nor
    `CONFIG_IRQ_TIME_ACCOUNTING`: time the host takes is charged to the running thread, so thread CPU
    time cannot show it, and `/proc/stat` steal shows only a small part of it.
  - The host caps a busy vCPU: a loop of 1 ms iterations of integer work, pinned to one CPU, lost about
    10 ms in every 100 ms (slow iterations 98.7 to 101.4 ms apart, each about 9.7 ms longer in wall and
    in CPU time alike), on two different CPUs. The loss went away when the loop slept about a fifth of
    the time (2 ms per 8 ms, 5 ms per 20 ms), and came back, more rarely, with 50 ms stretches of work: a
    cap of about 90 % of each 100 ms per vCPU. The continuous and paused runs of `measure.sh` show the
    same with the kit's own code.
  - Single-check condition, an earlier run with continuous runs only (three per SHA-256 implementation,
    kit of round 6): slow samples every 99.5 to 99.8 ms, 9.7 to 10.2 ms longer, 0.0100 per ms of run for
    Schnorr, release-path and worst-path checks alike; the raw worst-path p99 of about 10.7 ms is the
    host's 10 ms, not the check, which does the same 3,894 compressions every time. Raw bounds 11,653 to
    12,701 on the worst path, wall and CPU alike (release path: 1,197 to 1,442).
  - Single-check condition with the current kit (three rounds, pinned to CPU 11, continuous and paused):
    every run accepted, the largest CPU time over wall time -0.7 us in all 24 runs. The paused runs,
    which gate, ask for 1,829 to 2,289 on the worst path and 1,008 to 1,173 on the release path (six
    runs each, the CPU bound gating in every run); the continuous runs, reported, 11,980 to 12,720 and
    1,060 to 1,598. Library medians over the six runs: `VerifySLHDSA128S` 570.8 to 678.8 us,
    `VerifyMLDSA44` 103.5 to 110.3 us, `VerifySchnorrBIP340` 54.4 to 62.2 us.
  - Whole-block gate (the kit of round 8, `--rounds 5`, the complete matrix of 26 cells): passes, the
    largest ratio 0.710 (hook build, default `-par`, `mixed`); counts as expected (`slh` and `slh-one`
    799 with the hook on and off, `slh-invalid-last` 701, `slh-invalid-tail` 100, `slh-invalid-tail-one`
    799; 3,894.0 compressions per check with the hook, 2,214.0 to 2,304.0 real).
- a third machine, a cloud virtual machine of the kind the first runs had (4 vCPUs, an Intel Xeon at
  2.8 GHz without SHA extensions, guest kernel 6.18, GCC 13.3), with all 78 patches: a node there uses
  Core's AVX2 8-way SHA-256 (`sse4(1way);sse41(4way);avx2(8way)`), the code patch 0078 changes.
  - Single-check condition (two rounds, pinned to CPU 2, continuous and paused): five measurements, the
    first two with the kit of round 9 and the last three with the current one, all five analysed with
    the current one. Four were rejected, each for one Schnorr sample with a thread CPU time of 0 (wall
    times 175 to 870 us); the rule was kept, so no run of a rejected measurement counts. In the accepted
    one the paused runs ask for 2,741 to 3,219 on the worst path (`standard` 2,741 and 3,219,
    `sse4_avx2` 2,960 and 2,922) and 1,774 to 1,946 on the release path, the continuous runs 2,062 to
    2,983 and 1,187 to 1,725; steal time showed on the pinned CPU in every run, so the CPU bound gates.
    5,000 is at least 1.55 times the highest. The other runs of the four rejected measurements ask for
    no more than 3,355 (paused) and 3,426 (continuous). Library medians: `VerifySLHDSA128S` 632.6 to
    832.4 us over the three settings, `VerifyMLDSA44` 105.4 to 135.1 us, `VerifySchnorrBIP340` 49.9 to
    58.4 us.
  - Whole-block gate (`--rounds 2`, the complete matrix of 26 cells): passes, the largest ratio 0.727
    (hook build, `-par=1`, `mixed`), 0.729 with the M2 estimate; the one-input SLH-DSA kinds take 0.196
    to 0.333 of `schnorr-one`.

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
- **Counting and timing are separate builds.** The measurement counters (complete verifications, SHA-256
  compressions) are plain variables, so they are compiled only into the counting build
  (`SLH_MEASURE_COUNT`), which forces `-par=1`: one thread checks scripts, nothing else writes them, and
  the counts are exact. The counting build's blocks go through the same submission as the others, but
  their times are discarded and never reach the gate. The timing builds (`bench_bitcoin`, and
  `bitcoind_m4_measure` without `SLH_MEASURE_COUNT`) have no measurement counters; the wrapper's own atomic
  count of library calls (`g_library_calls`), which the tests use, is in every build, production
  included.

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
    with `SLH_SAMPLES_DIR` set, the wall time (`CLOCK_MONOTONIC`) and the thread CPU time
    (`CLOCK_THREAD_CPUTIME_ID`) of every single check, written to `<SLH_SAMPLES_DIR>/<benchmark>.csv`, every
    clock read checked (a failed read stops the measurement with an error); and with `SLH_PAUSE=<B>,<P>`,
    a sleep of P ms whenever the timed checks since the last pause add up to B ms, outside every sample's
    timing;
  - in `src/bitcoind.cpp` and `src/init.cpp`, for `bitcoind_m4_measure` only: `SLH_WORST=1`, a banner on
    standard error that names the build (counting or timing), the regtest-only and no-P2P rules, and in
    the counting build `-par=1` and, on exit, the counters (complete SLH-DSA verifications, SHA-256
    compressions of the vendored library); for every other target, as in `src/qt/main.cpp`, an `#error`.
- `build.sh <core-tree> <work-dir>`: a worktree of the patched tree with the patch applied and three build
  directories: `build/` (`bench_bitcoin`), `build-node/` (`bitcoind_m4_measure`, timing) and
  `build-count/` (`bitcoind_m4_measure`, counting), without wallet for the node, each with every other
  executable turned off; it stops if a cache shows a node or GUI target enabled, and checks that both
  nodes refuse mainnet.
- `measure.sh <bench_bitcoin> <out-dir> [rounds] [cpu]` (Linux; rounds a whole number, default 3; the gate
  needs at least 2, and with 1 the script says before it starts that this is a trial the gate does not
  accept, writes that round count into `plan.txt`, and `analyze.py` says `INCOMPLETE`): the runs of the
  single-check condition:
  `VerifyScriptP2MRSchnorr` and `VerifyScriptP2MRSLHDSA128S` (the whole path of a check as a block
  validates it, 10,000 single checks per run) for both SHA-256 implementations, real signatures and the
  worst case, pinned to one CPU, each run twice, continuously (`cont`) and paused (`pause`, 5 ms per 20 ms
  of timed checks), with every sample's wall and CPU time in `samples-<run>/`, and the `/proc/stat` CPU
  lines (steal time included) of all CPUs and of the pinned one, where readable, before and after each run
  in `stat-<run>.txt`; then the library-level `Verify*` benchmarks.
- `analyze.py <out-dir> --rounds N`: per run, for Schnorr and SLH-DSA, the median and p99 of the wall times and of the
  CPU times, the preempted share of each distribution, the steal time during the run, the largest excess
  of a CPU time over its wall time, and the bound `62.5 x p99(SLH) / median(Schnorr)` from the wall times
  and from the CPU times, to six decimals, with the C each asks for (its ceiling) and which of the two
  gates (below); then the range of the gating C over the paused runs, which gate, and over the continuous
  runs, which are reported. It rejects a run, and exits with 1, if a wall sample is not positive, a CPU
  sample is not positive, or a CPU sample exceeds its wall time by more than 5 us plus 0.1 % of its wall
  time (`--cpu-tolerance-us`, `--cpu-tolerance-fraction`): a clock that cannot be trusted. The harness
  reads the wall clock first and last, so the CPU interval lies inside the wall interval and time charged
  to the thread between two reads shows in the wall time only, as preemption; a CPU time can then exceed
  its wall time only by the difference between the two clocks, and `CLOCK_MONOTONIC` is slewed by NTP by
  at most 500 ppm while the thread CPU clock is not: the default allows twice that slew and 5 us for the
  reads. The largest excess printed is a fraction of a microsecond below zero. With `--filter-factor F`
  it also reports each run filtered (diagnostic only), without the samples that took more than F times
  the median of their distribution; the filtered figures gate nothing.
  In gate mode (the default) the matrix is fixed in the code: both SHA-256 settings, both paths, both
  modes and rounds 1 to N of `--rounds N`, required, with N >= 2; nothing in the out-dir can make it
  smaller. Every run of it needs both per-sample CSV files with 10,000 samples each; missing or short
  input is found before any statistic of that run is computed, and a run with only its JSON output does
  not count. The `plan.txt` that `measure.sh` writes only cross-checks: if present, it must say exactly
  that matrix and N, each key once, and nothing else. Anything less prints `INCOMPLETE` and exits with 2,
  as `wholeblock.py` does; a clock fault still rejects the run (exit 1). `--summary-only` reads no plan,
  needs no `--rounds` (and ignores one given) and checks no matrix: it reports every run it finds, under
  its own file or directory name, from its per-sample files or, without them, from the benchmark's JSON
  output (wall times only; older output names its runs without `-cont-`/`-pause-`); a run whose files
  cannot be read is listed as unreadable (`SKIPPED`) and the others are still reported. Library-level
  results that cannot be read are said so and not used: an optional input (the library results, and in
  `--summary-only` a run's JSON) never changes the verdict, whatever fails while reading it. Each of the
  six options with a fractional value (`--preempt-ms`, `--preempt-ratio`, `--max-preempted`, the two
  CPU tolerances, `--filter-factor`) must be finite and in its range (the tolerances and preemption limits
  not negative, `--filter-factor` above 0), or it is a usage error before any input is read; `--rounds` is
  checked as above. Text taken from any input (names inside files, file names) is printed with backslash
  escapes where the output cannot encode it, so no write can fail. Its exit codes are the
  only three: 0, a
  complete, accepted gate (or a summary without a rejected run); 1, a run rejected for a clock fault; 2,
  `INCOMPLETE`, a wrong command line, or anything unexpected, which prints one line and never a
  traceback.
- `wholeblock.py`: the whole-block gate, below.
- `wots_bound.py`: the numerical check of the bound argument below.
- `m2_bound.py <out-dir of measure.sh> <results.json of wholeblock.py>`: the estimate, with a margin, of what
  regtest leaves out, below.

## Which single-check bound gates

`measure.sh` runs every run twice: continuously, and paused, sleeping 5 ms whenever the timed checks since
the last pause add up to 20 ms. **The paused runs gate**; the continuous runs are reported next to them.
A virtual CPU that is busy without a break can lose time to the host at a fixed period (the second machine
below loses about 10 ms in every 100 ms), and a short check meets such a loss about once per period, in
proportion to its length: a 1 ms check about once in 100, which puts its p99 on the host's 10 ms, while a
55 us Schnorr check is hardly ever hit. Idling about a fifth of the time stays clear of such a cap. Such
sustained-load effects (a host cap, thermal limits) slow SLH-DSA and Schnorr checks alike; the whole-block
gate runs without pauses and measures them on both sides of its comparison. The pause costs the gate
little: only the first sample after a pause can find a colder cache, 1 in 20 to 1 in 33 SLH-DSA samples,
which can only raise their p99, and about 1 in 360 Schnorr samples, which leaves the median alone.

Within a run, a sample is preempted if its wall time exceeds its thread CPU time by more than 1 ms, or is
more than twice it (`--preempt-ms`, `--preempt-ratio`): for part of the check, the thread did not run. The
CPU bound gates only on evidence of preemption: more than 0.1 % (`--max-preempted`) of either
distribution's samples preempted, or steal time on the pinned CPU during the run (on all CPUs if the run
was not pinned; steal on other CPUs only does not count). Otherwise the wall bound gates; where the run's
`/proc/stat` log is missing, cannot be read or lacks the pinned CPU's line, and no sample was preempted,
the wall bound gates and the run is marked `steal unknown`. Thread CPU time leaves out the time the thread waited for a CPU, but whether it leaves out
time the host took from the virtual CPU depends on the guest kernel: with steal time accounting
(`CONFIG_PARAVIRT_TIME_ACCOUNTING`) the host's time shows as steal; without it, it is charged to the
running thread and shows in neither. The raw samples are always kept, and the gating figure is always
reported next to the raw wall bound.

## What the hook path bounds: work

In each of the 7 hypertree layers the verifier completes 35 WOTS+ chains (w = 16): 32 message chains, the
chain of digit m_i running 15 - m_i steps, and 3 checksum chains. The checksum x = sum(15 - m_i), with
0 <= x <= 480, is shifted left by 4 bits before it is split into base-16 digits, so its three digits c_j
are the hex digits of x, and the checksum chains run sum(15 - c_j) = 45 - ds(x) steps, ds(x) being the
sum of the hex digits of x. A layer thus runs

    steps(x) = x + 45 - ds(x)

chain steps over its 35 chains. As x grows by 1, ds(x) grows by at most 1, so steps(x) never decreases as
x grows, and its maximum is steps(480) = 480 + 45 - (1 + 14 + 0) = 510, reached only at x = 480
(`wots_bound.py` checks every x). Everything else a verification does, the message hash, FORS and the
authentication paths, the compression of each WOTS+ public key, is the same work for every signature. The
hook takes every message digit as zero, so x = 480 in every layer: its path, 3,894 SHA-256 compressions per
verification as counted (7 x 510 = 3,570 of them chain steps), bounds the work of every check. And a
rejected block does at most the work of the all-valid block with the same checks, since a check stops no
later than its valid twin and the block stops at its first failure: the hook build's all-valid blocks
bound the total work of the invalid kinds of the same layout.

That is a bound on work, per check and per block. A block's wall time also depends on how its checks are
split among inputs: one input's script is one job of the check queue, run by one thread, so on a node that
checks in parallel the slowest layout puts every check in one input. The slowest Schnorr block for such a
node is one input too, so that is the reference for that layout, and the gate compares each layout with
the Schnorr block of the same layout: `slh-one` and `slh-invalid-tail-one` with `schnorr-one`, the spread
kinds with `schnorr`. On such a node a single-input SLH-DSA block can take longer to connect than the
spread `schnorr` block, which several threads check together. 799 worst-path checks in one thread take
about 1.1 s: on the first machine above (4 vCPUs) the hook build's `slh-one` block took up to 1,167 ms at
the default `-par`, within 1 % of the spread `schnorr` block's median of 1,183 ms, and 0.28 of the
`schnorr-one` block's 4,234 ms; on a machine with more cores, or with SHA extensions for Core's own
SHA-256, the spread `schnorr` block connects faster (about 900 ms on the second machine) and the
single-input SLH-DSA block takes longer than it: on the second machine, with the hook at the default
`-par`, `slh-one` took up to 1,088.5 ms, about 1.16 times the spread `schnorr` block's median of
937.2 ms, and 0.229 of the `schnorr-one` block's median of 4,753.4 ms. That is not the comparison:
the spread `schnorr` block is not the slowest Schnorr block such a node can be given, and the same layout
put to Schnorr checks takes longer still. The invalid kinds are compared within their own layout too.

One verification uses about 9 KB of stack: measured at 8,956 bytes, thread start and libc included, by a
verification of a valid and of a changed signature in a thread whose 64 KiB stack was filled with a
pattern; it passes in a 16 KiB stack too. GCC's `-fstack-usage` gives the vendored library's 57 functions
21,264 bytes of frames in all, a bound on any call chain since none of them recurses: GCC's call graph
(`-fcallgraph-info`) has no cycle, and the indirect calls go to the hash functions of `slh_sha2.c`, which
call only SHA-2 code.

## Whole-block gate (`wholeblock.py`)

It builds the chain in Python with the functional test framework of the patched tree (blocks paying to
`OP_TRUE`, one block funding an output for every input to come) and eight blocks, each filled to the
maximum block weight: full inputs first, then smaller ones for the rest, or a single input. It asserts that
every block fills at least 99.5 % of what its coinbase leaves, and that every input's budget (witness size
+ 50) is used up by its checks to within one check's cost. The sizes of a leaf and its spend are computed
from the lengths of the leaf's parts, so that a single input of about 80,000 checks is built only once.

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
  fails;
- `slh-one`, `slh-invalid-tail-one`, `schnorr-one`: the same as `slh`, `slh-invalid-tail` and `schnorr`
  with all of the block's checks in one input (799 SLH-DSA checks, 798 valid and the last invalid, about
  80,000 Schnorr checks), which one thread checks whatever `-par` is.

Each block is connected by a fresh node (`-regtest -maxsigcachesize=0 -debug=bench`, no P2P) through
`submitblock`, never through the mempool, `--rounds` times for `-par=1` and for the default `-par`, in two
builds: the release build, a normal `bitcoind` built from the series (`--bitcoind-release`), with real
signatures and real verification; and the hook build, the timing build of `bitcoind_m4_measure`
(`--bitcoind-measure`, `build-node/`) with the hook on, where every SLH-DSA check takes the slowest path:
`slh`, `schnorr`, `mixed`, `slh-one` and `schnorr-one` (the invalid kinds are measured in the release
build only, as under the hook every signature of the right length passes).

The counts come only from the counting build (`--bitcoind-count`, `build-count/`, one script thread, its
times discarded): once per block with the hook off, the real path, and once per hook-build kind with the
hook on. The script asserts that the verifications counted equal the checks planned, for the invalid kinds
up to and including the failing check: for `slh-invalid-last` every check of the first seven inputs and
the first of the last, for `slh-invalid-tail` all the checks of its first input, for
`slh-invalid-tail-one` all of its checks. It reports the SHA-256 compressions per SLH-DSA check (hook /
real: 3,894 / about 2,200).

Connect time is the time `submitblock` takes, the one measure a rejected block has too; the `-debug=bench`
"Connect block" time of accepted blocks is reported next to it. Per build and `-par`, the script prints for
each block: weight, fill, inputs, checks planned per kind, SLH-DSA verifications counted (hook / real),
SHA-256 compressions per SLH-DSA check (hook / real), connect time median and maximum, its reference
(`schnorr-one` for the one-input kinds, `schnorr` for the others) and the ratio of the maximum to the
reference's median, of the same build and `-par`. **The gate passes when every ratio is at most 1 and the
matrix is complete**: 26 cells (8 kinds in the release build and 5 in the hook build, each with both `-par`
settings), each with `--rounds` samples, at least 2. A run with fewer kinds, builds or `-par` settings
prints `INCOMPLETE` and exits with 2, as do `--rounds 0` and `--rounds 1` (a trial run); a failing ratio
prints `FAIL` and exits with 1. Every run goes to `results.json`.

## What regtest leaves out

An Ark-0 block differs from the gate's regtest blocks in what its signet block solution and, from height
20000, its ML-DSA-44 block signature add. `m2_bound.py` estimates that from measured figures with a
margin of 10. It is an estimate under stated assumptions (the model below of what regtest leaves out, and
a coinbase of at most 3,000 bytes), not a mathematical bound on time:

- one ML-DSA-44 verification, taken as the highest median of the library-level `VerifyMLDSA44` over the
  runs, and one signature check of the block solution, taken as the highest median of
  `VerifySchnorrBIP340` (libsecp256k1 verifies ECDSA and Schnorr signatures at about the same cost);
- the hashing: each of the two computes a modified Merkle root over the block's transactions, with the
  coinbase (assumed at most 3,000 bytes with the signature push) hashed again, plus a few tagged hashes:
  378 SHA-256 compressions for the largest block, 41 transactions, each taken as the time of a
  worst-path SLH-DSA check divided by its 3,894 compressions, which is more than a compression costs;
- about 2.4 KB of non-witness coinbase data (12,000 WU with the margin of 3,000 bytes), which leaves every
  block kind, the references included, about 0.3 % fewer checks.

The script adds the estimate to the maximum of every gated block kind, nothing to the references, and
shrinks the references by the coinbase's weight while leaving the SLH-DSA blocks as they are, and prints
the largest ratio. On the first machine: 10 x (0.116 ms + 0.048 ms + 378 x 0.28 us) = 2.7 ms per block,
and the largest ratio goes from 0.715 to 0.720. On the second machine: 10 x (0.110 ms + 0.062 ms + 378 x
0.28 us) = 2.8 ms per block, and the largest ratio goes from 0.710 to 0.715. On the third machine: 10 x
(0.111 ms + 0.053 ms + 378 x 0.32 us) = 2.9 ms per block, and the largest ratio goes from 0.727 to 0.729.

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
# (rounds: at least 2 for the gate; with 1 it says before it starts that this is a trial the gate does not
# accept, and analyze.py says INCOMPLETE)
bash contrib/m4-measure/measure.sh /tmp/m4-measure/build/bin/bench_bitcoin /tmp/m4-out 3 2
python3 contrib/m4-measure/analyze.py /tmp/m4-out --rounds 3 --filter-factor 5
python3 contrib/m4-measure/wots_bound.py
# whole-block gate
python3 contrib/m4-measure/wholeblock.py --core /tmp/m4-measure \
    --bitcoind-release /path/to/patched/core/build/bin/bitcoind \
    --bitcoind-measure /tmp/m4-measure/build-node/bin/bitcoind_m4_measure \
    --bitcoind-count /tmp/m4-measure/build-count/bin/bitcoind_m4_measure \
    --workdir /tmp/m4-blocks --rounds 10
# what regtest leaves out
python3 contrib/m4-measure/m2_bound.py /tmp/m4-out /tmp/m4-blocks/results.json
```

In a container limited to fewer CPUs than the host has, pin `measure.sh` to a CPU the container may use
(its fourth argument). Take the worse of two machines. `wholeblock.py --help` lists its options (checks
per full input, rounds, which blocks, builds and `-par` settings, the least fill).

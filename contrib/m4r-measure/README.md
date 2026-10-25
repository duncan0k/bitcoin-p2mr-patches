# m4r-measure: the gates of the M4-R validation weight

Measurement only, never part of a node. It measures, with two gates, the validation weight of a rescue
check, `VALIDATION_WEIGHT_PER_P2MR_RESCUE_CHECK` (C), as `doc/p2mr-rescue.md` of the patched tree
describes it ("Validation weight"). The series set C = 130,000 from them (patch 0090); at 100,000 the
whole-block gate failed by a hair. Both gates read C from the Core tree they are given, so they measure
the value the series has:

- the single-check gate, `C >= 1.5 x 50 x p99(rescue check) / median(Schnorr check)` (`measure.cpp`,
  `gate.py`);
- the whole-block gate (`wholeblock.py`, with the measurement build of `build.sh`): every kind of block
  filled to the block weight limit connects no slower than the Schnorr worst block of the same machine.

The figures in this README are **indicative**: they come from one cloud VM. The release figures come
from the operator's machines.

## Single-check gate

It covers three rescue inputs, as the design names them:

- `rescue-worst-invalid`: a valid proof with `chall3` set to `ff..ff`, the most XOR work a verification
  does (ark0-rescue SPEC.md section 11), which is then invalid;
- `rescue-msg-65535`: a valid proof over a 65,535-byte message, the library's limit (the lane only
  takes 96 bytes);
- `rescue-valid`: a valid proof over the 96-byte message of the vector `r1` of
  `src/test/data/p2mr_rescue_vectors.json`.

`measure.cpp` links the node's own static library (`build/src/rescue/cargo-target/<triple>/release/libvoleith.a`)
and the node's libsecp256k1. It times each sample with both clocks, `steady_clock` and the thread's
CPU time, run continuously and with a pause before every sample. `gate.py` runs rounds and prints the
requirement per input, clock and mode. As in the M4-S kit (`contrib/m4-measure/`), the paused runs
gate. Given the Core tree, `gate.py` checks that requirement against the tree's C: `PASS` (0), `FAIL`
(1), or `INCOMPLETE` (2) with the reason, when a round has samples of a thread CPU time of 0, a figure
that is not finite and positive (the Schnorr medians are the denominators), or anything fails. It also prints the peak resident set size of the process, an upper bound of the verifier's.

`build-portable.sh` makes a copy of the vendored crate whose `clmul::available()` returns false, so
that every GF(2^128) product takes the portable path, as on a CPU without PCLMULQDQ: the design's
"weak hardware" row.

```
B=<Core build dir>; S=<Core source dir>
python3 prepare.py $S $B/src/rescue/cargo-target-tools/*/release/voleith-prove /tmp/m4r-in
g++ -O2 -std=c++20 -I$S/src/rescue/voleith/include -I$S/src/secp256k1/include measure.cpp \
    $B/src/rescue/cargo-target/x86_64-unknown-linux-gnu/release/libvoleith.a $B/src/secp256k1/lib/libsecp256k1.a \
    -lpthread -ldl -o measure-node
python3 gate.py ./measure-node /tmp/m4r-in 3 300 20000 2000 $S    # checks against C of $S
bash build-portable.sh $S /tmp/m4r-portable     # then link measure.cpp against /tmp/m4r-portable/target/release/libvoleith.a
```

The whole path of a check (signature message, key relation, proof) is timed by
`bench_bitcoin -filter='.*P2MRRescue.*'` in the node's tree; the proof is nearly all of it.

## Whole-block gate

### What it runs

`wholeblock.py` builds, with the functional test framework of the Core tree, a regtest chain and these
blocks. Each is filled to the block weight limit by its actual weight.

| block | kind | content |
|---|---|---|
| `rescue` | 1 | rescue spends in template R1, one rescue check per input (rule R3 allows no more), as many as fit |
| `rescue-invalid-last` | 1, control | that block with the last input's proof the most expensive invalid one (`chall3 = ff..ff`), failing R7 |
| `mixed` | 2 | a quarter of the weight each of SLH-DSA-128s, ML-DSA-44 and R1 rescue inputs, the rest Schnorr inputs, every input using up its validation budget |
| `rescue-tail` | 3 (layout) | inputs whose 0xe0 leaf first makes as many Schnorr checks as the rest of the input's budget pays for and then its rescue check, as many as fit, the rest Schnorr inputs |
| `rescue-tail-invalid-last` | 3, release control | that block with the last input's rescue check failing R7, every other check valid |
| `schnorr` | reference | 40 inputs that use their whole budget on Schnorr checks, the most checks per unit of budget, filled down to the last few weight units |

**Why this reference.** The `schnorr` block has 79,827 checks in 40 inputs. One input could carry
79,980 (3,999,996 WU), and at the default `-par` that block would be far slower than the 40-input one,
since one input's checks run on one thread. The gate keeps the 40-input block as the reference anyway:
against the Schnorr worst block, a faster reference only raises every ratio, so the gate errs on the
strict side.

A new node connects each block with `-maxsigcachesize=0` through `submitblock`. The node has never seen
the block, and the block does not go through the mempool. Each block is connected at `-par=1` and at the
default `-par`, in three builds:

- **release**: a normal `bitcoind` built from the series; every block.
- **hook**: `bitcoind_m4r_measure` of `build.sh` with `M4R_WORST=1`; `rescue`, `rescue-tail`, `mixed`,
  `schnorr`.
- **portable**: the same with `M4R_PORTABLE=1` as well, every GF(2^128) product on the portable path,
  as on a CPU without PCLMULQDQ; `rescue`, `rescue-tail`, `mixed`, `schnorr`.

That makes 28 cells, each with `--rounds` new nodes (at least 2).

**Weak hardware.** On a CPU without PCLMULQDQ the hook build is portable already. The measurement node
reports three things apart: the forced switch, the CPU's own PCLMULQDQ, and whether the portable path is
in use. `results.json` records the CPU's capability (`cpu_pclmulqdq`). Such a machine runs the whole
matrix: its hook and portable builds are then both portable, and its release build too.

### Kind 3: the upper bound

Kind 3 is a block whose checks are all valid but the last input's rescue check, which fails R7. Its worst
case is the failure found as late and as expensively as possible. The kit bounds it as the M4-S kit
does, with a hook rather than a deferred-failure build, since the hook is simpler to get right.

With the hook on, every rescue proof the node verifies first has a copy of itself verified with
`chall3 = ff..ff`. That copy is the most expensive invalid proof for its statement and message: after
step 6 of the verifier the work is fixed, except for one conditional XOR per set bit of `chall3`. The
node then verifies the proof itself and keeps the proof's own result. So in the hook build:

- each rescue check does at least the work of any invalid proof that can reach R7 in its place, plus its
  own;
- the other checks (Schnorr, ML-DSA-44, SLH-DSA) are unchanged;
- validation stops at the first failing check, in the check queue and in the serial path alike, so a
  block with failing checks does no more than the work up to its first failure.

The all-valid `rescue-tail` block of the hook build therefore does at least the work of every block of
the same layout in which any rescue checks fail. The deferred-failure version of the design is one of
those: every check finishes R7, and the failures are collected only after all checks have run. The same
holds for `rescue` and `mixed`. The release blocks `rescue-invalid-last` and `rescue-tail-invalid-last`
are only a control: they show that the block is rejected for the proof (`Invalid rescue proof`), and
what that costs in a node without the hook.

### Counts

Before the timed runs, `bitcoind_m4r_measure` connects each block once at `-par=1` (one thread, in
order), with its counters on (`M4R_COUNT=1`): once with the hook off, and once with it on for the hook
blocks. Its counts of complete verifications (rescue proofs, hook proofs, Schnorr, ML-DSA-44, SLH-DSA)
must equal what the layout gives.

- An invalid block's failing check is its last, so at `-par=1` every check runs and the counts are exact
  for it too.
- **The counters cost nothing in the timed runs.** The measurement build's own counters (hook proofs,
  Schnorr, ML-DSA-44) count only while `M4R_COUNT=1`, which only the counting runs set. Otherwise each
  costs one predictable branch on a flag read once at startup, and no atomic operation. The series' own
  counters (rescue proofs, SLH-DSA library calls) count in every build, the release one included; they
  are still checked in the timed runs of the hook and portable builds, where the others must read 0.
- A count that differs stops the run (`INCOMPLETE`).

### The condition

For each build and `-par`, the largest connect time (wall time of `submitblock`) of every block may be
at most `--max-ratio` (1) times the median of the **release** build's `schnorr` block at the same `-par`:
the Schnorr worst block of a node of the series on this machine. A slower Schnorr block in the hook or
portable build therefore cannot lower their ratios. The median of each build's own `schnorr` block, and
the ratio to it, are reported in an extra column only. The hook and portable builds' own `schnorr`
blocks are reported, not gated.

| result | when | exit status |
|---|---|---|
| `PASS` | the whole matrix ran with at least 2 rounds per cell, every time, median, reference and ratio finite and positive, and every block is at most `--max-ratio` times the reference | 0 |
| `FAIL` | the measurement is complete, as for `PASS`, and some block is above `--max-ratio`; the figures of each failure are printed | 1 |
| `INCOMPLETE` | fewer cells or rounds ran; a time, median, reference or ratio is not finite and positive; or a run failed, start-up included (a wrong `--core`, a node that does not start or exits, a timeout, a count that differs, an exception). Its nodes are stopped, and the reason is printed | 2 |

Completeness comes first: a run without a valid measurement of every cell is `INCOMPLETE` even if some
cell is above the ratio (its `FAIL` lines are still printed).

Each node gets an RPC port that no other node of the run uses. A node is measured only if the RPC that
answers is the process just started (its log is in that node's own data directory).

`--max-ratio` below 1 only serves to see the failure branch fire. The figures are in `results.json` in
the work directory.

### Where the hook lives

The hook lives only in the measurement build. `measurement-build.patch` adds:
- the hook and the switch to the portable multiplication (`src/script/p2mr_rescue.cpp`, the measurement
  copy of the vendored crate's `clmul.rs` and `ffi.rs`, and the `--cfg` that compiles it in
  `src/rescue/CMakeLists.txt`);
- the counters (`pubkey.cpp`, `crypto/mldsa44.cpp`);
- the target `bitcoind_m4r_measure`.

`build.sh` applies the patch to a new worktree of the series and builds that target only. It refuses to
apply the patch if a file it changes no longer has its SHA-256 in `measurement-build.pins`.

The measurement configuration may define no other executable or shared library. It has no install
rule, and its node refuses every chain but regtest and runs without P2P; `build.sh` checks that it
refuses mainnet. `wholeblock.py` checks with `nm` that the release `bitcoind` holds no symbol of the
measurement build (`M4RMeasure`, `m4r_measure`, `voleith_m4r`) and that the measurement node does. It
first checks that each binary has a symbol table (`AppInitMain` and `VerifyP2MRRescueProof` present), so
that a stripped binary cannot pass. Patches 0079–0089 stay unchanged.

The Rust side of the patch (the portable switch in `clmul.rs` and `ffi.rs`) is compiled only with
`--cfg m4r_measure`, which the patch passes to cargo in the `M4R_MEASURE` configuration alone
(`src/rescue/CMakeLists.txt`). With the option off, the patched tree builds the series' library:
its code, data, relocations and symbols are identical to the series' `libvoleith.a`. Only the embedded
LLVM bitcode differs (`.llvmbc`, from the changed source text), a section marked to be excluded from
every link.

### How to run it

On a machine with the series built (`-DBUILD_TESTS=ON`, for the test-only prover `voleith-prove`):

```
S=<Core tree with patches 0001-0088>; W=<a new directory>
bash build.sh $S $W
python3 wholeblock.py --core $S --bitcoind-release $S/build/bin/bitcoind \
    --bitcoind-measure $W/build/bin/bitcoind_m4r_measure \
    --prover $S/build/src/rescue/cargo-target-tools/x86_64-unknown-linux-gnu/release/voleith-prove \
    --workdir <a new directory> --rounds 2
```

The whole matrix with two rounds took about 3 minutes on the VM below, after the build. To see the
failure branch, run it with `--max-ratio 0.5`: it prints `FAIL` lines and exits 1. It has to be the
whole matrix, since a partial run is `INCOMPLETE` whatever its ratios.

## Results (indicative)

These come from a cloud VM: Intel Xeon @ 2.80 GHz, 4 vCPUs, AVX-512, PCLMULQDQ, no SHA extensions;
GCC 13.3, kernel 6.18. The release figures come from the operator's machines, with this kit.

### At C = 130,000 (the final series, patches 0001–0090, Core tree `8dc2d002…`, from a clean clone)

**Single-check gate** (`gate.py`, 3 rounds, checked against the tree's C): **PASS**, exit 0. The
paused runs ask for C ≥ 83,641 at most, a margin of 1.554. The continuous runs ask for up to 98,760.

**Whole-block gate** (`--rounds 2`, the whole matrix of 28 cells): **PASS**, exit 0. Every count
equals the plan. An R1 input has 354 units of budget left over; a block still holds 30 of them. Kind 3's
inputs carry 28 Schnorr checks beside their rescue check: 30 rescue and 1,831 Schnorr checks in the
block. Each cell gives its ratio to the release `schnorr` median, and in brackets to the build's own:

| block | release `-par=1` | release default | hook `-par=1` | hook default | portable `-par=1` | portable default |
|---|---|---|---|---|---|---|
| `rescue` | 0.264 (0.264) | 0.385 (0.385) | 0.433 (0.456) | 0.497 (0.479) | 0.541 (0.540) | 0.611 (0.588) |
| `rescue-invalid-last` | 0.269 (0.269) | 0.351 (0.351) |  |  |  |  |
| `mixed` | 0.512 (0.512) | 0.620 (0.620) | 0.561 (0.591) | 0.613 (0.591) | 0.660 (0.658) | 0.607 (0.585) |
| `rescue-tail` | 0.306 (0.306) | 0.421 (0.421) | 0.468 (0.493) | 0.634 (0.611) | 0.546 (0.545) | 0.616 (0.594) |
| `rescue-tail-invalid-last` | 0.284 (0.284) | 0.395 (0.395) |  |  |  |  |
| `schnorr` | 1.015 (1.015) | 1.021 (1.021) | 0.969 (1.021) | 1.076 (1.037) | 1.003 (1.000) | 1.042 (1.003) |
| `schnorr` median, ms (own) | 4,314 | 1,280 | 4,094 | 1,328 | 4,328 | 1,329 |

The largest ratios, release / hook / portable:
- kind 1: 0.385 / 0.497 / 0.611;
- kind 2: 0.620 / 0.613 / 0.660;
- kind 3: 0.421 / 0.634 / 0.616.

The failure branch, the whole matrix with `--max-ratio 0.5`, printed `FAIL` for ten cells and exited 1.

### At C = 100,000 (superseded: the reason for patch 0090)

**Single-check gate** (3 rounds of 300 rescue and 20,000 Schnorr samples): rescue check median about
25 ms, Schnorr 37 to 44 µs. The paused runs, which gate, give C ≥ 96,579 at most (continuous up to
104,539); the portable multiplication gives 93,143 paused and 106,234 continuous. Peak RSS about 19 MB.

**Whole-block gate**, kit of review round 1 (`--rounds 2`, the whole matrix of 28 cells, from a fresh
clean clone; CPU with PCLMULQDQ): **FAIL**, exit 1. The blocks, by their actual weight:

| block | weight | inputs | rescue / Schnorr / ML-DSA-44 / SLH-DSA |
|---|---|---|---|
| `rescue`, `rescue-invalid-last` | 3,918,800 | 30 | 30 / 0 / 0 / 0 |
| `mixed` | 3,999,982 | 24 | 7 / 21,733 / 4,000 / 199 |
| `rescue-tail`, `rescue-tail-invalid-last` | 3,999,992 | 21 | 19 / 41,881 / 0 / 0 |
| `schnorr` | 3,999,990 | 40 | 0 / 79,827 / 0 / 0 |

Every count equals the plan; with the hook on, the hook proofs equal the rescue proofs.

Each cell gives its largest connect time over the median of the **release** `schnorr` block at the same
`-par`, which is the gate; in brackets, the ratio over the build's own `schnorr` median, which is
reported only:

| block | release `-par=1` | release default | hook `-par=1` | hook default | portable `-par=1` | portable default |
|---|---|---|---|---|---|---|
| `rescue` | 0.235 (0.235) | 0.365 (0.365) | 0.386 (0.423) | 0.594 (0.614) | 0.471 (0.497) | 0.637 (0.611) |
| `rescue-invalid-last` | 0.236 (0.236) | 0.340 (0.340) |  |  |  |  |
| `mixed` | 0.505 (0.505) | 0.647 (0.647) | 0.520 (0.569) | 0.738 (0.762) | 0.517 (0.545) | 0.696 (0.668) |
| `rescue-tail` | 0.654 (0.654) | **1.005** (1.005) | 0.753 (0.824) | 0.880 (0.909) | 0.801 (0.846) | **1.014** (0.972) |
| `rescue-tail-invalid-last` | 0.660 (0.660) | 0.498 (0.498) |  |  |  |  |
| `schnorr` | 1.022 (1.022) | 1.011 (1.011) | 0.927 (1.014) | 0.969 (1.001) | 0.985 (1.040) | 1.044 (1.001) |
| `schnorr` median, ms (own) | 4,710 | 1,386 | 4,305 | 1,342 | 4,462 | 1,445 |

Two cells fail, both the kind 3 layout at the default `-par`: release 1.005 (largest 1,393 ms against the
release Schnorr median of 1,386 ms) and portable 1.014. Every other cell passes.

The VM is noisy. In that same run the release `rescue-tail` block took 1,156 and 1,393 ms in its two
rounds, while the release `schnorr` reference took 1,402 and 1,370 ms. One repeat of the whole matrix
right after it, with the same binaries, is reported as a diagnostic, not in place of the result:
**PASS**, largest ratios kind 1 0.391 / 0.550 / 0.568, kind 2 0.884 / 0.591 / 0.569, kind 3 0.760 / 0.956 /
0.903 (release / hook / portable). Its release `schnorr` median at the default `-par` was 1,660 ms, against
1,386 ms in the first run. On this machine, kind 3 is within the noise of the Schnorr worst block at
C = 100,000.

The failure branch, run once with `--max-ratio 0.5` (release, default `-par`), printed `FAIL` for
`rescue-tail` (0.680), `rescue-tail-invalid-last` (0.621) and `mixed` (0.614), and exited 1.

The figures of the kit before review round 1 (26 cells, each build against its own `schnorr` block)
gave a PASS with a largest ratio of 0.938; they are superseded by these.

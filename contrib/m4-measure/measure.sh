#!/usr/bin/env bash
# Single-check measurement of the validation weight of OP_CHECKSLHDSA128S (README.md).
#
# Usage: bash measure.sh <bench_bitcoin of build.sh> <out-dir> [rounds] [cpu]
# For each of Core's SHA-256 implementations without SHA extensions (generic, SSE4+AVX2), and for
# real signatures and the worst-case hook, runs the whole-path benchmarks of a Schnorr and an SLH-DSA
# check, 10,000 single checks each, pinned to one CPU, twice: continuously, and paused (a 5 ms sleep
# whenever the timed checks since the last pause add up to 20 ms; the sleep is outside every sample's
# timing). Then the library-level benchmarks. Linux only (taskset, /proc/stat, the thread CPU clock).
# Every single check's wall time and thread CPU time go to samples-<run>/<benchmark>.csv, and the
# CPU counters of /proc/stat (steal time included) of all CPUs and of the pinned one, where readable,
# to stat-<run>.txt before and after the run. A run is named <sha>-w<hook>-<cont|pause>-r<round>.
set -euo pipefail
B="$1"; OUT="$2"; R="${3:-3}"; CPU="${4:-2}"
PAUSE="20,5"
[ "$R" -ge 1 ] || { echo "rounds must be at least 1" >&2; exit 2; }
mkdir -p "$OUT"
stat_lines() {
  # The aggregate line and the pinned CPU's line: user nice system idle iowait irq softirq steal ...
  if [ -r /proc/stat ]; then grep -E "^cpu  |^cpu${CPU} " /proc/stat || true; else echo "unreadable"; fi
}
for sha in standard sse4_avx2; do
  for r in $(seq 1 "$R"); do
    for worst in 1 0; do
      for mode in cont pause; do
        run="$sha-w$worst-$mode-r$r"
        mkdir -p "$OUT/samples-$run"
        { echo "pinned $CPU"; echo "before"; stat_lines; } > "$OUT/stat-$run.txt"
        pause=(); [ "$mode" = pause ] && pause=(SLH_PAUSE="$PAUSE")
        env BENCH_SHA256=$sha SLH_WORST=$worst SLH_SAMPLES_DIR="$OUT/samples-$run" "${pause[@]}" taskset -c "$CPU" "$B" \
          -filter='VerifyScriptP2MR(Schnorr|SLHDSA128S)' -output-json="$OUT/full-$run.json" > "$OUT/full-$run.txt" 2>&1
        { echo "after"; stat_lines; } >> "$OUT/stat-$run.txt"
      done
    done
    BENCH_SHA256=$sha taskset -c "$CPU" "$B" -filter='Verify(SLHDSA128S|SchnorrBIP340|MLDSA44)' -min-time=3000 \
      -output-json="$OUT/lib-$sha-r$r.json" > "$OUT/lib-$sha-r$r.txt" 2>&1
  done
done

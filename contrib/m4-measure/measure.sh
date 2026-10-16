#!/usr/bin/env bash
# Single-check measurement of the validation weight of OP_CHECKSLHDSA128S (README.md).
#
# Usage: bash measure.sh <bench_bitcoin of build.sh> <out-dir> [rounds] [cpu]
# For each of Core's SHA-256 implementations without SHA extensions (generic, SSE4+AVX2), and for
# real signatures and the worst-case hook, runs the whole-path benchmarks of a Schnorr and an SLH-DSA
# check, 10,000 single checks each, pinned to one CPU; then the library-level benchmarks.
# Every single check's wall time and thread CPU time go to samples-<run>/<benchmark>.csv, and the
# host's CPU counters (/proc/stat, steal time included), where readable, to stat-<run>.txt before and
# after the run.
set -euo pipefail
B="$1"; OUT="$2"; R="${3:-3}"; CPU="${4:-2}"
[ "$R" -ge 1 ] || { echo "rounds must be at least 1" >&2; exit 2; }
mkdir -p "$OUT"
stat_lines() {
  # The aggregate line and the pinned CPU's line: user nice system idle iowait irq softirq steal ...
  if [ -r /proc/stat ]; then grep -E "^cpu  |^cpu${CPU} " /proc/stat || true; else echo "unreadable"; fi
}
for sha in standard sse4_avx2; do
  for r in $(seq 1 "$R"); do
    for worst in 1 0; do
      run="$sha-w$worst-r$r"
      mkdir -p "$OUT/samples-$run"
      { echo "before"; stat_lines; } > "$OUT/stat-$run.txt"
      BENCH_SHA256=$sha SLH_WORST=$worst SLH_SAMPLES_DIR="$OUT/samples-$run" taskset -c "$CPU" "$B" \
        -filter='VerifyScriptP2MR(Schnorr|SLHDSA128S)' -output-json="$OUT/full-$run.json" > "$OUT/full-$run.txt" 2>&1
      { echo "after"; stat_lines; } >> "$OUT/stat-$run.txt"
    done
    BENCH_SHA256=$sha taskset -c "$CPU" "$B" -filter='Verify(SLHDSA128S|SchnorrBIP340|MLDSA44)' -min-time=3000 \
      -output-json="$OUT/lib-$sha-r$r.json" > "$OUT/lib-$sha-r$r.txt" 2>&1
  done
done

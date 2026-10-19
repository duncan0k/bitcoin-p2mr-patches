#!/usr/bin/env bash
# Single-check measurement of the validation weight of OP_CHECKSLHDSA128S (README.md).
#
# Usage: bash measure.sh <bench_bitcoin of build.sh> <out-dir> [rounds] [cpu]
# Then: python3 analyze.py <out-dir> --rounds <rounds>. The gate needs at least 2 rounds; with 1 the script
# says so before it starts, runs it as a trial, and analyze.py says INCOMPLETE.
# For each of Core's SHA-256 implementations without SHA extensions (generic, SSE4+AVX2), and for
# real signatures and the worst-case hook, runs the whole-path benchmarks of a Schnorr and an SLH-DSA
# check, 10,000 single checks each, pinned to one CPU, twice: continuously, and paused (a 5 ms sleep
# whenever the timed checks since the last pause add up to 20 ms; the sleep is outside every sample's
# timing). Then the library-level benchmarks. Linux only (taskset, /proc/stat, the thread CPU clock).
# Every single check's wall time and thread CPU time go to samples-<run>/<benchmark>.csv, and the
# CPU counters of /proc/stat (steal time included) of all CPUs and of the pinned one, where readable,
# to stat-<run>.txt before and after the run. A run is named <sha>-w<hook>-<cont|pause>-r<round>. The plan
# (SHA-256 settings, paths, modes, rounds, samples) goes to plan.txt first.
set -euo pipefail
B="$1"; OUT="$2"; R="${3:-3}"; CPU="${4:-2}"
PAUSE="20,5"
[[ "$R" =~ ^[0-9]+$ ]] || { echo "rounds must be a whole number, not '$R'" >&2; exit 2; }
R=$((10#$R))  # one decimal form for the loops and the plan: 02 is 2
[ "$R" -ge 1 ] || { echo "rounds must be at least 1" >&2; exit 2; }
if [ "$R" -lt 2 ]; then
  echo "note: $R round is a trial: the gate does not accept it (it needs 2 rounds or more); analyze.py will say INCOMPLETE" >&2
fi
mkdir -p "$OUT"
# The plan analyze.py checks the output against (gate mode): what this script is about to run.
printf 'sha standard sse4_avx2\npaths 0 1\nmodes cont pause\nrounds %s\nsamples 10000\n' "$R" > "$OUT/plan.txt"
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

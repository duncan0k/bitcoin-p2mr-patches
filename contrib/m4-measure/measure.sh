#!/usr/bin/env bash
# Single-check measurement of the validation weight of OP_CHECKSLHDSA128S (README.md).
#
# Usage: bash measure.sh <bench_bitcoin of build.sh> <out-dir> [rounds] [cpu]
# For each of Core's SHA-256 implementations without SHA extensions (generic, SSE4+AVX2), and for
# real signatures and the worst-case hook, runs the whole-path benchmarks of a Schnorr and an SLH-DSA
# check, 10,000 single checks each, pinned to one CPU; then the library-level benchmarks.
set -euo pipefail
B="$1"; OUT="$2"; R="${3:-3}"; CPU="${4:-2}"
mkdir -p "$OUT"
for sha in standard sse4_avx2; do
  for r in $(seq 1 "$R"); do
    for worst in 1 0; do
      BENCH_SHA256=$sha SLH_WORST=$worst taskset -c "$CPU" "$B" -filter='VerifyScriptP2MR(Schnorr|SLHDSA128S)' \
        -output-json="$OUT/full-$sha-w$worst-r$r.json" > "$OUT/full-$sha-w$worst-r$r.txt" 2>&1
    done
    BENCH_SHA256=$sha taskset -c "$CPU" "$B" -filter='Verify(SLHDSA128S|SchnorrBIP340|MLDSA44)' -min-time=3000 \
      -output-json="$OUT/lib-$sha-r$r.json" > "$OUT/lib-$sha-r$r.txt" 2>&1
  done
done

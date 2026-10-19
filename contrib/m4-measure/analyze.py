#!/usr/bin/env python3
"""Summarize the output of measure.sh: per run, for the whole-path Schnorr and SLH-DSA checks, the median
and p99 of their wall time and of their thread CPU time, the share of preempted samples, the host's steal
time, and the bound 1.25 x 50 x p99(SLH-DSA) / median(Schnorr) from the wall times and from the CPU times,
unrounded, with C_needed, the smallest integer C at or above it (its ceiling).

Usage: analyze.py <out-dir> [--preempt-ms T] [--preempt-ratio R] [--max-preempted P] [--cpu-tolerance-us U]
                            [--cpu-tolerance-fraction F]
                            [--filter-factor F] [--samples N] [--summary-only]

Runs are continuous or paused (measure.sh runs each both ways). The paused runs gate; the continuous ones
are reported next to them (README.md, "Which single-check bound gates").

By default the script checks that the output is complete: for both SHA-256 settings (standard, sse4_avx2)
and both paths (release, worst), a continuous and a paused run for every round from 1 to the highest one
found, each with N samples (default 10,000) of both checks. Anything less prints INCOMPLETE and exits
with 2; a rejected run exits with 1. --summary-only skips that check (exit 0 unless a run is rejected).

A sample is preempted if its wall time exceeds its thread CPU time by more than T ms (default 1) or is
more than R times it (default 2). In each run, the CPU bound gates only on evidence of preemption: more
than P (default 0.1 %) of either distribution's samples preempted, or steal time on the pinned CPU during
the run (on all CPUs if the run was not pinned); steal on other CPUs only does not count. Otherwise the
wall bound gates, and the run is marked "steal unknown" if its /proc/stat log is missing, cannot be read,
or lacks the pinned CPU's line.

A run is rejected, and the script exits with 1, if any sample has a wall time that is not positive, a CPU
time that is not positive, or a CPU time that exceeds its wall time by more than U microseconds plus F of
its wall time (defaults 5 us and 0.001). The harness reads the clocks so that the CPU interval lies inside
the wall interval, so a CPU time can exceed its wall time only by the difference between the two clocks:
CLOCK_MONOTONIC is slewed by NTP by at most 500 ppm and the thread CPU clock is not, so the default allows
twice that slew and 5 us for the reads; anything beyond is a clock that cannot be trusted. Each run's
largest excess is printed; with the nested reads it is a fraction of a microsecond below zero.

Runs without per-sample files (older output) are reported from the -output-json files, wall times only.

With --filter-factor F, each run is also reported filtered (diagnostic only): without the samples that
took more than F times the median of their own distribution. The filtered figures never gate."""
import argparse, csv, glob, json, math, os, re, statistics, sys

ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
ap.add_argument("out")
ap.add_argument("--preempt-ms", type=float, default=1.0)
ap.add_argument("--preempt-ratio", type=float, default=2.0)
ap.add_argument("--max-preempted", type=float, default=0.001, help="largest preempted share for the wall bound to gate")
ap.add_argument("--cpu-tolerance-us", type=float, default=5.0,
                help="absolute part of the largest CPU time over wall time accepted, in us (default 5: the cost of the reads)")
ap.add_argument("--cpu-tolerance-fraction", type=float, default=0.001,
                help="part of the wall time added to it (default 0.001: twice the 500 ppm NTP may slew CLOCK_MONOTONIC by)")
ap.add_argument("--filter-factor", type=float, default=None)
ap.add_argument("--samples", type=int, default=10000, help="samples of each check a complete run has (default 10000)")
ap.add_argument("--summary-only", action="store_true", help="skip the completeness check")
args = ap.parse_args()


def dist_json(fn, name):
    d = json.load(open(fn))
    for r in d['results']:
        if r['name'] == name:
            return [(m['elapsed'] / m['iterations'], None) for m in r['measurements']]
    return None


def dist_csv(path):
    """(wall, cpu) of every sample in seconds."""
    with open(path) as f:
        return [(int(r['wall_ns']) * 1e-9, int(r['cpu_ns']) * 1e-9) for r in csv.DictReader(f)]


def q(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(round(p * (len(xs) - 1))))]


def preempted(samples):
    return sum(1 for w, c in samples if w - c > args.preempt_ms * 1e-3 or w > args.preempt_ratio * c) / len(samples)


def invalid_wall(samples):
    """Why a run's wall samples cannot be used, or None."""
    bad = sum(1 for w, _ in samples if w <= 0)
    return f"{bad} wall samples not positive (a clock that cannot be trusted)" if bad else None


def invalid_cpu(samples):
    """Why a run's CPU samples cannot be used, or None."""
    bad_sign = sum(1 for _, c in samples if c <= 0)
    bad_excess = sum(1 for w, c in samples if c - w > args.cpu_tolerance_us * 1e-6 + args.cpu_tolerance_fraction * w)
    if bad_sign or bad_excess:
        return (f"{bad_sign} CPU samples not positive, {bad_excess} above wall time by more than "
                f"{args.cpu_tolerance_us:g} us + {args.cpu_tolerance_fraction:g} of the wall time "
                "(the CPU interval lies inside the wall interval: a clock that cannot be trusted)")
    return None


def steal(fn):
    """Steal time during the run, in clock ticks (USER_HZ, usually 1/100 s): (all CPUs, pinned CPU or None,
    pinned CPU number or None); None, steal unknown, if the log is missing or cannot be read or parsed, or
    lacks the aggregate line or the pinned CPU's line before or after the run."""
    try:
        lines = open(fn).read().split('\n')
        pinned = next((l.split()[1] for l in lines if l.startswith('pinned ')), None)
        b, a = lines.index('before'), lines.index('after')
        before = {l.split()[0]: int(l.split()[8]) for l in lines[b + 1:a] if l.startswith('cpu')}
        after = {l.split()[0]: int(l.split()[8]) for l in lines[a + 1:] if l.startswith('cpu')}
    except (OSError, ValueError, IndexError):
        return None
    needed = ['cpu'] + ([f'cpu{pinned}'] if pinned is not None else [])
    if any(k not in before or k not in after for k in needed):
        return None
    return (after['cpu'] - before['cpu'], after[f'cpu{pinned}'] - before[f'cpu{pinned}'] if pinned is not None else None,
            pinned)


header = ('sha impl | SLH path | mode | run | n | Schnorr wall median us | p99 | Schnorr CPU median us | p99 | '
          'SLH wall median us | p99 | SLH CPU median us | p99 | preempted Schnorr | preempted SLH | '
          'steal ticks (all, pinned CPU) | max CPU over wall us | wall bound | C_needed (wall) | CPU bound | C_needed (CPU) | gates')
if args.filter_factor:
    header += f' | filtered (diagnostic only, > {args.filter_factor:g} x median dropped): SLH wall p99 us | bound | C_needed'
print(header)
rejected = []
gating = {}  # (mode, path) -> list of C_needed of the gating bound
seen = {}  # (sha, path digit, mode, round) -> (Schnorr samples, SLH-DSA samples)
for fn in sorted(glob.glob(f'{args.out}/full-*.json')):
    run = os.path.basename(fn)[len('full-'):-len('.json')]
    m = re.fullmatch(r'(standard|sse4_avx2)-w([01])(?:-(cont|pause))?-r(\d+)', run)
    if not m:
        continue
    sha, w, mode = m.group(1), 'worst' if m.group(2) == '1' else 'release', m.group(3) or 'cont'
    sdir = f'{args.out}/samples-{run}'
    if os.path.exists(f'{sdir}/VerifyScriptP2MRSchnorr.csv'):
        s = dist_csv(f'{sdir}/VerifyScriptP2MRSchnorr.csv')
        h = dist_csv(f'{sdir}/VerifyScriptP2MRSLHDSA128S.csv')
    elif mode == 'pause':
        rejected.append(f'{run}: paused run without per-sample files')
        continue
    else:
        s = dist_json(fn, 'VerifyScriptP2MRSchnorr')
        h = dist_json(fn, 'VerifyScriptP2MRSLHDSA128S')
    if s is None or h is None:
        rejected.append(f'{run}: a benchmark is missing')
        continue
    seen[(sha, m.group(2), mode, int(m.group(4)))] = (len(s), len(h))
    have_cpu = s[0][1] is not None
    why = invalid_wall(s) or invalid_wall(h)
    if why:
        rejected.append(f'{run}: {why}')
        continue
    if have_cpu:
        why = invalid_cpu(s) or invalid_cpu(h)
        if why:
            rejected.append(f'{run}: {why}')
            continue
    sw, hw = [x for x, _ in s], [x for x, _ in h]
    wall_bound = 62.5 * q(hw, 0.99) / statistics.median(sw)
    cols = [sha, w, mode, run, str(len(h)), '%.1f' % (statistics.median(sw) * 1e6), '%.1f' % (q(sw, 0.99) * 1e6)]
    st = steal(f'{args.out}/stat-{run}.txt')
    if have_cpu:
        sc, hc = [c for _, c in s], [c for _, c in h]
        cpu_bound = 62.5 * q(hc, 0.99) / statistics.median(sc)
        ps, ph = preempted(s), preempted(h)
        if st is None:
            steal_seen, steal_text = False, 'unknown'
        else:
            all_cpus, pinned_cpu, pinned = st
            steal_seen = (pinned_cpu if pinned is not None else all_cpus) > 0
            steal_text = f"cpu {all_cpus}" + (f", cpu{pinned} {pinned_cpu}" if pinned is not None else ", not pinned")
        use_cpu = ps > args.max_preempted or ph > args.max_preempted or steal_seen
        gate = 'CPU' if use_cpu else ('wall, steal unknown' if st is None else 'wall')
        needed = math.ceil(cpu_bound if use_cpu else wall_bound)
        cols += ['%.1f' % (statistics.median(sc) * 1e6), '%.1f' % (q(sc, 0.99) * 1e6),
                 '%.1f' % (statistics.median(hw) * 1e6), '%.1f' % (q(hw, 0.99) * 1e6),
                 '%.1f' % (statistics.median(hc) * 1e6), '%.1f' % (q(hc, 0.99) * 1e6), '%.3f%%' % (ps * 100),
                 '%.3f%%' % (ph * 100), steal_text,
                 '%.1f' % (max(c - w for w, c in s + h) * 1e6), '%.6f' % wall_bound, str(math.ceil(wall_bound)),
                 '%.6f' % cpu_bound, str(math.ceil(cpu_bound)), gate]
    else:
        needed = math.ceil(wall_bound)
        cols += ['-', '-', '%.1f' % (statistics.median(hw) * 1e6), '%.1f' % (q(hw, 0.99) * 1e6), '-', '-', '-', '-',
                 '-' if st is None else f"cpu {st[0]}", '-', '%.6f' % wall_bound, str(math.ceil(wall_bound)), '-', '-',
                 'wall (no CPU times)']
    gating.setdefault((mode, w), []).append(needed)
    if args.filter_factor:
        limit = args.filter_factor * statistics.median(hw)
        kept = [x for x in hw if x <= limit]
        fb = 62.5 * q(kept, 0.99) / statistics.median(sw)
        cols += ['%.1f' % (q(kept, 0.99) * 1e6), '%.6f' % fb, str(math.ceil(fb))]
    print(' | '.join(cols))
for fn in sorted(glob.glob(f'{args.out}/lib-*.json')):
    d = json.load(open(fn))
    print(os.path.basename(fn), ', '.join('%s %.1f us' % (r['name'], r['median(elapsed)'] / r['batch'] * 1e6 if 'batch' in r
                                                         else r['median(elapsed)'] * 1e6) for r in d['results']))
print()
for mode, role in (('pause', 'gates'), ('cont', 'reported, does not gate')):
    for w in ('worst', 'release'):
        if (mode, w) in gating:
            c = gating[(mode, w)]
            print(f"{'paused' if mode == 'pause' else 'continuous'} runs ({role}), {w} path: C_needed {min(c)} to {max(c)} over {len(c)} runs")
if not any(mode == 'pause' for mode, _ in gating):
    print("no paused runs: nothing gates")
for r in rejected:
    print(f"REJECTED {r}")
if args.summary_only:
    sys.exit(1 if rejected else 0)
rounds = max((k[3] for k in seen), default=0)
expected = [(sha, w, mode, r) for sha in ('standard', 'sse4_avx2') for w in ('0', '1') for mode in ('cont', 'pause')
            for r in range(1, rounds + 1)]
missing = [k for k in expected if k not in seen and not any(x.startswith(f"{k[0]}-w{k[1]}-{k[2]}-r{k[3]}:") for x in rejected)]
short = [k for k, n in seen.items() if n != (args.samples, args.samples)]
name = lambda k: f"{k[0]}-w{k[1]}-{k[2]}-r{k[3]}"  # noqa: E731
if rejected:
    sys.exit(1)
if not seen or missing or short:
    why = []
    if not seen:
        why.append("no runs")
    if missing:
        why.append("missing: " + ", ".join(name(k) for k in missing))
    if short:
        why.append("not " + f"{args.samples} samples of each check: " + ", ".join(f"{name(k)} ({seen[k][0]}, {seen[k][1]})" for k in short))
    print("INCOMPLETE: " + "; ".join(why))
    sys.exit(2)
print(f"COMPLETE: 2 SHA-256 settings x 2 paths x continuous and paused x {rounds} rounds, {args.samples} samples of each check")
sys.exit(0)

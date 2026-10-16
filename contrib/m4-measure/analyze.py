#!/usr/bin/env python3
"""Summarize the output of measure.sh: per run, for the whole-path Schnorr and SLH-DSA checks, the median
and p99 of their wall time and of their thread CPU time, the share of preempted samples, the host's steal
time, and the bound 1.25 x 50 x p99(SLH-DSA) / median(Schnorr) from the wall times and from the CPU times,
unrounded, with C_needed, the smallest integer C at or above it (its ceiling).

Usage: analyze.py <out-dir> [--preempt-ms T] [--preempt-ratio R] [--max-preempted P] [--filter-factor F]

A sample is preempted if its wall time exceeds its thread CPU time by more than T ms (default 1) or is
more than R times it (default 2): the thread did not run for part of the check, because the scheduler or
the host ran something else. Which bound gates (README.md): the wall bound where the samples show no
preemption, that is at most P (default 0.1 %) of the samples of each distribution preempted and no steal
time during the run; otherwise the CPU bound, reported with the preempted share and the steal time. The
raw samples are kept in samples-<run>/.

Runs without per-sample files (older output) are reported from the -output-json files, wall times only.

With --filter-factor F, each run is also reported filtered (diagnostic only): without the samples that
took more than F times the median of their own distribution. The filtered figures never gate."""
import argparse, csv, glob, json, math, os, statistics

ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
ap.add_argument("out")
ap.add_argument("--preempt-ms", type=float, default=1.0)
ap.add_argument("--preempt-ratio", type=float, default=2.0)
ap.add_argument("--max-preempted", type=float, default=0.001, help="largest preempted share for the wall bound to gate")
ap.add_argument("--filter-factor", type=float, default=None)
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


def steal(fn):
    """Steal time during the run, in clock ticks (USER_HZ, usually 1/100 s), for all CPUs and for the pinned
    one; None if /proc/stat was not readable."""
    if not os.path.exists(fn):
        return None
    lines = open(fn).read().split('\n')
    try:
        b, a = lines.index('before'), lines.index('after')
    except ValueError:
        return None
    before = {l.split()[0]: int(l.split()[8]) for l in lines[b + 1:a] if l.startswith('cpu')}
    after = {l.split()[0]: int(l.split()[8]) for l in lines[a + 1:] if l.startswith('cpu')}
    if not before or before.keys() != after.keys():
        return None
    return {k: after[k] - before[k] for k in before}


def fmt_steal(st):
    return '-' if st is None else ', '.join(f"{k} {v}" for k, v in sorted(st.items()))


header = ('sha impl | SLH path | run | n | Schnorr wall median us | p99 | Schnorr CPU median us | p99 | '
          'SLH wall median us | p99 | SLH CPU median us | p99 | preempted Schnorr | preempted SLH | '
          'steal ticks (all, pinned CPU) | wall bound | C_needed (wall) | CPU bound | C_needed (CPU) | gates')
if args.filter_factor:
    header += f' | filtered (diagnostic only, > {args.filter_factor:g} x median dropped): SLH wall p99 us | bound | C_needed'
print(header)
for sha in ('standard', 'sse4_avx2'):
    for fn in sorted(glob.glob(f'{args.out}/full-{sha}-w*-r*.json')):
        run = os.path.basename(fn)[len('full-'):-len('.json')]
        w = 'worst' if '-w1-' in fn else 'release'
        sdir = f'{args.out}/samples-{run}'
        if os.path.exists(f'{sdir}/VerifyScriptP2MRSchnorr.csv'):
            s = dist_csv(f'{sdir}/VerifyScriptP2MRSchnorr.csv')
            h = dist_csv(f'{sdir}/VerifyScriptP2MRSLHDSA128S.csv')
        else:
            s = dist_json(fn, 'VerifyScriptP2MRSchnorr')
            h = dist_json(fn, 'VerifyScriptP2MRSLHDSA128S')
        sw, hw = [x for x, _ in s], [x for x, _ in h]
        wall_bound = 62.5 * q(hw, 0.99) / statistics.median(sw)
        cols = [sha, w, run, str(len(h)), '%.1f' % (statistics.median(sw) * 1e6), '%.1f' % (q(sw, 0.99) * 1e6)]
        have_cpu = s[0][1] is not None
        st = steal(f'{args.out}/stat-{run}.txt')
        if have_cpu:
            sc, hc = [c for _, c in s], [c for _, c in h]
            cpu_bound = 62.5 * q(hc, 0.99) / statistics.median(sc)
            ps, ph = preempted(s), preempted(h)
            quiet = ps <= args.max_preempted and ph <= args.max_preempted and st is not None and not any(st.values())
            cols += ['%.1f' % (statistics.median(sc) * 1e6), '%.1f' % (q(sc, 0.99) * 1e6)]
        else:
            cols += ['-', '-']
        cols += ['%.1f' % (statistics.median(hw) * 1e6), '%.1f' % (q(hw, 0.99) * 1e6)]
        if have_cpu:
            cols += ['%.1f' % (statistics.median(hc) * 1e6), '%.1f' % (q(hc, 0.99) * 1e6), '%.3f%%' % (ps * 100),
                     '%.3f%%' % (ph * 100), fmt_steal(st), '%.6f' % wall_bound, str(math.ceil(wall_bound)),
                     '%.6f' % cpu_bound, str(math.ceil(cpu_bound)), 'wall' if quiet else 'CPU']
        else:
            cols += ['-', '-', '-', '-', fmt_steal(st), '%.6f' % wall_bound, str(math.ceil(wall_bound)), '-', '-', 'wall (no CPU times)']
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

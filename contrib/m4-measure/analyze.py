#!/usr/bin/env python3
"""Summarize the output of measure.sh: per run, for the whole-path Schnorr and SLH-DSA checks, the median
and p99 of their wall time and of their thread CPU time, the share of preempted samples, the host's steal
time, and the bound 1.25 x 50 x p99(SLH-DSA) / median(Schnorr) from the wall times and from the CPU times,
unrounded, with C_needed, the smallest integer C at or above it (its ceiling).

Usage: analyze.py <out-dir> --rounds N [--preempt-ms T] [--preempt-ratio R] [--max-preempted P]
                            [--cpu-tolerance-us U] [--cpu-tolerance-fraction F] [--filter-factor F]
       analyze.py <out-dir> --summary-only [options as above but --rounds]

Runs are continuous or paused (measure.sh runs each both ways). The paused runs gate; the continuous ones
are reported next to them (README.md, "Which single-check bound gates").

Gate mode (the default) checks a fixed matrix, which nothing in the out-dir can make smaller: both SHA-256
settings (standard, sse4_avx2), both paths (release, worst), both modes (continuous, paused), and rounds 1
to N of --rounds N, required, with N >= 2. Every run of the matrix needs both of its per-sample CSV files,
each with 10,000 samples; a run with only its -output-json file does not count. If the out-dir has the
plan.txt that measure.sh writes, it only cross-checks: it must say exactly that matrix and N, each key
(sha, paths, modes, rounds, samples) once, samples 10000, and nothing else. Missing or short input, or a
plan.txt that says anything else, prints INCOMPLETE and exits with 2, before any statistic of a run with
missing input is computed; a clock fault (a rejected run) exits with 1.

--summary-only reads no plan.txt, needs no --rounds (and ignores one given) and checks no matrix: it reports
every run it finds, under its own name, whatever the format: from a samples-<run> directory, or, without
one, from full-<run>.json (wall times only; older output names its runs without -cont-/-pause-). A run whose
input cannot be read is listed as SKIPPED and the others are still reported. Library-level results that
cannot be read are said so, in either mode, and do not count. An optional input (the library results; in
--summary-only, a run's JSON) never changes the verdict: whatever fails while reading or parsing it, that
file or run is marked unreadable and the rest goes on; the verdict depends on the required inputs only.

Each of the six numeric options with a fractional value (--preempt-ms, --preempt-ratio, --max-preempted,
--cpu-tolerance-us, --cpu-tolerance-fraction, --filter-factor) must be finite: the tolerances and the
preemption limits not negative, --filter-factor above 0. Anything else is a usage error (exit 2) before any
input is read. The defaults are as above. --rounds is checked as described above. Text taken from any input
is printed with backslash escapes where the output cannot encode it, so no write can fail.

Exit codes, the only ones: 0, a complete, accepted gate (or a summary without a rejected run); 1, a run
rejected for a clock fault; 2, INCOMPLETE in gate mode, a wrong command line, or anything unexpected, which
prints one line ('INCOMPLETE: unexpected input: <type>: <message>', or 'error: ...' with --summary-only)
and never a traceback.

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

With --filter-factor F, each run is also reported filtered (diagnostic only): without the samples that
took more than F times the median of their own distribution. The filtered figures never gate."""
import argparse, csv, glob, json, math, os, re, statistics, sys

# Text taken from any input (names inside files, file names) can never make a write fail: before anything
# is printed, characters the output cannot encode are written as backslash escapes.
for stream in (sys.stdout, sys.stderr):
    stream.reconfigure(errors='backslashreplace')

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
ap.add_argument("--rounds", type=int, default=None, help="rounds of the matrix, required in gate mode, at least 2")
ap.add_argument("--summary-only", action="store_true", help="report the runs found; no plan, no matrix")
args = ap.parse_args()
# Every numeric option finite and in its range, checked before any input is read (a usage error, exit 2).
for name, value, low, strict in (('--preempt-ms', args.preempt_ms, 0, False), ('--preempt-ratio', args.preempt_ratio, 0, False),
                                 ('--max-preempted', args.max_preempted, 0, False),
                                 ('--cpu-tolerance-us', args.cpu_tolerance_us, 0, False),
                                 ('--cpu-tolerance-fraction', args.cpu_tolerance_fraction, 0, False),
                                 ('--filter-factor', args.filter_factor, 0, True)):
    if value is None:
        continue
    if not math.isfinite(value) or value < low or (strict and value == low):
        ap.error(f"{name} must be a finite number {'above' if strict else 'at least'} {low}, not {value}")


def main():


    SHAS, PATHS, MODES, SAMPLES = ('standard', 'sse4_avx2'), ('0', '1'), ('cont', 'pause'), 10000


    def dist_json(fn, name):
        try:
            d = json.load(open(fn))
            for r in d['results']:
                if r['name'] == name:
                    return [(float(m['elapsed']) / float(m['iterations']), None) for m in r['measurements']]
        except Exception:  # an optional input: whatever fails, this run is unreadable and the rest goes on
            pass
        return None


    def dist_csv(path):
        """(wall, cpu) of every sample in seconds; None if the file is missing or cannot be parsed."""
        try:
            with open(path) as f:
                return [(int(r['wall_ns']) * 1e-9, int(r['cpu_ns']) * 1e-9) for r in csv.DictReader(f)]
        except (OSError, ValueError, KeyError, TypeError, OverflowError, csv.Error):
            return None


    def check_plan(out, rounds):
        """None if the out-dir has no plan.txt or one that says exactly the gate's matrix with these rounds;
        otherwise why not."""
        path = f'{out}/plan.txt'
        if not os.path.exists(path):
            return None
        try:
            text = open(path, encoding='ascii').read()
        except (OSError, UnicodeDecodeError) as e:
            return f"plan.txt cannot be read ({e.__class__.__name__})"
        lines = text.split('\n')
        if lines and lines[-1] == '':
            lines.pop()
        want = {'sha': list(SHAS), 'paths': list(PATHS), 'modes': list(MODES), 'rounds': [str(rounds)], 'samples': [str(SAMPLES)]}
        got = {}
        for line in lines:
            words = line.split()
            if not words:
                return "plan.txt has an empty line"
            key, values = words[0], words[1:]
            if key not in want:
                return f"plan.txt has an unknown key '{key}'"
            if key in got:
                return f"plan.txt has '{key}' more than once"
            got[key] = values
        for key in want:
            if key not in got:
                return f"plan.txt lacks '{key}'"
        for key in ('rounds', 'samples'):
            v = got[key]
            if len(v) != 1 or not v[0].isdigit() or v[0].startswith('0'):
                return f"plan.txt's {key} is not a positive integer: {' '.join(v) or '(nothing)'}"
        for key, values in want.items():
            if key == 'rounds' and rounds is None:
                continue  # no --rounds to compare with; that is reported on its own
            if got[key] != values:
                return f"plan.txt says {key} {' '.join(got[key])}, not {' '.join(values)}"
        return None


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
    incomplete_runs = []  # runs whose input is missing or short: no statistic is computed for them
    gating = {}  # (mode, path) -> list of C_needed of the gating bound
    gate_problems = []
    if args.summary_only:
        # Every run found, under its own name, whatever the format: a samples-<run> directory or a
        # full-<run>.json file (older output names its runs without -cont-/-pause-).
        names = set()
        for p in glob.glob(f'{args.out}/samples-*'):
            names.add(os.path.basename(p)[len('samples-'):])
        for p in glob.glob(f'{args.out}/full-*.json'):
            names.add(os.path.basename(p)[len('full-'):-len('.json')])
        runs = []
        for run in sorted(names):
            m = re.fullmatch(r'(standard|sse4_avx2)-w([01])(?:-(cont|pause))?-r(\d+)', run)
            if m:
                runs.append((run, m.group(1), m.group(2), m.group(3) or 'cont', int(m.group(4))))
    else:
        if args.rounds is None or args.rounds < 2:
            gate_problems.append("--rounds N with N >= 2 is required in gate mode" if args.rounds is None
                                 else f"--rounds {args.rounds}: the gate needs at least 2 rounds")
        rounds = args.rounds if args.rounds is not None and args.rounds >= 1 else 0
        why = check_plan(args.out, args.rounds)
        if why:
            gate_problems.append(why)
        runs = [(f"{sha}-w{w}-{mode}-r{r}", sha, w, mode, r)
                for sha in SHAS for w in PATHS for mode in MODES for r in range(1, rounds + 1)]
    for run, sha, wd, mode, rnd in runs:
        w = 'worst' if wd == '1' else 'release'
        sdir = f'{args.out}/samples-{run}'
        fn = f'{args.out}/full-{run}.json'
        if args.summary_only and not os.path.isdir(sdir):
            s = dist_json(fn, 'VerifyScriptP2MRSchnorr')
            h = dist_json(fn, 'VerifyScriptP2MRSLHDSA128S')
        else:
            s = dist_csv(f'{sdir}/VerifyScriptP2MRSchnorr.csv')
            h = dist_csv(f'{sdir}/VerifyScriptP2MRSLHDSA128S.csv')
        counts = (len(s) if s is not None else None, len(h) if h is not None else None)
        # Missing or short input: no statistic for this run.
        short = counts != (SAMPLES, SAMPLES) if not args.summary_only else (not counts[0] or not counts[1])
        if short:
            shown = ', '.join(f"{b} {'missing or unreadable' if n is None else f'{n} samples'}"
                              for b, n in zip(('Schnorr', 'SLH-DSA'), counts))
            incomplete_runs.append(f'{run}: {shown}' + ('' if args.summary_only else f', not {SAMPLES} each'))
            continue
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
            if kept:
                fb = 62.5 * q(kept, 0.99) / statistics.median(sw)
                cols += ['%.1f' % (q(kept, 0.99) * 1e6), '%.6f' % fb, str(math.ceil(fb))]
            else:
                cols += ['- (no sample kept)', '-', '-']
        print(' | '.join(cols))
    # The library-level benchmarks are reported, not gated: one that cannot be read is said so.
    for fn in sorted(glob.glob(f'{args.out}/lib-*.json')):
        try:
            d = json.load(open(fn))
            line = ', '.join('%s %.1f us' % (r['name'], float(r['median(elapsed)']) / float(r['batch']) * 1e6 if 'batch' in r
                                             else float(r['median(elapsed)']) * 1e6) for r in d['results'])
        except Exception as e:  # an optional input: whatever fails, it is named unreadable and not used
            line = f"unreadable ({type(e).__name__}), not used"
        print(os.path.basename(fn), line)
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
        for r in incomplete_runs:
            print(f"SKIPPED {r}")
        sys.exit(1 if rejected else 0)
    if rejected:
        sys.exit(1)
    why = list(gate_problems)
    if incomplete_runs:
        why.append("missing or short input: " + "; ".join(incomplete_runs))
    if why:
        print("INCOMPLETE: " + "; ".join(why))
        sys.exit(2)
    print(f"COMPLETE: 2 SHA-256 settings x 2 paths x continuous and paused x {args.rounds} rounds, {SAMPLES} samples of each check")
    sys.exit(0)


if __name__ == '__main__':
    try:
        main()
    except SystemExit:
        raise
    except Exception as e:  # anything unexpected: one line and exit 2, never a traceback
        message = ' '.join(str(e).split())[:300]
        print(f"{'error' if args.summary_only else 'INCOMPLETE'}: unexpected input: {type(e).__name__}: {message}")
        sys.exit(2)

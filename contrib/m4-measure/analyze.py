#!/usr/bin/env python3
"""Summarize the output of measure.sh: per run, the median and p99 of the whole-path Schnorr and SLH-DSA
checks, the bound 1.25 x 50 x p99(SLH-DSA) / median(Schnorr) unrounded, and C_needed, the smallest integer C
at or above it (its ceiling).

Usage: analyze.py <out-dir> [--stall-factor F]

With --stall-factor F, each run is also reported without host stalls. A sample is a stall if it took more
than F times the median of its own distribution (the same benchmark in the same run): a check that the host
interrupted for much longer than the check itself. Such stalls hit a long check (an SLH-DSA verification of
about a millisecond) far more often than a short one (a Schnorr check of about 50 us) when the host pauses
for milliseconds every so often, so on such a host the raw p99 of the long check measures the host. The
stall-free columns give, for each distribution, the share of samples taken as stalls, and the bound and
C_needed computed from the p99 of the SLH-DSA samples that are not stalls (the Schnorr median is that of all
its samples). F = 5 suits checks whose spread without stalls is well under five times their median."""
import argparse, glob, json, math, os, statistics

ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
ap.add_argument("out")
ap.add_argument("--stall-factor", type=float, default=None)
args = ap.parse_args()


def dist(fn, name):
    d = json.load(open(fn))
    for r in d['results']:
        if r['name'] == name:
            return sorted(m['elapsed'] / m['iterations'] for m in r['measurements'])
    return None


def q(xs, p): return xs[min(len(xs) - 1, int(round(p * (len(xs) - 1))))]


def stall_free(xs, factor):
    limit = factor * statistics.median(xs)
    kept = [x for x in xs if x <= limit]
    return kept, 1 - len(kept) / len(xs)


header = ('sha impl | SLH path | file | Schnorr median us | Schnorr p99 us | SLH median us | SLH p99 us | n | '
          'bound = 62.5*p99(SLH)/median(Schnorr) | C_needed = ceil(bound)')
if args.stall_factor:
    header += (f' | stalls (> {args.stall_factor:g} x median) Schnorr | stalls SLH | stall-free SLH p99 us | '
               'stall-free bound | stall-free C_needed')
print(header)
for sha in ('standard', 'sse4_avx2'):
    for fn in sorted(glob.glob(f'{args.out}/full-{sha}-w*-r*.json')):
        w = 'worst' if '-w1-' in fn else 'release'
        s = dist(fn, 'VerifyScriptP2MRSchnorr')
        h = dist(fn, 'VerifyScriptP2MRSLHDSA128S')
        s_med, h_p99 = statistics.median(s), q(h, 0.99)
        bound = 62.5 * h_p99 / s_med
        line = '%s | %s | %s | %.1f | %.1f | %.1f | %.1f | %d | %.6f | %d' % (
            sha, w, os.path.basename(fn), s_med * 1e6, q(s, 0.99) * 1e6, statistics.median(h) * 1e6, h_p99 * 1e6,
            len(h), bound, math.ceil(bound))
        if args.stall_factor:
            _, s_share = stall_free(s, args.stall_factor)
            h_kept, h_share = stall_free(h, args.stall_factor)
            free_bound = 62.5 * q(h_kept, 0.99) / s_med
            line += ' | %.3f%% | %.3f%% | %.1f | %.6f | %d' % (s_share * 100, h_share * 100, q(h_kept, 0.99) * 1e6,
                                                              free_bound, math.ceil(free_bound))
        print(line)
for fn in sorted(glob.glob(f'{args.out}/lib-*.json')):
    d = json.load(open(fn))
    print(os.path.basename(fn), ', '.join('%s %.1f us' % (r['name'], r['median(elapsed)'] / r['batch'] * 1e6 if 'batch' in r
                                                         else r['median(elapsed)'] * 1e6) for r in d['results']))

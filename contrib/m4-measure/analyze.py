#!/usr/bin/env python3
"""Summarize the output of measure.sh: per run, the median and p99 of the whole-path Schnorr and SLH-DSA
checks, the bound 1.25 x 50 x p99(SLH-DSA) / median(Schnorr) unrounded, and C_needed, the smallest integer C
at or above it (its ceiling). Usage: analyze.py <out-dir>"""
import json, glob, math, sys, statistics, os
out=sys.argv[1]
def dist(fn, name):
    d=json.load(open(fn))
    for r in d['results']:
        if r['name']==name:
            xs=sorted(m['elapsed']/m['iterations'] for m in r['measurements'])
            return xs
    return None
def q(xs,p): return xs[min(len(xs)-1,int(round(p*(len(xs)-1))))]
rows=[]
for sha in ('standard','sse4_avx2'):
    for fn in sorted(glob.glob(f'{out}/full-{sha}-w*-r*.json')):
        w='worst' if '-w1-' in fn else 'release'
        s=dist(fn,'VerifyScriptP2MRSchnorr'); h=dist(fn,'VerifyScriptP2MRSLHDSA128S')
        rows.append((sha,w,os.path.basename(fn),statistics.median(s)*1e6,q(s,0.99)*1e6,statistics.median(h)*1e6,q(h,0.99)*1e6,len(h)))
print('sha impl | SLH path | file | Schnorr median us | Schnorr p99 us | SLH median us | SLH p99 us | n | bound = 62.5*p99(SLH)/median(Schnorr) | C_needed = ceil(bound)')
for r in rows:
    bound=62.5*r[6]/r[3]
    print('%s | %s | %s | %.1f | %.1f | %.1f | %.1f | %d | %.6f | %d' % (r+(bound, math.ceil(bound))))
for fn in sorted(glob.glob(f'{out}/lib-*.json')):
    d=json.load(open(fn))
    print(os.path.basename(fn), ', '.join('%s %.1f us'%(r['name'], r['median(elapsed)']/r['batch']*1e6 if 'batch' in r else r['median(elapsed)']*1e6) for r in d['results']))

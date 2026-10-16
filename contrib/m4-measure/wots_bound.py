#!/usr/bin/env python3
"""Why the worst-case hook bounds every SLH-DSA-SHA2-128s verification (README.md, "Why the hook path is
the worst case").

In each of the 7 hypertree layers, the verifier completes 35 WOTS+ chains (w = 16): 32 message chains,
the chain of digit m_i running 15 - m_i steps, and 3 checksum chains. The checksum is
x = sum(15 - m_i), 0 <= x <= 480, shifted left by 4 bits; its three base-16 digits c_j are therefore
the three hex digits of x, and the checksum chains run sum(15 - c_j) = 45 - ds(x) steps, ds(x) being
the sum of the hex digits of x. So a layer runs

    steps(x) = x + 45 - ds(x)

chain steps. As x grows by 1, ds(x) grows by at most 1, so steps(x) never decreases, and its maximum is
steps(480) = 480 + 45 - (1 + 14 + 0) = 510. Every other part of a verification (the message hash,
FORS, the WOTS+ public key compressions, the authentication paths) does the same work for every
signature. The hook takes every message digit as 0, so x = 480 in every layer: its path, 3,894
SHA-256 compressions per verification, bounds every signature. This script checks the numbers."""

L1, W = 32, 16


def ds(x):
    return sum(int(d, 16) for d in f"{x:03x}")


def steps(x):
    return x + 45 - ds(x)


def checksum_digits(x):
    """The three base-16 digits of the checksum as SLH-DSA computes them (FIPS 205: csum shifted left by 4, then base 16)."""
    csum = x << 4
    return [(csum >> s) & 0xf for s in (12, 8, 4)]


xs = range(0, L1 * (W - 1) + 1)
for x in xs:
    assert checksum_digits(x) == [int(d, 16) for d in f"{x:03x}"], x
    assert steps(x) == x + sum(15 - c for c in checksum_digits(x)), x
assert all(steps(x + 1) >= steps(x) for x in xs[:-1])
top = max(steps(x) for x in xs)
argmax = [x for x in xs if steps(x) == top]
assert top == 510 and argmax == [480], (top, argmax)
print(f"x = 0..{xs[-1]}: steps(x) = x + 45 - ds(x) is non-decreasing; maximum {top}, only at x = {argmax[0]}")
print(f"7 layers x {top} = {7 * top} chain steps on the hook path; real signatures: "
      f"{7 * steps(240)} at x = 240 in every layer (the mean of x)")

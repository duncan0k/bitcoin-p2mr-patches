#!/usr/bin/env python3
# Copyright (c) 2026 duncan0k
# Distributed under the MIT software license, see the accompanying
# file COPYING or http://www.opensource.org/licenses/mit-license.php.
"""Run the single-check gate (design section 2.5) and print the requirement it gives:

    C >= 1.5 x 50 x p99(rescue) / median(Schnorr)

for each of the three rescue inputs, on wall time and on thread CPU time, per round, continuous and
paused. As in the M4-S kit, the paused runs gate: a pause before every sample keeps the host's
scheduling out of the samples as far as it can.

    gate.py <measure binary> <input dir> <rounds> <rescue samples> <schnorr samples> <pause us> [<Core tree>]

With the Core tree, the requirement of the paused runs is checked against the tree's own
VALIDATION_WEIGHT_PER_P2MR_RESCUE_CHECK (src/script/script.h): PASS exits with 0, FAIL with 1, and a
round with samples of a thread CPU time of 0, or a figure that is not finite and positive (a Schnorr
median is a denominator), is not usable and makes the result INCOMPLETE (2), as does any failure, with
the reason.
"""
import math
import re
import subprocess
import sys
from pathlib import Path

LINE = re.compile(r"^(\S+)\s+(\S+)\s+n=(\d+)\s+wall median\s+([\d.]+) us p99\s+([\d.]+) us max\s+([\d.]+) us \| cpu median\s+([\d.]+) us p99\s+([\d.]+) us zero-cpu (\d+)")
KINDS = ("rescue-worst-invalid", "rescue-msg-65535", "rescue-valid")


def good(x):
    return math.isfinite(x) and x > 0


def gate(binary, inputs, rounds, n_rescue, n_schnorr, pause, core):
    """The requirement per input, clock and mode; with a Core tree, the verdict against its weight. Returns
    the exit status: 0 PASS (or, without a tree, every round usable), 1 FAIL, 2 INCOMPLETE."""
    unusable = []
    worst = {}
    peak = None
    for r in range(int(rounds)):
        for p in ("0", pause):
            out = subprocess.run([binary, inputs, n_rescue, n_schnorr, p], capture_output=True, check=True, text=True).stdout
            rows = {}
            for line in out.splitlines():
                m = LINE.match(line)
                if m:
                    rows[m.group(1)] = {"wall_med": float(m.group(4)), "wall_p99": float(m.group(5)), "cpu_med": float(m.group(7)),
                                        "cpu_p99": float(m.group(8)), "zero": int(m.group(9)), "mode": m.group(2)}
                elif line.startswith("peak"):
                    peak = line
            missing = [k for k in ("schnorr",) + KINDS if k not in rows]
            if missing:
                raise RuntimeError(f"round {r + 1}: the measuring binary gave no figures for {missing}")
            s = rows["schnorr"]
            mode = s["mode"]
            # A round is usable only if no sample has a thread CPU time of 0 and every figure the
            # requirement uses is finite and positive: the Schnorr medians are its denominators.
            reasons = []
            if s["zero"] or any(rows[k]["zero"] for k in rows):
                reasons.append("samples with a thread CPU time of 0")
            bad = [f"{name} {field} {rows[name][field]}" for name in ("schnorr",) + KINDS
                   for field in (("wall_med", "cpu_med") if name == "schnorr" else ("wall_p99", "cpu_p99"))
                   if not good(rows[name][field])]
            if bad:
                reasons.append("figures not finite and positive: " + ", ".join(bad))
            if reasons:
                unusable.append(f"round {r + 1} {mode}: " + "; ".join(reasons))
                print(f"round {r + 1} {mode}: {'; '.join(reasons)}; round not usable")
                continue
            for kind in KINDS:
                k = rows[kind]
                req_wall = 1.5 * 50 * k["wall_p99"] / s["wall_med"]
                req_cpu = 1.5 * 50 * k["cpu_p99"] / s["cpu_med"]
                print(f"round {r + 1} {mode:<10} {kind:<21} p99 wall {k['wall_p99']:9.1f} us cpu {k['cpu_p99']:9.1f} us | "
                      f"schnorr median wall {s['wall_med']:6.2f} us cpu {s['cpu_med']:6.2f} us | C >= {math.ceil(req_wall):7d} (wall) {math.ceil(req_cpu):7d} (cpu)")
                key = (mode, kind)
                worst[key] = max(worst.get(key, 0), math.ceil(req_wall), math.ceil(req_cpu))
    if peak:
        print(peak)
    for (mode, kind), req in sorted(worst.items()):
        print(f"highest requirement, {mode:<10} {kind:<21} {req}")
    paused = [v for (m, _), v in worst.items() if m == "paused"]
    if unusable or not paused:
        for line in unusable or ["no usable paused round"]:
            print("INCOMPLETE: " + line)
        print("gate: INCOMPLETE")
        return 2
    required = max(paused)
    print(f"gate (paused runs): C >= {required}")
    if not core:
        return 0
    weight = int(re.search(r"VALIDATION_WEIGHT_PER_P2MR_RESCUE_CHECK\{(\d+)\}",
                           (Path(core) / "src" / "script" / "script.h").read_text()).group(1))
    verdict = "PASS" if required <= weight else "FAIL"
    print(f"the series' weight: C = {weight}; {verdict} (required {required}, margin {weight / required:.3f})")
    return {"PASS": 0, "FAIL": 1}[verdict]


def main():
    args = sys.argv[1:]
    if len(args) not in (6, 7):
        print(__doc__, file=sys.stderr)
        return 2
    try:
        return gate(*args[:6], args[6] if len(args) == 7 else None)
    except Exception as e:
        print(f"gate: INCOMPLETE\nINCOMPLETE: the run failed: {type(e).__name__}: {e}")
        return 2


if __name__ == "__main__":
    sys.exit(main())

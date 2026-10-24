#!/usr/bin/env python3
# Copyright (c) 2026 duncan0k
# Distributed under the MIT software license, see the accompanying
# file COPYING or http://www.opensource.org/licenses/mit-license.php.
"""Run the single-check gate (design section 2.5) and print the requirement it gives:

    C >= 1.5 x 50 x p99(rescue) / median(Schnorr)

for each of the three rescue inputs, on wall time and on thread CPU time, per round, continuous and
paused. As in the M4-S kit, the paused runs gate: a pause before every sample keeps the host's
scheduling out of the samples as far as it can.

    gate.py <measure binary> <input dir> <rounds> <rescue samples> <schnorr samples> <pause us>
"""
import math
import re
import subprocess
import sys

binary, inputs, rounds, n_rescue, n_schnorr, pause = sys.argv[1:7]
LINE = re.compile(r"^(\S+)\s+(\S+)\s+n=(\d+)\s+wall median\s+([\d.]+) us p99\s+([\d.]+) us max\s+([\d.]+) us \| cpu median\s+([\d.]+) us p99\s+([\d.]+) us zero-cpu (\d+)")
worst = {}
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
        s = rows["schnorr"]
        mode = rows["schnorr"]["mode"]
        for kind in ("rescue-worst-invalid", "rescue-msg-65535", "rescue-valid"):
            k = rows[kind]
            req_wall = 1.5 * 50 * k["wall_p99"] / s["wall_med"]
            req_cpu = 1.5 * 50 * k["cpu_p99"] / s["cpu_med"]
            print(f"round {r + 1} {mode:<10} {kind:<21} p99 wall {k['wall_p99']:9.1f} us cpu {k['cpu_p99']:9.1f} us | "
                  f"schnorr median wall {s['wall_med']:6.2f} us cpu {s['cpu_med']:6.2f} us | C >= {math.ceil(req_wall):7d} (wall) {math.ceil(req_cpu):7d} (cpu)")
            key = (mode, kind)
            worst[key] = max(worst.get(key, 0), math.ceil(req_wall), math.ceil(req_cpu))
        if s["zero"] or any(rows[k]["zero"] for k in rows):
            print(f"round {r + 1} {mode}: samples with a thread CPU time of 0, round not usable")
print(peak)
for (mode, kind), req in sorted(worst.items()):
    print(f"highest requirement, {mode:<10} {kind:<21} {req}")
print(f"gate (paused runs): C >= {max(v for (m, _), v in worst.items() if m == 'paused')}")

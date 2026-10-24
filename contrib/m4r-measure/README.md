# m4r-measure: the single-check gate of the M4-R validation weight

Measurement only, never part of a node. It measures what
`docs/m4r/m4r-rescue-design-2026-10-23.md`, section 2.5, asks of
`VALIDATION_WEIGHT_PER_P2MR_RESCUE_CHECK` (provisional 100,000):

```
C >= 1.5 x 50 x p99(rescue check) / median(Schnorr check)
```

over three rescue inputs, as the design names them:

- `rescue-worst-invalid`: a valid proof with `chall3` set to `ff..ff`, the most XOR work a verification
  does (ark0-rescue SPEC.md section 11), which is then invalid;
- `rescue-msg-65535`: a valid proof over a 65,535-byte message, the library's limit (the lane only
  takes 96 bytes);
- `rescue-valid`: a valid proof over the 96-byte message of the vector `r1` of
  `src/test/data/p2mr_rescue_vectors.json`.

`measure.cpp` links the node's own static library (`build/src/rescue/cargo-target/<triple>/release/libvoleith.a`)
and the node's libsecp256k1, and times each sample with both clocks, `steady_clock` and the thread's
CPU time, run continuously and with a pause before every sample. `gate.py` runs rounds and prints the
requirement per input, clock and mode; as in the M4-S kit (`contrib/m4-measure/`), the paused runs gate.
It also prints the peak resident set size of the process, an upper bound of the verifier's.

`build-portable.sh` makes a copy of the vendored crate whose `clmul::available()` returns false, so
that every GF(2^128) product takes the portable path, as on a CPU without PCLMULQDQ: the design's
"weak hardware" row.

```
B=<Core build dir>; S=<Core source dir>
python3 prepare.py $S $B/src/rescue/cargo-target-tools/*/release/voleith-prove /tmp/m4r-in
g++ -O2 -std=c++20 -I$S/src/rescue/voleith/include -I$S/src/secp256k1/include measure.cpp \
    $B/src/rescue/cargo-target/x86_64-unknown-linux-gnu/release/libvoleith.a $B/src/secp256k1/lib/libsecp256k1.a \
    -lpthread -ldl -o measure-node
python3 gate.py ./measure-node /tmp/m4r-in 3 300 20000 2000
bash build-portable.sh $S /tmp/m4r-portable     # then link measure.cpp against /tmp/m4r-portable/target/release/libvoleith.a
```

The whole path of a check (signature message, key relation, proof) is timed by
`bench_bitcoin -filter='.*P2MRRescue.*'` in the node's tree; the proof is all but all of it.

`block_gate.py` is a reduced whole-block gate, run as a functional test of the Core tree it measures
(`CORE=<Core source dir> python3 block_gate.py --configfile=<Core build>/test/config.ini --rounds=3`).
On a node restarted for each block with `-maxsigcachesize=0`, at `-par=1` and the default, it times
`submitblock` of three blocks on one tip, each seen for the first time:

- `rescue`: rescue spends in template R1, one input each, as many as the block weight limit takes next
  to the coinbase, counted by the weight of the block itself (30 inputs, about 3.92 MWU);
- `rescue-last-fails`: the same block with the last proof failing R7;
- `schnorr`: the worst block of BIP 342 checks in P2MR leaves of version `0xc0` (7 inputs of 10,580
  checks).

A rescue block slower than the Schnorr block, at either setting, fails the run (non-zero exit, with the
figures). It does not cover:

- the mixed block of the design's kind 2, rescue checks next to ML-DSA-44 and SLH-DSA checks;
- kind 3 at its worst: the failure deferred to the slowest place, which needs a build with a hook
  in the verifier; here the last proof simply fails, at whatever point the verifier finds it;
- a Schnorr block filled to the last weight unit: its seven inputs leave about 0.36 MWU unused, which
  makes the Schnorr side faster and the gate stricter.

The figures from this kit are indicative. The release figures come from the operator's machines, with
the full whole-block gate (see `docs/m4r/C-R1-REPORT.md`).

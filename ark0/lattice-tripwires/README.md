# Lattice tripwires: keys and scripts fixed before the outputs existed

M4-T is a rule of Ark-0 from height 55000 (`../NETWORK.md`, "Rule change at
height 55000 (M4-T and M4-B)"): P2MR leaves of a new version, `0xe2`, whose ML-DSA-44 checks stop working from the block after any
one of three designated outputs is spent; from then on a non-empty ML-DSA-44
signature in such a leaf makes the script fail. Their SLH-DSA checks keep
working, and their elliptic curve checks follow the M3 tripwire, as in leaves
of version `0xe0` (`../NETWORK.md`, "Rule change at height 30000 (M3)"). The
three designated outputs are the lattice tripwires. This directory fixed them
before they existed: their keys, their scripts, their addresses and how each
key was made. The outputs were created after it was published, at height
39971, by transaction
`aac5a1fef01545c338de92bceda520684a49682f2ec546900a4dbe99eba8827c`: L1 is
its output 0, L2 output 1 and L3 output 2. Patch 113, in `../../patches-m4b/`,
puts these outpoints, that height and the activation height, 55000, into the
build; nodes with that build enforce the rule from height 55000.

| File | What it is |
|---|---|
| `tripwires.json` | for each output: the ML-DSA-44 public key and its SHA-256, the two leaf scripts, the P2MR Merkle root, the scriptPubKey and the address |
| `check.py` | checks every value in `tripwires.json`, with Python's standard library alone |
| `l2/l2-keygen.py`, `l2/Dockerfile`, `l2/job.yaml` | the program that made L2, the image it ran in and the Kubernetes manifest that ran it, byte for byte as used |

## The three outputs

Each output holds 10,000 sats and is a P2MR output with two leaves at depth
one, both of leaf version `0xc0`:

- leaf A is S1 of an ML-DSA-44 public key (`../NETWORK.md`, "Rule change at
  height 8600 (M1)"): the 1,312-byte key in three pushes of 520, 520 and 272
  bytes, then `OP_CHECKMLDSA44` (`0xf0`);
- leaf B is `OP_RETURN` (`0x6a`).

| Output | Key | Who can spend it | Public key SHA-256 | Address |
|---|---|---|---|---|
| L1 | a NUMS key | nobody knows a private key; spending it takes a forged signature or a bug in verification | `591dae318a0fdde81a590eab8c8581e07657d2c30b97cf31d87bafe636c92805` | `tb1z3ldyx9mxwqzlp2zn22h2uvfm8rgvfk28dfpmszu77exm52wgm4tqxymzjg` |
| L2 | a destroyed key | nobody, if the private key was destroyed as stated below; spending it takes recovering the private key from the public key, or a bug in verification | `c7a02f1f92cb9b73dc324dab81e85737b43cbc06acf8c3d959e68153eaa1df44` | `tb1zfkz379fyp6m9kp5uqg572fr6yefuy8723tfhvdr6whu33qx8ennsxus4gq` |
| L3 | the drill key | the operator, at an announced time | `5237bdf62ebe594076000f2231b7fcf741e01a7482ffa64f50cc3ce14c28b495` | `tb1zsa6ra80mllsgxtqu4yqu86lfsvn4zzttgnwrsv3lutsywhtq6waqpqslw9` |

Leaf A is in version `0xc0`, not `0xe2`, for two reasons. Before the
activation height a leaf of version `0xe2` is not executed, so anyone could
spend it, and that spend would trip the rule as soon as it activates. After
the rule trips, an ML-DSA-44 check in `0xe2` no longer works at all. In
`0xc0`, `OP_CHECKMLDSA44` has been enforced since height 8600 and nothing
disables it.

For each block from the activation height on, the rule looks at the three
outpoints in the UTXO set as it stands before that block. If any of them is
missing, or was created at or above the activation height, ML-DSA-44 checks
in `0xe2` leaves fail in that block. So a spend takes effect from the next
block, and a reorganisation that undoes the spend restores the output.

What a spend would most likely mean:

| Spent | Most likely |
|---|---|
| only L1 | a signature forgery, or a bug in verification: a malformed signature accepted, the key's pushes put together wrongly, the message or the context string computed wrongly; recovering a private key does not help |
| only L2 | the private key was recovered, or it was not destroyed and has leaked |
| L1 and L2 | a general break of the lattice problems, or a bug in verification |
| L3 | the operator's announced drill, or a leak of the drill key |

General progress in lattice reduction would reach L1 and L2 at about the same
cost, so neither is an earlier warning than the other. On a network with one
block producer the operator could also keep L1 or L2 from being spent, or
spend L2 itself and pass it off as a break: the tripwires show how the rule
works, they do not make the operator trustless.

## L1: the NUMS key

The key is computed from a domain separator and a label, and nobody chose it:

```
X   = SHAKE256("MLDSA44-NUMS/v1" || 0x00 || "lattice-tripwire/0"), read in order
rho = the first 32 bytes of X
for i in 0..3, j in 0..255: take the next 3 bytes (b0, b1, b2) of X,
    z = b0 + 256*b1 + 65536*(b2 mod 128), until z < q = 8380417;
    t[i][j] = z                                                        (FIPS 204, Algorithm 14)
t1  = the high part of Power2Round(t), d = 13                          (Algorithm 35)
pk  = pkEncode(rho, t1), 1,312 bytes                                   (Algorithm 22)
```

`Verify` of FIPS 204 accepts any 1,312 bytes as a public key, so this is a
usable key. Neither string names the project: the outpoint ties the key to
this network. Plain random bytes for `t1` could be told apart from a real
key, because in a key made by `KeyGen` the values 0 and 1023 occur about half
as often as the others.

Why nobody can spend L1, in short: the chance that a private key of the shape
`KeyGen` produces exists for this public key is at most 2^-6237. Wider keys
that could sign very likely exist, but, as with the discrete logarithm of the
point H of BIP 341, nobody can compute one. Forging a signature is the
SelfTargetMSIS problem for a key derived from a uniformly random `t`: a
forger's advantage is at most its advantage against a real key without any
signatures, plus the advantage of telling MLWE samples from uniform ones. No
signing oracle is involved. In the quantum random oracle model SelfTargetMSIS
is an assumption of its own, the same one ML-DSA rests on.

## L2: the destroyed key

The operator made L2 once, at 2026-11-02 17:21:22 UTC, with a Kubernetes Job
in a namespace of its own on the operator's cluster, then deleted the
namespace. The manifest, `l2/job.yaml`, gives the pod:

- a NetworkPolicy that allows nothing in or out;
- no service account token, no service links, no host namespaces and no
  persistent volume;
- a read-only root file system, a non-root user and no capabilities;
- as its only writable place, a 1 MiB volume in memory;
- one run only: `backoffLimit: 0`, `restartPolicy: Never`, at most 300 s.

The program, `l2/l2-keygen.py`, marks its process as not dumpable, draws 32
bytes with `os.urandom`, runs ML-DSA-44 `KeyGen_internal` and prints two lines,
the public key and its SHA-256. It writes no file and prints nothing else.

| File | SHA-256 |
|---|---|
| `l2/l2-keygen.py` | `813c5aa1d4a59c2be48ae7f7c104615cc7396cc84f51463bb90618fd398a39e9` |
| `l2/Dockerfile` | `2bcbf84e5bd207644fd3643d2696ad84f748c22cfc27292d02dde4767388aa27` |
| `fips204.py` | `85b1a86a801179b32f527c25bae33be8f041f101bdac1e33bcaff723269175b5` |
| `l2/job.yaml` | `cd2f69a19b6dc7963482ad78dd5d0e5aad44330f7dc371bd2ee78b88a7661148` |

`fips204.py` is not repeated here: it is the file that
`../../patches-m1/0042-test-sign-and-verify-ML-DSA-44-in-the-functional-tes.patch`
adds as `test/functional/test_framework/crypto/fips204.py`, with the same
SHA-256. In the build context it was at `test/py/core_m1/fips204.py`, the path
the Dockerfile copies from. The three files in `l2/` are published as they
were used, so their comments refer to the operator's internal notes and paths.

The image was built with kaniko from the Dockerfile, on
`python:3.13-slim-bookworm@sha256:281d39ff02e3b389401586134ff9e6572954687822a58055ec07a60ded4e0b7f`,
and the Job ran it by digest:
`sha256:28b614b7dd1bd825588a7f4c53fd9b34823cc8c127a3756a12fa0cb04116e602`. The
build is not reproducible, so a rebuild gives another digest; the operator
keeps the image.

**What this shows and what it does not.** The operator generated this key
once in a one-time pod, without network access or persistent storage,
printing only the public key and its hash, and did not keep the private key.
That is the operator's statement. It cannot be proved: whoever administers
the cluster could have copied the pod's memory or run the program a second
time.

## L3: the drill key

The drill key is an ML-DSA-44 key made on the operator's machine from a
32-byte seed drawn from a cryptographic random number generator. Its public
key was derived from the seed by two independent implementations, which
agreed. The seed is kept only in encrypted backups, in three places, all
checked against the public key the day they were made.

The operator will spend L3 at an announced time, to show the rule at work on
the live chain. The drill will be announced at least two weeks ahead. If the
drill key leaked, the rule would trip early, the same as in the drill.

## How to check it

```bash
python3 check.py
```

It computes the NUMS key again from its two strings, then for each output the
SHA-256 of the key, both leaf scripts, the Merkle root (the TapLeaf and
TapBranch tagged hashes of BIP 341, as BIP 360 uses them), the scriptPubKey
`OP_2 <root>` and the bech32m address, and compares them with
`tripwires.json`. It prints `ok` for L1, L2 and L3 and exits 0 when all match.

Any node finds the outputs with
`scantxoutset start '["raw(<scriptPubKey>)"]'`, and `gettxout` shows the
scriptPubKey and the amount of each outpoint.

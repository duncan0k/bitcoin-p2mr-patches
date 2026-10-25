# m4r-crosscheck: the rescue proofs of the series' vectors through the Go verifier

`go_vectors.py` gives every proof of `src/test/data/p2mr_rescue_vectors.json` (the series' own vectors,
9 inputs) and of `src/test/data/p2mr_ts_rescue.json` (the vectors of p2mr-ts, 2 inputs, each with the
`m` of regtest, which its proofs are over, and of a custom signet, which they are not) to two verifiers.
One is the Go verifier of ark0-rescue (`go/` at `3c90cd4`, the commit `src/rescue/README.md` names,
standard library only). The other is Core's:
the `voleith-verify` of the vendored crate, built by a Core build with `-DBUILD_TESTS=ON`, the code the
node links. Each proof is given with its own `m`, and again with `chall3 = ff..ff`. It prints each
verdict, whether the two agree and match what the unit tests assert, and the number that differ.

```bash
git -C <ark0-rescue> archive 3c90cd4 go | tar -x -C /tmp/ark0-go
(cd /tmp/ark0-go/go && go build -trimpath -o /tmp/voleith-verify-go ./cmd/voleith-verify)
python3 go_vectors.py <Core tree> /tmp/voleith-verify-go \
    <Core build>/src/rescue/cargo-target-tools/<triple>/release/voleith-verify
```

On 2026-10-24 (Go 1.24.7): 26 cases, 0 differing.

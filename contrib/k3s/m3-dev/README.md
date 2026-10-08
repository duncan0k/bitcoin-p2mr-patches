# M3 development builds

The scripts that build and test the M3 series (`patches-m3/`, P2MR leaf version `0xe0` and its
tripwire outputs) in the `p2mr-build` namespace while it is being developed. They are a trimmed copy
of `../build.sh`, `../build.yaml` and `../run-build.sh` that keep to directories of their own on the
`p2mr-work` volume, so that nothing the release builds or the Ark-0 nodes use is touched:

| Path on the volume | What |
|---|---|
| `/work/m3-src` | source tree, cloned once and reset on every run |
| `/work/m3-ccache` | compiler cache |
| `/work/m3-logs/<run>` | `summary.txt` and one log per phase |
| `/work/m3-out/runs/<run>/bin` | the run's binaries; nothing is published from here |
| `/work/m3-test-cache`, `/work/m3-test-tmp/<run>` | functional test cache and temporary directories |
| `/work/patches/m3` | the copy of `patches-m3/` the runs apply, last |
| `/work/patches/m3-m2tests` | `patches-m2/0054`, the M2 test patch the series sits on, applied after `master m05 spacing m1 m2`; kept apart from `/work/patches/m2`, which the release builds use |
| `/work/m3-imgctx/node`, `/work/m3-imgctx/miner` | the staging network's image contexts (`../m3-staging/images.sh`); `node` is written only by a passing run with `M3_IMGCTX=1` |
| `/work/m3-fuzz-inputs/<run>` | the run's inputs for one fuzz target, when it has any; the build that runs them is `build-fuzz` in the source tree |

| File | Purpose |
|---|---|
| `m3-build.sh` | what the Job runs: `git am`, configure, build, unit tests, functional tests |
| `m3-build.yaml` | Job template, `m3-build-<run>`; `m3-run.sh` fills in its `__TOKEN__` placeholders |
| `m3-run.sh` | start one run from a directory that holds these files, `patches-m3/` and `patches-m3-m2tests/`, each with its SHA256SUMS file |
| `m3-wait.sh` | wait for a run, then print its summary and the tail of its log |
| `fuzz-script-flags.py` | write inputs for the `script_flags` fuzz target that break its monotonicity check through `P2MR`, `P2MR_MLDSA44` or `P2MR_TRIPWIRE`, the flags the target leaves alone from 0055 on |

A run, from such a directory on the node:

```bash
RUN_ID=m3-<name> FUNCTIONAL_TESTS="feature_p2mr_tripwire.py" ./m3-run.sh
TAIL=60 ./m3-wait.sh m3-<name>
```

Every run needs a new `RUN_ID` starting with `m3-`. `M3_PATCH_LIMIT=<n>` applies only the first
`n` patches of `patches-m3/`, to check one commit of the series on its own; `RUN_UNIT_TESTS=0`
skips the unit tests; `FUZZ_TARGET=<target> FUZZ_INPUTS=<directory>` also builds the fuzz binary with
`-DBUILD_FOR_FUZZING=ON` in `build-fuzz` (the fuzz binary of a Release build does not run targets)
and runs the target on each file of that directory, one result per file in the summary;
`M3_IMGCTX=1` publishes the binaries of a passing run of the whole series, with a
`PROVENANCE.txt`, to `/work/m3-imgctx/node` for `../m3-staging/images.sh`; `BUILD_JOBS` and `CPU`
stay at 10 or below. `m3-run.sh` starts no Job in a minute whose last digit is 4 or 5: the
Ark-0 observe CronJob runs at minute 5 of every ten (`5-59/10`).

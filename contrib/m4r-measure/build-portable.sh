#!/usr/bin/env bash
# Copyright (c) 2026 duncan0k
# Distributed under the MIT software license, see the accompanying
# file COPYING or http://www.opensource.org/licenses/mit-license.php.
#
# A measurement-only copy of the vendored verifier that never uses PCLMULQDQ: clmul::available()
# returns false, so every GF(2^128) product takes the portable path, as on a CPU without the
# instruction (the "weak hardware" row of the design's section 2.5). Never part of a node.
#
# Usage: bash build-portable.sh <Core tree> <output dir>
set -euo pipefail
CORE=$1
OUT=$2
mkdir -p "$OUT"
cp -r "$CORE/src/rescue/voleith" "$OUT/voleith"
python3 - "$OUT/voleith/src/clmul.rs" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
old = 'std::arch::is_x86_feature_detected!("pclmulqdq") && std::arch::is_x86_feature_detected!("sse2")'
assert s.count(old) == 1
open(p, "w").write(s.replace(old, "false"))
PY
cd "$OUT/voleith"
CARGO_HOME="$OUT/cargo-home" cargo +1.97.0 build --release --offline --locked --frozen --no-default-features --lib \
  --target-dir "$OUT/target" \
  --config "source.crates-io.replace-with='vendored-sources'" \
  --config "source.vendored-sources.directory='$CORE/src/rescue/vendor'"
echo "$OUT/target/release/libvoleith.a"

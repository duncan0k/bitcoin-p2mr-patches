#!/usr/bin/env python3
"""Packs the Windows release of Ark0Witness into one zip that is the same, byte for byte, on every run:

    python3 package.py <Ark0Witness.exe> <bitcoind.exe> <COPYING> <LICENSE-mldsa-native> <out.zip>

Ark0Witness.exe must be built with the SHA-256 of that bitcoind.exe (build.sh). COPYING and
LICENSE-mldsa-native are the files of the M3 release. Names, order, times and attributes are fixed;
the files are deflated at level 9.
"""
import hashlib
import sys
import zipfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
VERSION = (HERE.parent / "VERSION").read_text(encoding="ascii").strip()
TOP = f"ark0-witness-{VERSION}-win64/"
STAMP = (2026, 10, 14, 0, 0, 0)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def crlf(data: bytes) -> bytes:
    return data.replace(b"\r\n", b"\n").replace(b"\n", b"\r\n")


def main() -> None:
    if len(sys.argv) != 6:
        sys.exit(__doc__)
    witness, node, copying, mldsa = (Path(p).read_bytes() for p in sys.argv[1:5])
    out = Path(sys.argv[5])
    readme = (HERE / "README.txt").read_text(encoding="ascii").format(
        version=VERSION, witness_sha256=sha256(witness), bitcoind_sha256=sha256(node))
    files = [
        ("Ark0Witness.exe", witness),
        ("bitcoind.exe", node),
        ("README.txt", crlf(readme.encode("ascii"))),
        ("COPYING.txt", crlf(copying)),
        ("LICENSE-mldsa-native.txt", crlf(mldsa)),
        ("LICENSE-Ark0Witness.txt", crlf((HERE.parent / "LICENSE").read_bytes())),
    ]
    with zipfile.ZipFile(out, "w") as z:
        for name, data in files:
            info = zipfile.ZipInfo(TOP + name, STAMP)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 0
            info.external_attr = 0
            z.writestr(info, data, compresslevel=9)
    print(sha256(out.read_bytes()), out.name)


if __name__ == "__main__":
    main()

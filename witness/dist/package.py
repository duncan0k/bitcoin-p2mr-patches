#!/usr/bin/env python3
"""Packs Ark0Witness reproducibly. The original Windows command remains supported:

    python3 package.py <Ark0Witness.exe> <bitcoind.exe> <COPYING> <LICENSE-mldsa-native> <out.zip>

    python3 package.py <platform> <witness> <bitcoind> <COPYING> <LICENSE-mldsa-native> <out>

Platforms: windows (zip), macos (universal binaries, zip), linux-x86_64 and linux-aarch64 (tar.gz).
The witness must be built with the SHA-256 of the supplied node (build.sh). COPYING and
LICENSE-mldsa-native come from that platform's M3 release. Each platform has its own README template.
File names, order, modes and timestamps are fixed. Windows keeps its original file list, CRLF text,
zip attributes and level 9 deflation. Unix archives use executable modes and LF text; tar owners are
0 with empty names, and the gzip header has mtime 0 and no filename. Identical inputs give identical bytes.
"""
import gzip
import hashlib
import io
import stat
import sys
import tarfile
import zipfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
VERSION = (HERE.parent / "VERSION").read_text(encoding="ascii").strip()
TOP = f"ark0-witness-{VERSION}-win64/"
STAMP = (2026, 10, 14, 0, 0, 0)
MTIME = 1791936000  # The same fixed UTC instant as STAMP.


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def crlf(data: bytes) -> bytes:
    return data.replace(b"\r\n", b"\n").replace(b"\n", b"\r\n")


def main() -> None:
    args = sys.argv[1:]
    platform = "windows"
    if len(args) == 6:
        platform, *args = args
    if len(args) != 5 or platform not in {"windows", "macos", "linux-x86_64", "linux-aarch64"}:
        sys.exit(__doc__)
    witness, node, copying, mldsa = (Path(p).read_bytes() for p in args[:4])
    out = Path(args[4])
    template = "README.txt" if platform == "windows" else "README-macos.txt" if platform == "macos" else "README-linux.txt"
    readme = (HERE / template).read_text(encoding="ascii").format(
        version=VERSION, witness_sha256=sha256(witness), bitcoind_sha256=sha256(node),
        arch=platform.removeprefix("linux-"))
    witness_name = "Ark0Witness.exe" if platform == "windows" else "Ark0Witness" if platform == "macos" else "ark0-witness"
    node_name = "bitcoind.exe" if platform == "windows" else "bitcoind"
    text = crlf if platform == "windows" else lambda data: data.replace(b"\r\n", b"\n")
    files = [
        (witness_name, witness),
        (node_name, node),
        ("README.txt", text(readme.encode("ascii"))),
        ("COPYING.txt", text(copying)),
        ("LICENSE-mldsa-native.txt", text(mldsa)),
        ("LICENSE-Ark0Witness.txt", text((HERE.parent / "LICENSE").read_bytes())),
    ]
    programs = {witness_name, node_name}
    if platform.startswith("linux-"):
        pack_tar(out, f"ark0-witness-{VERSION}-{platform}/", files, programs)
    else:
        pack_zip(out, TOP if platform == "windows" else f"ark0-witness-{VERSION}-macos/", files, programs, platform)
    print(sha256(out.read_bytes()), out.name)


def pack_zip(out: Path, top: str, files: list, programs: set, platform: str) -> None:
    with zipfile.ZipFile(out, "w") as z:
        for name, data in files:
            info = zipfile.ZipInfo(top + name, STAMP)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 0 if platform == "windows" else 3
            info.external_attr = 0 if platform == "windows" else (stat.S_IFREG | (0o755 if name in programs else 0o644)) << 16
            z.writestr(info, data, compresslevel=9)


def pack_tar(out: Path, top: str, files: list, programs: set) -> None:
    with out.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, compresslevel=9, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as tar:
                for name, data in sorted(files):
                    info = tarfile.TarInfo(top + name)
                    info.size = len(data)
                    info.mode = 0o755 if name in programs else 0o644
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    info.mtime = MTIME
                    tar.addfile(info, io.BytesIO(data))


if __name__ == "__main__":
    main()

"""L2 of the M4-T lattice tripwires (design m4-design-2026-10-14.md §3.1): an ML-DSA-44 key whose private part
is destroyed right after it is made. The one program of the image built from l2/Dockerfile, run once by the
Job of l2/job.yaml:

    python3 -I -B /app/l2-keygen.py

It draws 32 bytes xi from the operating system (os.urandom: getrandom(2) on Linux), runs ML-DSA-44
KeyGen_internal(xi) (FIPS 204 Alg. 6, the FIPS 204 implementation next to it, test/py/core_m1/fips204.py of
p2mr-ts), prints exactly two lines and exits:

    mldsa44-public-key <2624 lowercase hex characters>
    mldsa44-public-key-sha256 <64 lowercase hex characters>

written as bytes with LF line ends, whatever the platform. Before xi is drawn the process makes itself not
dumpable (prctl(PR_SET_DUMPABLE, 0), checked with PR_GET_DUMPABLE; it refuses where that fails or on a system
other than Linux), so that no core dump can hold the key. Nothing else is printed, logged or written: no file
is opened for writing (the root file system is read-only, the only writable place an emptyDir in memory that
this program does not use), and the private
key and xi exist only in this process's memory. xi is kept in a bytearray that is overwritten before the
output is written; the bytes objects KeyGen_internal makes from it, the private key among them, cannot be
overwritten in Python, and go with the process, which ends right after. Whether the key is destroyed cannot
be proved (design §3.1): the operator's record says how it was run.
"""
import ctypes
import hashlib
import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from fips204 import ML_DSA  # noqa: E402

PK_LINE = "mldsa44-public-key"
SHA_LINE = "mldsa44-public-key-sha256"
PR_GET_DUMPABLE, PR_SET_DUMPABLE = 3, 4


def not_dumpable():
    """prctl(PR_SET_DUMPABLE, 0), checked (as ops/m4t-tripwires/nodump.py; inlined, the image holds two files)."""
    if not sys.platform.startswith("linux"):
        raise SystemExit("l2-keygen: runs on Linux only, where it can keep the key out of core dumps")
    libc = ctypes.CDLL(None, use_errno=True)
    libc.prctl(PR_SET_DUMPABLE, 0, 0, 0, 0)
    if libc.prctl(PR_GET_DUMPABLE, 0, 0, 0, 0) != 0:
        raise SystemExit("l2-keygen: the process could not be made not dumpable")


def keygen(urandom=os.urandom):
    """The public key of a fresh key and its SHA-256; xi and the private key are never returned."""
    not_dumpable()
    xi = bytearray(urandom(32))
    try:
        if len(xi) != 32 or not any(xi):
            raise SystemExit("l2-keygen: the operating system did not give 32 random bytes")
        pk, sk = ML_DSA("ML-DSA-44").keygen_internal(bytes(xi))
        del sk
        pk = bytes(pk)
        if len(pk) != 1312:
            raise SystemExit("l2-keygen: the public key is not 1312 bytes")
        return pk, hashlib.sha256(pk).hexdigest()
    finally:
        xi[:] = bytes(len(xi))


def main(out=None, urandom=os.urandom):
    """The two lines, as bytes with LF, to out (a binary stream; stdout's buffer by default)."""
    out = out or sys.stdout.buffer
    pk, digest = keygen(urandom)
    out.write(f"{PK_LINE} {pk.hex()}\n{SHA_LINE} {digest}\n".encode("ascii"))
    out.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main())

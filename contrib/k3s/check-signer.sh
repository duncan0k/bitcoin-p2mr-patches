#!/bin/sh
# check-signer.sh <signer>: refuse a signet-slh-sign that must not go into the
# producer image (doc/signet-slhblock.md of the M4-B series). The one entry
# point of this check: stage-miner.sh runs it on the staged copy (streamed out of
# the pod) and in its --self-test, and Dockerfile.miner runs it in the image
# build. A thin wrapper: the check is the Python below (python3, standard library
# only), run in isolated mode on the one file. <signer> may be /dev/stdin.
#
# Scope. The release signer is a static, non-PIE executable: it loads no
# library, so there are no dependencies to parse. This check verifies that (an
# x86-64 ELF64 little-endian ET_EXEC with neither PT_INTERP nor PT_DYNAMIC, its
# headers and its PT_LOAD segments within the file) and searches its one file
# for the test build's mark SIGNET_SLH_SIGN_SANITIZER_BUILD, the fault build's
# switch SIGNET_SLH_SIGN_TEST_FAULT, the runtime symbol prefixes (__asan_,
# __hwasan_, __ubsan_, __tsan_, __msan_, __lsan_, __sanitizer_, __safestack_,
# __morestack), and the strings the runtimes carry as data, which stripping
# does not remove (AddressSanitizer, HWAddressSanitizer, ThreadSanitizer,
# MemorySanitizer, LeakSanitizer, UndefinedBehaviorSanitizer, "ubsan: ", the
# prefix of UBSan's minimal runtime, "safestack CHECK failed", SafeStack's, and
# SPLIT_STACK_GUARD, libgcc's split-stack runtime's).
#
# Three mechanisms move stack frames out of the reach of the signer's stack
# scrub (burn_stack), leaving the key's copies there: AddressSanitizer's fake
# stack, SafeStack's unsafe stack and GCC's split stacks. The signer's source,
# Core's configure check and this check refuse all three (the source cannot see
# -fsplit-stack, for which no compiler defines a macro). The lists cover the
# runtimes known in Clang 18 and GCC 13: each holds at least one of these
# strings; the static release signer, glibc included, holds none. It stops a
# signer from a mistaken build or configuration from going into the image. It
# is not a defence against someone who controls the build host, nor against a
# runtime the lists do not know: for that, the guarantee is the release
# pipeline with its pinned toolchain and flags, and the image digest recorded
# in the runbook.
#
# Exit 0 only when every check passed; otherwise exit 1 and the reason on
# standard error.
set -u
[ "$#" = 1 ] || { echo "usage: check-signer.sh <signer>" >&2; exit 1; }
command -v python3 > /dev/null || { echo "$1: not staging it: python3 is not available to check it" >&2; exit 1; }
# The program goes in with -c, so that standard input stays free for a signer
# given as /dev/stdin.
program="$(cat <<'PYTHON'
import struct
import sys

path = sys.argv[1]

def refuse(reason):
    print(f"{path}: not staging it: {reason}", file=sys.stderr)
    sys.exit(1)

try:
    with open(path, "rb") as f:
        data = f.read()
except OSError as e:
    refuse(f"cannot read it ({e.strerror})")

# The ELF header: 64 bytes for ELF64.
if len(data) < 64:
    refuse("not an ELF file, or a truncated one (shorter than an ELF64 header)")
if data[:4] != b"\x7fELF":
    refuse("not an ELF file")
if data[4] != 2:
    refuse("not a 64-bit ELF file")
if data[5] != 1:
    refuse("not a little-endian ELF file")
if data[6] != 1:
    refuse("not ELF version 1 (EI_VERSION)")
(e_type, e_machine, e_version, e_entry, e_phoff, e_shoff, e_flags, e_ehsize,
 e_phentsize, e_phnum, e_shentsize, e_shnum, e_shstrndx) = struct.unpack_from("<HHIQQQIHHHHHH", data, 16)
if e_version != 1:
    refuse(f"not ELF version 1 (e_version {e_version})")
if e_type != 2:
    refuse(f"not a non-PIE executable (e_type {e_type}, ET_EXEC is 2): static-pie, dynamic and shared files are refused")
if e_machine != 62:
    refuse(f"not an x86-64 file (e_machine {e_machine})")
if e_ehsize != 64:
    refuse(f"an inconsistent ELF header size ({e_ehsize})")

# The program headers: all within the file. Python's integers do not overflow.
if e_phentsize != 56:
    refuse(f"an inconsistent program header size ({e_phentsize})")
if e_phnum == 0 or e_phnum == 0xffff:
    refuse(f"an unusable program header count ({e_phnum})")
if e_phoff < 64 or e_phoff + e_phnum * e_phentsize > len(data):
    refuse(f"program headers beyond the file (offset {e_phoff}, {e_phnum} of {e_phentsize} bytes, file {len(data)} bytes)")
loads = 0
for i in range(e_phnum):
    (p_type, p_flags, p_offset, p_vaddr, p_paddr, p_filesz, p_memsz, p_align) = \
        struct.unpack_from("<IIQQQQQQ", data, e_phoff + i * e_phentsize)
    if p_type == 3:
        refuse("it has a PT_INTERP: it is dynamically linked")
    if p_type == 2:
        refuse("it has a PT_DYNAMIC: it is not fully static")
    if p_type == 1:                                        # PT_LOAD
        loads += 1
        if p_offset + p_filesz > len(data):
            refuse(f"a PT_LOAD segment beyond the file (offset {p_offset}, {p_filesz} bytes, file {len(data)} bytes)")
        if p_filesz > p_memsz:
            refuse(f"a PT_LOAD segment larger in the file than in memory ({p_filesz} > {p_memsz})")
if loads == 0:
    refuse("no PT_LOAD segment")

# The same bytes: the marks, the runtime symbol prefixes, and the strings the
# runtimes carry as data, which stripping leaves in place.
for needle in (b"SIGNET_SLH_SIGN_SANITIZER_BUILD", b"SIGNET_SLH_SIGN_TEST_FAULT",
               b"__asan_", b"__hwasan_", b"__ubsan_", b"__tsan_", b"__msan_", b"__lsan_", b"__sanitizer_",
               b"AddressSanitizer", b"HWAddressSanitizer", b"ThreadSanitizer", b"MemorySanitizer",
               b"LeakSanitizer", b"UndefinedBehaviorSanitizer", b"ubsan: "):
    if needle in data:
        refuse(f"a sanitizer or fault test build (it contains {needle.decode()!r})")
for needle in (b"__safestack_", b"safestack CHECK failed"):
    if needle in data:
        refuse(f"a SafeStack build, whose unsafe stack the stack scrub does not reach (it contains {needle.decode()!r})")
for needle in (b"__morestack", b"SPLIT_STACK_GUARD"):
    if needle in data:
        refuse(f"a split-stack build, whose stack segments the stack scrub does not reach (it contains {needle.decode()!r})")
sys.exit(0)
PYTHON
)" || exit 1
exec python3 -I -c "$program" "$1"

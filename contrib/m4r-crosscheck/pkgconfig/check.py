import os, shlex, subprocess, sys
d, module = sys.argv[1], sys.argv[2]
readers = sys.argv[3:] or ["pkgconf", "pkg-config"]
ok = True

def gen(threads, bad=None):
    cmd = ["cmake", f"-DMODULE={module}", f"-DOUT={d}/t.pc", f"-DTHREADS={threads}"]
    if bad is not None:
        cmd.append(f"-DBAD={bad}")
    return subprocess.run(cmd + ["-P", f"{d}/gen.cmake"], capture_output=True, text=True)

for reader in readers:
    version = subprocess.run([reader, "--version"], capture_output=True, text=True, check=True).stdout.strip()
    print(f"{reader} {version} ({os.path.realpath(subprocess.run(['sh', '-c', f'command -v {reader}'], capture_output=True, text=True).stdout.strip())})")
    for threads, tflags in {"-pthread": ["-pthread"], "": []}.items():
        r = gen(threads)
        if r.returncode != 0:
            print(r.stderr); sys.exit(1)
        line = open(f"{d}/t.pc").read().splitlines()[-1]
        out = subprocess.run([reader, "--static", "--libs", "t"], env=dict(os.environ, PKG_CONFIG_PATH=d),
                             capture_output=True, text=True, check=True).stdout
        got = shlex.split(out)  # as a POSIX shell reads it
        want = ["-lt", "-lbitcoinkernel_voleith"] + tflags + [
            "-framework", "CoreFoundation", "-lm", "-Wl,-z,relro,-z,now", "-l:libfoo.a", "-lkernel32"] + (
            ["-pthread"] if not tflags else []) + ["-l" + n for n in (
            "dl rt System bcrypt advapi32 ntdll userenv ws2_32 synchronization Bcrypt").split()]
        print(f"  CMAKE_THREAD_LIBS_INIT={threads!r}\n    .pc:    {line}\n    args:   {got}")
        if got == want:
            print(f"    all {len(want)} arguments as expected")
        else:
            ok = False
            print(f"    DIFFERS: want {want}")
# Refused entries: the configuration must stop with the message naming the entry. No path is taken,
# absolute or relative, nor a fragment of a path a CMake list split at ";", nor another target.
for bad in ["/usr/lib/gcc/x86_64-linux-gnu/13/libstdc++.a", "b/libx.a", "/opt/a b/libs.a", "/opt/x$y/liby.a",
            "/System/Library/Frameworks/Foo.framework", "C:/msys64/mingw64/lib/libstdc++.a", "Foo::Bar", "lib$x",
            "stdc++ m"]:
    r = gen("-pthread", bad)
    refused = (r.returncode != 0 and "libbitcoinkernel.pc cannot carry the link entry" in r.stderr
               and bad in r.stderr)
    ok &= refused
    print(f"  refused {bad!r}: {'yes' if refused else 'NO'}")
    if bad in ("b/libx.a", "/usr/lib/gcc/x86_64-linux-gnu/13/libstdc++.a"):
        print("    " + r.stderr.strip().replace("\n", "\n    "))
# The review's case, called directly: the module sees "/opt/a" and "b/libx.a", and refuses the first.
with open(f"{d}/split.cmake", "w") as f:
    f.write(f'include({module})\npkgconfig_libs(x "/opt/a;b/libx.a")\n')
r = subprocess.run(["cmake", "-P", f"{d}/split.cmake"], capture_output=True, text=True)
split = r.returncode != 0 and "cannot carry the link entry" in r.stderr and "    /opt/a\n" in r.stderr
ok &= split
print(f"  refused pkgconfig_libs(x \"/opt/a;b/libx.a\") (naming '/opt/a'): {'yes' if split else 'NO'}")
print("PASS" if ok else "FAIL")
sys.exit(0 if ok else 1)

# The static kernel's Libs.private on sample entries

`gen.cmake` runs `pkgconfig_libs` of `cmake/module/PkgConfigLibs.cmake` (patch 0079) on sample link
entries:
- names, among them those the Rust library's and the kernel's lists hold on Linux, macOS, MinGW and
  MSVC (`dl`, `rt`, `m`, `System`, `bcrypt`, `ws2_32`, `kernel32.lib`, …);
- `Threads::Threads`, with `CMAKE_THREAD_LIBS_INIT` set and empty;
- the options `-framework CoreFoundation`, `-Wl,-z,relro,-z,now` and `-l:libfoo.a`.

It writes a `.pc` file. `check.py` reads it with each reader named (by default `pkgconf` and
`pkg-config`), splits the output as a POSIX shell does, and compares the arguments with the expected
ones: one per entry, two for `-framework CoreFoundation`.

The module takes no paths. `check.py` gives `gen.cmake` absolute and relative paths, a framework given by
path, a target name and names with other characters, one at a time, and checks that each stops the
configuration with the message naming the entry. It also checks the case of the review, called
directly: `pkgconfig_libs(x "/opt/a;b/libx.a")`.

```bash
python3 check.py <a new directory with gen.cmake> <Core tree>/cmake/module/PkgConfigLibs.cmake [reader...]
```

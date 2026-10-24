cmake_minimum_required(VERSION 3.22)
# cmake -DMODULE=<PkgConfigLibs.cmake> -DOUT=<pc file> -DTHREADS=<CMAKE_THREAD_LIBS_INIT> [-DBAD=<entry>] -P gen.cmake
# Without BAD: the accepted samples. With BAD: that one entry after a name, which the module must refuse.
include(${MODULE})
set(CMAKE_THREAD_LIBS_INIT "${THREADS}")
if(DEFINED BAD)
  pkgconfig_libs(libs m "${BAD}")
else()
  pkgconfig_libs(libs
    bitcoinkernel_voleith
    Threads::Threads
    -framework\ CoreFoundation
    m
    "-Wl,-z,relro,-z,now"
    -l:libfoo.a
    kernel32.lib
    -pthread
    # Names the Rust library's and the kernel's lists hold on Linux, macOS, MinGW and MSVC.
    dl rt System bcrypt advapi32 ntdll userenv ws2_32 synchronization kernel32 Bcrypt.lib
    m
  )
endif()
file(WRITE ${OUT} "Name: t\nDescription: t\nVersion: 1\nLibs: -lt\nLibs.private: ${libs}\n")

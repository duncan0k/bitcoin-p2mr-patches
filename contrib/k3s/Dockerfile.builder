# Build environment for the P2MR (BIP-360) patch series on Bitcoin Core v31.1.
#
# Contains everything Core v31.1 needs to configure, build and run both the
# unit tests (build/bin/test_bitcoin) and the functional test suite
# (build/test/functional/test_runner.py).
#
# Built in-cluster with kaniko and imported into containerd; see README.md.
# The image carries no source and no patches: those live on the work PVC.
#
# Pinning: the base image is pinned by digest. Individual apt versions are not
# pinned, because the noble-updates pocket drops superseded versions within
# weeks and the Dockerfile would stop building. Instead the exact version of
# every installed package is recorded in /opt/p2mr/packages.txt inside the
# image, so any build can be described exactly after the fact, and re-created
# from snapshot.ubuntu.com if that is ever needed.

ARG BASE_IMAGE=docker.io/library/ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254
FROM ${BASE_IMAGE}
# Re-declared so the value is also visible to the RUN below, which records it.
ARG BASE_IMAGE

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

# Toolchain and build dependencies of Bitcoin Core v31.1:
#   build-essential  gcc/g++ 13 (C++20), make, libc headers
#   cmake            3.28 (Core requires >= 3.22)
#   pkgconf          dependency discovery
#   libevent-dev     >= 2.1.8, required
#   libboost-dev     >= 1.73 headers; also supplies the header-only Boost.Test
#                    that build/bin/test_bitcoin is written against
#   libsqlite3-dev   descriptor wallet storage (-DENABLE_WALLET=ON)
#   libzmq3-dev      optional ZMQ notifications (-DWITH_ZMQ=ON); the default
#                    build in build.sh leaves ZMQ off, matching apply.sh
#   ccache           compiler cache, kept on the work PVC between runs
# Functional test suite:
#   python3          >= 3.10, the test framework is stdlib-only ...
#   python3-zmq      ... except interface_zmq.py, which skips itself without it
#   bsdextrautils    hexdump, used by a few of the tool_* tests
#   procps           ps, used when a test framework failure has to be triaged
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        build-essential \
        bsdextrautils \
        ca-certificates \
        ccache \
        cmake \
        curl \
        git \
        libboost-dev \
        libevent-dev \
        libsqlite3-dev \
        libzmq3-dev \
        pkgconf \
        procps \
        python3 \
        python3-zmq \
    ; \
    rm -rf /var/lib/apt/lists/*; \
    mkdir -p /opt/p2mr; \
    dpkg-query -W -f='${binary:Package}=${Version}\n' | sort > /opt/p2mr/packages.txt; \
    { \
        echo "base_image=${BASE_IMAGE}"; \
        echo "built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
        echo "cmake=$(cmake --version | head -1)"; \
        echo "gxx=$(g++ --version | head -1)"; \
        echo "python=$(python3 --version)"; \
    } > /opt/p2mr/toolchain.txt

# ccache defaults; the Job overrides CCACHE_DIR to a directory on the work PVC.
ENV CCACHE_DIR=/work/ccache \
    CCACHE_MAXSIZE=20G \
    CCACHE_COMPILERCHECK=content

WORKDIR /work
CMD ["/bin/bash"]

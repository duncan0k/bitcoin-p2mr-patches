#!/bin/sh
# Builds Ark0Witness reproducibly: the same Go release and the same source give the same bytes, whatever the
# environment, the checkout or its version control state.
#   sh build.sh windows <SHA-256 of bitcoind.exe>   writes out/Ark0Witness.exe
#   sh build.sh linux <SHA-256 of bitcoind>         writes out/ark0-witness
# Without the hash, the program runs whatever bitcoind lies next to it; release builds always pass it.
set -eu
cd "$(dirname "$0")"
target=${1:?windows or linux}
node_sha256=${2:-}
case $target in
windows) out=out/Ark0Witness.exe ;;
linux) out=out/ark0-witness ;;
*)
	echo "unknown target: $target" >&2
	exit 2
	;;
esac
if [ -n "$node_sha256" ] && ! printf '%s\n' "$node_sha256" | grep -Eqx '[0-9a-f]{64}'; then
	echo "not a SHA-256: $node_sha256" >&2
	exit 2
fi
mkdir -p out
# The whole build environment is set before go first runs, so that nothing from outside changes the bytes and no
# other Go release is fetched: no go env file (an empty GOEXPERIMENT would otherwise fall back to it), no
# go.work around the module.
export GOENV=off GOWORK=off GO111MODULE=on CGO_ENABLED=0 GOOS=$target GOARCH=amd64 GOAMD64=v1 GOEXPERIMENT= \
	GOFIPS140=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local GOPROXY=off
go version
go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.version=$(cat VERSION) -X main.bitcoindSHA256=$node_sha256" -o "$out" .
sha256sum "$out"

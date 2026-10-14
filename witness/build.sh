#!/bin/sh
# Builds Ark0Witness reproducibly: the same Go release and the same source give the same bytes.
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
CGO_ENABLED=0 GOOS=$target GOARCH=amd64 GOFLAGS=-mod=readonly GOTOOLCHAIN=local GOPROXY=off \
	go build -trimpath -ldflags "-s -w -buildid= -X main.version=$(cat VERSION) -X main.bitcoindSHA256=$node_sha256" -o "$out" .
sha256sum "$out"

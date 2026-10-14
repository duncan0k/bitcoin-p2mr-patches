#!/bin/sh
# Builds Ark0Witness reproducibly with the same Go release and source, whatever the checkout or environment.
#   sh build.sh windows <SHA-256 of bitcoind.exe>   writes out/Ark0Witness.exe
#   sh build.sh linux-amd64 <SHA-256 of bitcoind>   writes out/linux-amd64/ark0-witness
#   sh build.sh linux-arm64 <SHA-256 of bitcoind>   writes out/linux-arm64/ark0-witness
#   sh build.sh darwin <SHA-256 of universal bitcoind> writes out/darwin/Ark0Witness, using llvm-lipo or lipo
# The darwin target also writes out/darwin-amd64/Ark0Witness and out/darwin-arm64/Ark0Witness.
# linux is an alias of linux-amd64. Without a hash, only development builds run the shipped file directly.
set -eu
cd "$(dirname "$0")"
target=${1:?windows, linux-amd64, linux-arm64 or darwin}
node_sha256=${2:-}
case $target in
windows|linux-amd64|linux-arm64|darwin) ;;
linux) target=linux-amd64 ;;
*)
	echo "unknown target: $target" >&2
	exit 2
	;;
esac
if [ -n "$node_sha256" ] && ! printf '%s\n' "$node_sha256" | grep -Eqx '[0-9a-f]{64}'; then
	echo "not a SHA-256: $node_sha256" >&2
	exit 2
fi
# The whole build environment is set before go first runs, so that nothing outside changes the bytes, no
# other Go release is fetched, and no go env file or go.work around the module affects the build.
export GOENV=off GOWORK=off GO111MODULE=on CGO_ENABLED=0 GOAMD64=v1 GOARM64=v8.0 GOEXPERIMENT= \
	GOFIPS140=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local GOPROXY=off
go version
build() {
	export GOOS=$1 GOARCH=$2
	out=$3
	mkdir -p "$(dirname "$out")"
	go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.version=$(cat VERSION) -X main.bitcoindSHA256=$node_sha256" -o "$out" .
	sha256sum "$out"
}
case $target in
windows) build windows amd64 out/Ark0Witness.exe ;;
linux-amd64) build linux amd64 out/linux-amd64/ark0-witness ;;
linux-arm64) build linux arm64 out/linux-arm64/ark0-witness ;;
darwin)
	if command -v llvm-lipo >/dev/null 2>&1; then
		lipo_tool=llvm-lipo
	elif command -v lipo >/dev/null 2>&1; then
		lipo_tool=lipo
	else
		echo "darwin requires llvm-lipo or lipo to combine the two architectures" >&2
		exit 2
	fi
	build darwin amd64 out/darwin-amd64/Ark0Witness
	build darwin arm64 out/darwin-arm64/Ark0Witness
	mkdir -p out/darwin
	"$lipo_tool" -create out/darwin-amd64/Ark0Witness out/darwin-arm64/Ark0Witness -output out/darwin/Ark0Witness
	sha256sum out/darwin/Ark0Witness
	;;
esac

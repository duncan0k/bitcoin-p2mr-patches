# Ark0Witness

A small Windows program for watching the Ark-0 M3 drill with a node of your own, without typing commands.
Double-click `Ark0Witness.exe` and it:

- starts the Ark-0 node that ships next to it (`bitcoind.exe`, the M3 release built for Windows) in the
  background, with the settings of [`ark0/JOIN.md`](../ark0/JOIN.md);
- opens a page in your browser, served on 127.0.0.1 only, that shows what that node reports: how far it has
  synced, whether it is on Ark-0 (the hash of block 1264), block 30000 where M3 applies, the two tripwires,
  and whether elliptic curve signatures in leaves of version `0xe0` are still accepted;
- lets you paste a raw transaction for the node to judge with `testmempoolaccept`. Nothing is ever sent;
- gives a short witness report to share. It holds no IP address and no folder name.

Closing the program's console window stops the node; a node still loading then is ended, and picks up where
it left off at the next start. The node is tied to the program: however the program ends, Windows ends the
node with it (a job object), so no node is left running hidden. The node keeps its data in
`%LOCALAPPDATA%\Ark0Witness` for the next start; to remove everything, close the window, wait a few
seconds, and delete that folder and the program's folder.

Ark-0 is a test network for trying things out. It may be reset at any time.

## What it does and does not do

- No wallet, no keys, no sending. The only node calls are `getblockchaininfo`, `getconnectioncount`,
  `getblockhash`, `getnetworkinfo`, `gettripwireinfo`, `testmempoolaccept` (with a maximum fee rate of 0,
  so that only the rules decide) and `stop`.
- Nothing listens for other computers. The node runs with `-listen=0`, and its RPC listens on 127.0.0.1 with
  the cookie the node writes into its data folder; with `-noconf -nosettings`, no `bitcoin.conf` or
  `settings.json` in that folder can add to that. The program trusts the RPC port only once the node it
  started has written its cookie, which the node does only after binding the port, and follows no redirect.
- The page is served on 127.0.0.1, on a port the system picks, only under a random 256-bit path, and only to
  requests whose Host is that address: another site's page cannot find it or reach it under a name of its own.
- No update checks, no statistics, no other network traffic than the node's own.
- The program is built with the SHA-256 of the `bitcoind.exe` it ships with and starts no other: it holds the
  file open against changes while it checks and starts it.
- What the page calls good rests on the node's own answers: it says so only once the node has confirmed
  block 1264 and reported both tripwires; anything it could not read shows as unknown, also in the report.
- A second start while the program runs shows the running one's page.

## Before you run it on Windows

The program is not code-signed. On first start, Windows SmartScreen may say "Windows protected your PC":
click "More info", then "Run anyway". Check the zip's SHA-256 against the `SHA256SUMS` of the release, whose
signature you can check with the release key as `ark0/JOIN.md` describes. It needs 64-bit Windows 10 or 11
on an Intel or AMD processor.

## Build

Go 1.26, standard library only:

```sh
sh build.sh windows <SHA-256 of bitcoind.exe>   # out/Ark0Witness.exe
sh build.sh linux <SHA-256 of bitcoind>         # out/ark0-witness, the same program for Linux, for tests
```

The build is reproducible: the same Go release and the same source give the same bytes, whatever the checkout.
Version 0.1.0 is built with go1.26.9.

## Test

`go test ./...` covers the node's arguments, the RPC client, the status and report, the page server's
boundaries (Host, path, methods, content type, origin), the strings of the six languages and, with the test
program standing in for bitcoind, how the node is started, trusted and stopped.
`test/e2e-linux.sh` runs the Linux build against the Linux M3 release, syncing from `node.ark0.si`.

## License

MIT, see [`LICENSE`](LICENSE).

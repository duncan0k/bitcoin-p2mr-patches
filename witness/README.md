# Ark0Witness

A small program for Windows, macOS and Linux for watching the Ark-0 M3 drill with a node of your own, without
typing commands. Start it (on Windows and macOS with a double-click, on Linux from a terminal) and it:

- starts the Ark-0 node that ships next to it (`bitcoind`, the code of the M3 release built for that system)
  in the background, with the settings of [`ark0/JOIN.md`](../ark0/JOIN.md);
- opens a page in your browser, served on 127.0.0.1 only, that shows what that node reports: how far it has
  synced, whether it is on Ark-0 (the hash of block 1264), block 30000 where M3 applies, the two tripwires,
  and whether elliptic curve signatures in leaves of version `0xe0` are still accepted;
- lets you paste a raw transaction for the node to judge with `testmempoolaccept`. It is never broadcast;
- gives a short witness report to share. It holds no IP address and no folder name.

The program is `Ark0Witness.exe` on Windows, `Ark0Witness` on macOS and `ark0-witness` on Linux; the node next
to it is `bitcoind.exe` on Windows and `bitcoind` on the other two.

Closing the program's window stops the node; on macOS and Linux, Ctrl+C in it does too.

- On Windows, a node that is still loading at that moment, or still shutting down a few seconds later, is
  ended; at the next start it carries on from what it last saved. The node is tied to the program: however the
  program ends, Windows ends the node with it (a job object), so no node is left running hidden. One gap is
  left: if the program is killed in the instant between starting the node and tying it to the program, a
  suspended node process that has run no code stays behind; end it in Task Manager before deleting the
  program's folder.
- On macOS and Linux, the node runs in a process group of its own, so that Ctrl+C and a closed terminal reach
  only the program, which then asks the node to stop, waits up to a minute and then sends it SIGTERM; the node
  finishes its shutdown on its own. On Linux the node also gets SIGTERM if the program dies. The program
  records the node's process id in `node.pid` in its data folder, and at its next start stops a node left
  from an earlier run, but only a process that runs its own copy of the node. On macOS, if the program is
  killed in the instant between starting the node and recording it, that node keeps running unrecorded: quit
  `bitcoind` in Activity Monitor, or restart the Mac.

The node keeps its data for the next start in `%LOCALAPPDATA%\Ark0Witness` on Windows,
`~/Library/Application Support/Ark0Witness` on macOS and `~/.local/share/Ark0Witness` on Linux
(`$XDG_DATA_HOME/Ark0Witness` if `XDG_DATA_HOME` is set to an absolute path); `-datadir` chooses another
folder. To remove everything, stop the program, wait until it has exited, and delete that folder and the
program's folder.

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
  Other users of the computer can read the command lines of its programs, so on macOS the program hands the
  address to the browser without putting it on one, and on Linux, where the browser's own command line may
  hold it, the page answers only to programs of the user who started it, or of root.
- No update checks, no statistics, no other network traffic than the node's own.
- The program is built with the SHA-256 of the node it ships with and starts no other.
  - On Windows it holds `bitcoind.exe` open, so that no other program can change it, while it checks the
    file, starts the node suspended and checks with Windows that the node runs that very file; only then does
    it let the node run. That check needs file ids, which FAT32 and exFAT drives do not give (see below).
  - On macOS and Linux it copies `bitcoind` into the folder `node` of its data folder, hashing the copy as it
    writes it, and starts that copy. It refuses a data folder that another user could change, or replace
    through a folder above it, or that holds anything another user could have put there (a link, a file of
    another owner, a file others can write), and a copy that is not its own. On Linux it starts the copy from
    the very file it checked, through `/proc/self/fd`. On macOS the copy also spares a second prompt from
    Gatekeeper: a file the program writes carries no quarantine attribute.
- What the page calls good rests on the node's own answers: it says so only once the node has confirmed
  block 1264 and reported both tripwires; anything it could not read shows as unknown, also in the report.
- A second start while the program runs shows the running one's page.

## Before you run it

Check the download's SHA-256 against the `SHA256SUMS` of the release, whose signature you can check with the
release key as `ark0/JOIN.md` describes. None of the programs is code-signed or notarized.

**Windows.** It needs 64-bit Windows 10 or 11 on an Intel or AMD processor. Unzip it onto the computer's own
drive (NTFS), not onto a FAT32 or exFAT drive such as many USB sticks: there the program cannot check the node
and never lets it run, and its window says that the node has stopped, followed by a Windows error such as "The
parameter is incorrect." On first start, Windows SmartScreen may say "Windows protected your PC": click "More
info", then "Run anyway". With Smart App Control on, Windows may block the program and offer no such choice;
on such a computer, run the Linux build in WSL and follow `ark0/JOIN.md` instead.

**macOS.** It needs macOS 14 (Sonoma) or later; both programs are universal, for Apple silicon and Intel. A
download from a browser is quarantined, so at the first start macOS says it cannot verify `Ark0Witness`: click
"Done" (on macOS 14, "Cancel"), open System Settings, Privacy & Security, click "Open Anyway" at the message
about `Ark0Witness` and confirm, then start it again and click "Open Anyway" (on macOS 14, "Open"). The node
asks for nothing, since the program runs its own copy. A download made with `curl` in Terminal is not
quarantined, and macOS does not ask.

**Linux.** It needs 64-bit Linux on x86_64 or aarch64 with glibc 2.35 or later, such as Ubuntu 22.04,
Debian 12 or Raspberry Pi OS (bookworm), or later releases; the node carries its own C++ runtime. Unpack the
tarball with `tar -xzf` and run `./ark0-witness` in a terminal. If other users could change a folder above its
data folder, as with a group-writable `~/.local` in a home folder that others can enter, it stops and prints
the `chmod` command that fixes it. With a desktop, the page opens in the browser; without one, open the printed
address in a browser on the same computer, or from another computer through `ssh -L <port>:127.0.0.1:<port>`
with the same port number, since the page answers only to its own address.

## Build

Go 1.26, standard library only:

```sh
sh build.sh windows <SHA-256 of bitcoind.exe>           # out/Ark0Witness.exe
sh build.sh darwin <SHA-256 of the universal bitcoind>  # out/darwin/Ark0Witness, with llvm-lipo or lipo
sh build.sh linux-amd64 <SHA-256 of bitcoind>           # out/linux-amd64/ark0-witness
sh build.sh linux-arm64 <SHA-256 of bitcoind>           # out/linux-arm64/ark0-witness
```

The build is reproducible: the same Go release and the same source give the same bytes, whatever the checkout
and the system it runs on. Version 0.2.0 is built with go1.26.9. `dist/package.py` then packs the zip for
Windows and for macOS and the tarballs for Linux, the same bytes on every run: the two programs, the README for
that system (`dist/README*.txt`) with their SHA-256, and the licenses. The nodes are built by
`contrib/windows/build.sh` and `contrib/cross/build.sh` at the root of this repository; the two macOS builds
are joined into one universal file by `contrib/cross/lipo.sh`.

## Test

`go test ./...` covers the node's arguments, the RPC client, the status and report, the page server's
boundaries (Host, path, methods, content type, origin), the strings of the six languages and, with the test
program standing in for bitcoind, how the node is started, trusted and stopped; on macOS and Linux also the
data folder rules, the private copy, the signals and the left-over node.
`test/e2e-linux.sh` runs the Linux build against the Linux M3 release, syncing from `node.ark0.si`.
The tests pass on Windows 11 and Windows 10 22H2, macOS 26 on Apple silicon (the Intel build under Rosetta
too) and Linux x86_64. The program synced Ark-0 with its node on each of them; the Linux aarch64 node synced
Ark-0 and passed Core's unit tests under qemu-user.

## License

MIT, see [`LICENSE`](LICENSE).

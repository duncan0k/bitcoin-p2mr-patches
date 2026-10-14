Ark0Witness {version} for macOS

Watch the Ark-0 M3 drill with a node of your own. Ark-0 is a test network for trying things out;
it may be reset at any time.

1. Unzip the whole download into a folder. Both programs are universal binaries for Intel
   and Apple Silicon Macs.
2. Open Terminal in that folder and run ./Ark0Witness. The program has no Apple Developer ID
   signature; macOS may block the first start.
3. Your browser shows the status page. Keep the terminal open: closing it stops the node.
   Ctrl+C also stops the node. If the browser does not open, copy the printed page address.

The page shows how far your node has synced, whether it is on Ark-0, block 30000 where M3
applies and the two tripwires, and has your node judge a raw transaction you paste into
it, which is never broadcast. All times are UTC.

The node runs from a checked private copy in ~/Library/Application Support/Ark0Witness/node.
The program asks the node to stop and waits up to 60 seconds, then sends SIGTERM if it is
still running. A node left after the witness is killed is stopped on the next start.
To remove everything: stop the witness, wait for bitcoind to exit, then delete this folder
and ~/Library/Application Support/Ark0Witness, where the node keeps its data.

Files
  Ark0Witness              the witness program: witness/ in
                            https://github.com/duncan0k/bitcoin-p2mr-patches
  bitcoind                 the Ark-0 node, milestone M3: Bitcoin Core v31.1 with the patch
                            sets of that repository (tag ark0-node-m3), for macOS
  COPYING.txt               Bitcoin Core's license (MIT)
  LICENSE-mldsa-native.txt  license of the ML-DSA-44 verifier in bitcoind
                            (Apache-2.0 OR ISC OR MIT)
  LICENSE-Ark0Witness.txt   the witness program's license (MIT)

SHA-256
  {witness_sha256}  Ark0Witness
  {bitcoind_sha256}  bitcoind

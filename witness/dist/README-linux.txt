Ark0Witness {version} for Linux

Watch the Ark-0 M3 drill with a node of your own. Ark-0 is a test network for trying things out;
it may be reset at any time.

1. Extract the whole .tar.gz download into a folder. Use the x86_64 download for 64-bit Intel
   or AMD processors, or the aarch64 download for 64-bit ARM processors.
2. Open a terminal in that folder and run ./ark0-witness. The checked node starts through
   /proc/self/fd; procfs must be mounted.
3. Your browser shows the status page through xdg-open. Keep the terminal open: closing it
   stops the node. Ctrl+C also stops it. If no browser opens, copy the printed page address.

The page shows how far your node has synced, whether it is on Ark-0, block 30000 where M3
applies and the two tripwires, and has your node judge a raw transaction you paste into
it, which is never broadcast. All times are UTC.

Data is kept in $XDG_DATA_HOME/Ark0Witness when XDG_DATA_HOME is an absolute path, otherwise
in ~/.local/share/Ark0Witness. The node runs from a checked private copy in its node folder.
The program asks the node to stop and waits up to 60 seconds, then sends SIGTERM if it is
still running. Linux also sends SIGTERM if the witness dies. A surviving node is stopped
on the next start.
To remove everything: stop the witness, wait for bitcoind to exit, then delete this folder
and the data folder above. The -datadir flag selects a different data folder.

Files
  ark0-witness             the witness program: witness/ in
                            https://github.com/duncan0k/bitcoin-p2mr-patches
  bitcoind                 the Ark-0 node, milestone M3: Bitcoin Core v31.1 with the patch
                            sets of that repository (tag ark0-node-m3), for Linux
  COPYING.txt               Bitcoin Core's license (MIT)
  LICENSE-mldsa-native.txt  license of the ML-DSA-44 verifier in bitcoind
                            (Apache-2.0 OR ISC OR MIT)
  LICENSE-Ark0Witness.txt   the witness program's license (MIT)

SHA-256
  {witness_sha256}  ark0-witness
  {bitcoind_sha256}  bitcoind

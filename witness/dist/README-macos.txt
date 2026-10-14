Ark0Witness {version} for macOS

Watch the Ark-0 M3 drill with a node of your own. Ark-0 is a test network for trying things out;
it may be reset at any time.

For macOS 14 (Sonoma) or later, on Apple silicon and Intel Macs alike.

1. Unzip the download (Safari usually does that by itself), then double-click Ark0Witness
   in the folder ark0-witness-{version}-macos. Keep the files of this folder together.
2. The program is not notarized by Apple, so the first time macOS says it cannot verify
   "Ark0Witness" and offers only "Done" and "Move to Trash": click Done. Then open System
   Settings, Privacy & Security, scroll down to the message that "Ark0Witness" was blocked,
   click "Open Anyway" and confirm with your password. Double-click Ark0Witness again and
   click "Open Anyway". macOS may also ask whether Terminal may access files in your
   Downloads folder: click Allow.
3. A Terminal window opens and your browser shows the status page. Keep the window open:
   closing it stops the node. Terminal asks first; click Terminate.

The page shows how far your node has synced, whether it is on Ark-0, block 30000 where M3
applies and the two tripwires, and has your node judge a raw transaction you paste into
it, which is never broadcast. All times are UTC.

The node needs no approval of its own: the witness checks bitcoind against the SHA-256
below and runs a copy of it that it keeps in its data folder.

To remove everything: close the Terminal window, wait a minute for the node to stop, then
delete this folder and the folder ~/Library/Application Support/Ark0Witness, where the node
keeps its data (in the Finder, choose Go > Go to Folder and paste that path).

Files
  Ark0Witness               the witness program: witness/ in
                            https://github.com/duncan0k/bitcoin-p2mr-patches
  bitcoind                  the Ark-0 node, milestone M3: Bitcoin Core v31.1 with the patch
                            sets of that repository (tag ark0-node-m3), for macOS 14 and
                            later
  COPYING.txt               Bitcoin Core's license (MIT)
  LICENSE-mldsa-native.txt  license of the ML-DSA-44 verifier in bitcoind
                            (Apache-2.0 OR ISC OR MIT)
  LICENSE-Ark0Witness.txt   the witness program's license (MIT)

Both programs are universal: one file holds the Apple silicon and the Intel build.

SHA-256
  {witness_sha256}  Ark0Witness
  {bitcoind_sha256}  bitcoind

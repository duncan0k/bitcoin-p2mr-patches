Ark0Witness {version} for Windows

Watch the Ark-0 M3 drill with a node of your own. Ark-0 is a test network for trying things out;
it may be reset at any time.

1. Unzip the whole download into a folder on the computer's own drive (right-click,
   Extract All), then double-click Ark0Witness.exe in that folder. From a FAT32 or exFAT
   drive, such as many USB sticks, the node does not start: the window says the node has
   stopped, with a Windows error such as "The parameter is incorrect."
2. Windows may say "Windows protected your PC", because the program is not code-signed:
   click "More info", then "Run anyway". With Smart App Control on, Windows may block the
   program outright; then run the Linux build in WSL instead, following ark0/JOIN.md in
   the repository below.
3. A console window opens and your browser shows the status page. Keep the window open:
   closing it stops the node.

The page shows how far your node has synced, whether it is on Ark-0, block 30000 where M3
applies and the two tripwires, and has your node judge a raw transaction you paste into
it, which is never broadcast. All times are UTC.

To remove everything: close the console window, wait a few seconds, then delete this folder
and the folder %LOCALAPPDATA%\Ark0Witness, where the node keeps its data.

Files
  Ark0Witness.exe           the witness program: witness/ in
                            https://github.com/duncan0k/bitcoin-p2mr-patches
  bitcoind.exe              the Ark-0 node, milestone M3: Bitcoin Core v31.1 with the patch
                            sets of that repository (tag ark0-node-m3), for 64-bit Windows
                            10 and 11
  COPYING.txt               Bitcoin Core's license (MIT)
  LICENSE-mldsa-native.txt  license of the ML-DSA-44 verifier in bitcoind.exe
                            (Apache-2.0 OR ISC OR MIT)
  LICENSE-Ark0Witness.txt   the witness program's license (MIT)

SHA-256
  {witness_sha256}  Ark0Witness.exe
  {bitcoind_sha256}  bitcoind.exe

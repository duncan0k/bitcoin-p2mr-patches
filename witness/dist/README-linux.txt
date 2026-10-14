Ark0Witness {version} for Linux ({arch})

Watch the Ark-0 M3 drill with a node of your own. Ark-0 is a test network for trying things out;
it may be reset at any time.

For 64-bit Linux with glibc 2.35 or later, such as Ubuntu 22.04, Debian 12 and Raspberry Pi
OS (bookworm) or later releases. The x86_64 download is for Intel and AMD processors, the
aarch64 download for 64-bit ARM, such as a Raspberry Pi with the 64-bit system.

1. Unpack the download and start the program in a terminal:
     tar -xzf ark0-witness-{version}-linux-{arch}.tar.gz
     cd ark0-witness-{version}-linux-{arch}
     ./ark0-witness
2. On a desktop, your browser shows the status page. Otherwise, open the address the program
   prints in a browser on the same computer. Keep the terminal open: closing it, or pressing
   Ctrl+C, stops the node.

The page shows how far your node has synced, whether it is on Ark-0, block 30000 where M3
applies and the two tripwires, and has your node judge a raw transaction you paste into
it, which is never broadcast. All times are UTC.

The page answers on this computer only. To see it from another computer, forward the same
port number over SSH. For example, if the program printed http://127.0.0.1:41234/..., run
     ssh -L 41234:127.0.0.1:41234 you@this-computer
on the other computer and open the printed address there, unchanged. To keep the witness
running after you log out, start it inside tmux or screen.

To remove everything: stop the program, wait up to a minute until it has exited, then delete
this folder and the folder ~/.local/share/Ark0Witness, where the node keeps its data
($XDG_DATA_HOME/Ark0Witness instead, if you set XDG_DATA_HOME).

Files
  ark0-witness              the witness program: witness/ in
                            https://github.com/duncan0k/bitcoin-p2mr-patches
  bitcoind                  the Ark-0 node, milestone M3: Bitcoin Core v31.1 with the patch
                            sets of that repository (tag ark0-node-m3), for 64-bit Linux
                            ({arch})
  COPYING.txt               Bitcoin Core's license (MIT)
  LICENSE-mldsa-native.txt  license of the ML-DSA-44 verifier in bitcoind
                            (Apache-2.0 OR ISC OR MIT)
  LICENSE-Ark0Witness.txt   the witness program's license (MIT)

SHA-256
  {witness_sha256}  ark0-witness
  {bitcoind_sha256}  bitcoind

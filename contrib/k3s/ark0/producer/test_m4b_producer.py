#!/usr/bin/env python3
"""Test the M4-B changes of ark0.py against a local node, with ark0.py itself.

Runs on a workstation, never in the cluster, and touches nothing but a temporary
directory: one bitcoind of an M4-B build on a custom signet with a 1-of-1 bare
multisig challenge like Ark-0's, -signetpqblock and -signetslhblock; test keys
made from public strings.

  python3 test_m4b_producer.py --builddir <core>/build --srcdir <core>
  python3 -O test_m4b_producer.py ...     the gates only: they are require(), so -O
                                          changes nothing. ark0.py mine itself does not
                                          run under -O, M2 or M4-B: the functional test
                                          framework's psbt.py reads the PSBT magic inside
                                          an assert, which -O removes.

It checks:

- `ark0.py mine` (the code the producer pod runs) makes blocks the node
  accepts, below the SLH height with the ML-DSA-44 push only and from it with
  the SLH output at vout[c - 1], the witness commitment last, and both block
  signatures and the signet solution valid;
- the order: the SLH output is final before the ML-DSA-44 signature, which
  covers it;
- the gates of section 4.5, through check_block_keys: on Ark-0 while M4-B is
  undecided, any ARK0_SLH_* variable stops the producer; once decided (set
  here in the module only), missing variables, a wrong key hash and a start
  later than the height minus 1,920; anywhere, an incomplete set of variables;
- a key that is not the one ARK0_SLH_BLOCK_KEY_SHA256 names, and a signer that
  fails, stop ark0.py before it submits anything;
- review round 1: with fake signers, mine_block calls submitblock 0 times (the
  calls are counted, not inferred from the tip) when the signer hangs (it is
  killed after the timeout), exits 1 after half a signature, answers -pubkey
  with the expected key but signs with another, or prints a valid-length
  signature that does not verify; and the next block after a hung signer is
  made and submitted, as miner_loop.sh would go on.
"""
import argparse
import hashlib
import importlib.machinery
import importlib.util
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time

ARK0_CHALLENGE = "5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae"
HERE = os.path.dirname(os.path.abspath(__file__))
PQ_HEIGHT = 2
SLH_HEIGHT = 4


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def check(condition, message):
    if not condition:
        raise SystemExit(f"FAILED: {message}")
    print(f"ok - {message}", flush=True)


class Env:
    def __init__(self, args):
        self.bin = os.path.join(args.builddir, "bin")
        self.src = args.srcdir
        sys.path.insert(0, os.path.join(self.src, "test", "functional"))
        self.tmp = tempfile.mkdtemp(prefix="ark0-m4b-")
        self.home = os.path.join(self.tmp, "home")
        self.node = os.path.join(self.home, "nodeA")
        os.makedirs(self.node)

        from test_framework.key import ECKey
        from test_framework.script import CScript, OP_1, OP_CHECKMULTISIG
        from test_framework.signet_pq import signet_pq_keygen
        from test_framework.wallet_util import bytes_to_wif
        self.classical = (7).to_bytes(32, "big")
        key = ECKey()
        key.set(self.classical, True)
        self.challenge = bytes(CScript([OP_1, key.get_pubkey().get_bytes(), OP_1, OP_CHECKMULTISIG]))
        self.wif = bytes_to_wif(self.classical)
        self.pq_seed = hashlib.sha256(b"test_m4b_producer pq").digest()
        self.pq_pubkey, _ = signet_pq_keygen(self.pq_seed)
        self.pq_seed_file = os.path.join(self.tmp, "pq.seed")
        with open(self.pq_seed_file, "w") as f:
            f.write(self.pq_seed.hex() + "\n")
        self.slh_key_file = os.path.join(self.tmp, "slh.key")
        with open(self.slh_key_file, "wb") as f:
            f.write(hashlib.sha256(b"test_m4b_producer slh").digest() + bytes(16))
        self.signer = os.path.join(self.bin, "signet-slh-sign")
        out = subprocess.run([self.signer, "-pubkey", self.slh_key_file], stdout=subprocess.PIPE, check=True).stdout
        self.slh_pubkey = bytes.fromhex(out.decode().strip())
        self.rpcport = free_port()
        with open(os.path.join(self.node, "bitcoin.conf"), "w") as f:
            f.write("signet=1\n[signet]\n"
                    f"signetchallenge={self.challenge.hex()}\n"
                    f"signetpqblock={self.pq_pubkey.hex()}@{PQ_HEIGHT}\n"
                    f"signetslhblock={self.slh_pubkey.hex()}@{SLH_HEIGHT}\n"
                    "blockreservedweight=48000\n"
                    f"rpcport={self.rpcport}\nport={free_port()}\nlisten=0\ndnsseed=0\nfixedseeds=0\n"
                    "rpcuser=ark0test\nrpcpassword=ark0test\n")
        self.proc = None

    def cli(self, *args, quiet=False):
        out = subprocess.run([os.path.join(self.bin, "bitcoin-cli"), f"-datadir={self.node}", "-signet"] + list(args),
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL if quiet else None, check=True).stdout
        return out.decode().strip()

    def start(self):
        self.proc = subprocess.Popen([os.path.join(self.bin, "bitcoind"), f"-datadir={self.node}", "-debug=validation"],
                                     stdout=subprocess.DEVNULL)
        for _ in range(300):
            try:
                self.cli("-rpcwait", "-rpcwaittimeout=1", "getblockcount", quiet=True)
                return
            except subprocess.CalledProcessError:
                time.sleep(0.2)
        raise SystemExit("bitcoind did not start")

    def stop(self):
        if self.proc:
            try:
                self.cli("stop")
            except subprocess.CalledProcessError:
                pass
            self.proc.wait(timeout=120)

    def producer_env(self, **overrides):
        env = dict(os.environ)
        for name in list(env):
            if name.startswith("ARK0_"):
                del env[name]
        env.update({
            "ARK0_HOME": self.home,
            "ARK0_BIN": self.bin,
            "ARK0_MINER_PATH": os.path.join(self.src, "contrib", "signet", "miner"),
            "ARK0_NODE_A": self.node,
            "ARK0_NODE_B": self.node,
            "ARK0_WALLET": "ark0",
            "ARK0_PQ_BLOCK_SEED_FILE": self.pq_seed_file,
            "ARK0_PQ_BLOCK_FROM": str(PQ_HEIGHT),
            "ARK0_PQ_BLOCK_KEY_SHA256": hashlib.sha256(self.pq_pubkey).hexdigest(),
            "ARK0_SLH_BLOCK_KEY_FILE": self.slh_key_file,
            "ARK0_SLH_BLOCK_FROM": str(SLH_HEIGHT),
            "ARK0_SLH_BLOCK_KEY_SHA256": hashlib.sha256(self.slh_pubkey).hexdigest(),
            "PYTHONPATH": os.path.join(self.src, "test", "functional"),
        })
        for name, value in overrides.items():
            if value is None:
                env.pop(name, None)
            else:
                env[name] = value
        return env

    def mine(self, count, **overrides):
        cmd = [sys.executable] + (["-O"] if sys.flags.optimize else []) + [os.path.join(HERE, "ark0.py"), "mine", str(count), "--quiet"]
        return subprocess.run(cmd, env=self.producer_env(**overrides), stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def load_ark0(env):
    """ark0.py as a module, with its constants read from env."""
    saved = dict(os.environ)
    os.environ.clear()
    os.environ.update(env)
    try:
        loader = importlib.machinery.SourceFileLoader("ark0_under_test", os.path.join(HERE, "ark0.py"))
        spec = importlib.util.spec_from_loader("ark0_under_test", loader)
        module = importlib.util.module_from_spec(spec)
        loader.exec_module(module)
        return module
    finally:
        os.environ.clear()
        os.environ.update(saved)


def raises(fn, needle):
    try:
        fn()
    except RuntimeError as e:
        return needle in str(e)
    return False


def test_mining(env):
    from test_framework.messages import CBlock, from_hex
    from test_framework.signet_pq import verify_signet_pq_signature, witness_commitment_index
    from test_framework.signet_slh import SIGNET_SLH_SCRIPT_PREFIX, signet_slh_index, verify_signet_slh_signature

    env.cli("createwallet", "ark0", "false", "true")
    desc = f"multi(1,{env.wif})"
    desc += "#" + json.loads(env.cli("getdescriptorinfo", desc))["checksum"]
    result = json.loads(env.cli("-rpcwallet=ark0", "importdescriptors", json.dumps([{"desc": desc, "timestamp": "now"}])))
    check(all(r["success"] for r in result), "the wallet holds the challenge key")
    # The BIP 173 example address; the reward goes nowhere anyone needs.
    reward = "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx"
    with open(os.path.join(env.home, "reward_address.txt"), "w") as f:
        f.write(reward + "\n")

    p = env.mine(SLH_HEIGHT + 3)
    check(p.returncode == 0, f"ark0.py mine {SLH_HEIGHT + 3} succeeds: {p.stderr.decode()[-500:]}")
    check(int(env.cli("getblockcount")) == SLH_HEIGHT + 3, "the node accepted every block")
    for height in range(1, SLH_HEIGHT + 4):
        block = from_hex(CBlock(), env.cli("getblock", env.cli("getblockhash", str(height)), "0"))
        coinbase = block.vtx[0]
        c = witness_commitment_index(block)
        check(c == len(coinbase.vout) - 1, f"height {height}: the witness commitment is the last output")
        has_slh = bytes(coinbase.vout[c - 1].scriptPubKey)[:9] == SIGNET_SLH_SCRIPT_PREFIX
        check(has_slh == (height >= SLH_HEIGHT), f"height {height}: an SLH output {'at c - 1' if has_slh else 'absent'}")
        if height >= PQ_HEIGHT:
            check(verify_signet_pq_signature(block, env.pq_pubkey, env.challenge), f"height {height}: the ML-DSA-44 signature verifies")
        if has_slh:
            check(signet_slh_index(block) == c - 1 and verify_signet_slh_signature(block, env.slh_pubkey, env.challenge),
                  f"height {height}: the SLH-DSA signature verifies")
            check(coinbase.vout[c - 1].nValue == 0, f"height {height}: the SLH output has amount 0")

    tip = env.cli("getbestblockhash")
    p = env.mine(1, ARK0_SLH_BLOCK_KEY_SHA256="00" * 32)
    check(p.returncode != 0 and b"not the one ARK0_SLH_BLOCK_KEY_SHA256 names" in p.stderr, "a wrong SLH key hash stops ark0.py")
    bad_key = os.path.join(env.tmp, "bad.key")
    with open(bad_key, "wb") as f:
        f.write(bytes(47))
    p = env.mine(1, ARK0_SLH_BLOCK_KEY_FILE=bad_key)
    check(p.returncode != 0 and b"signet-slh-sign" in p.stderr, "a signer that fails stops ark0.py")
    p = env.mine(1, ARK0_SLH_SIGNER=os.path.join(env.tmp, "no-such-signer"))
    check(p.returncode != 0, "a missing signer stops ark0.py")
    p = env.mine(1, ARK0_SLH_BLOCK_FROM=None)
    check(p.returncode != 0 and b"go together" in p.stderr, "an incomplete set of ARK0_SLH_* variables stops ark0.py")
    check(env.cli("getbestblockhash") == tip, "nothing was submitted by the failed runs")
    with open(os.path.join(env.node, "signet", "debug.log"), encoding="utf-8") as f:
        log = f.read()
    check(f"Signet SLH-DSA block signature required from height {SLH_HEIGHT}, public key SHA256 {hashlib.sha256(env.slh_pubkey).hexdigest()}" in log,
          "the node logged the rule at startup")
    check(f"below height {SLH_HEIGHT}, SLH-DSA block signature absent" in log, "below the height the node logged its verdict")


def fake_signer(env, name, body):
    """A stand-in for signet-slh-sign, executable from its creation (no chmod)."""
    path = os.path.join(env.tmp, name)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o755)
    os.write(fd, (f"#!{sys.executable}\nimport os, sys, time\n"
                  f"sys.path.insert(0, {os.path.join(env.src, 'test', 'functional')!r})\n{body}\n").encode())
    os.close(fd)
    return path


def test_signer_faults(env):
    from test_framework.signet_slh import SIGNET_SLH_CONTEXT
    real = env.signer
    other_seed = hashlib.sha256(b"test_m4b_producer another slh key").digest() + bytes(16)
    pidfile = os.path.join(env.tmp, "sleeper.pid")
    pubkey_answer = f"""if sys.argv[1] == '-pubkey':
    os.execv({real!r}, [{real!r}] + sys.argv[1:])
"""
    signers = {
        "a signer that hangs": (fake_signer(env, "sleeper", pubkey_answer + f"open({pidfile!r}, 'w').write(str(os.getpid())); time.sleep(600)"),
                                "did not finish within 2 s"),
        "half a signature, exit 1": (fake_signer(env, "half", pubkey_answer + f"""msg = sys.stdin.read()
import subprocess
sig = subprocess.run([{real!r}, sys.argv[1]], input=msg.encode(), stdout=subprocess.PIPE).stdout.decode().strip()
sys.stdout.write(sig[:len(sig) // 2]); sys.exit(1)"""), "failed with code 1"),
        "-pubkey right, signed with another key": (fake_signer(env, "swapped", pubkey_answer + f"""from test_framework.p2mr_slhdsa128s import slhdsa128s_keygen, slhdsa128s_sign
_, sk = slhdsa128s_keygen({other_seed!r})
print(slhdsa128s_sign(sk, bytes.fromhex(sys.stdin.read().strip()), context={SIGNET_SLH_CONTEXT!r}, addrnd=os.urandom(16)).hex())"""),
                                                    "does not verify under the checked public key"),
        "a valid-length signature that does not verify": (fake_signer(env, "invalid", pubkey_answer + "sys.stdin.read(); print('ab' * 7856)"),
                                                          "does not verify under the checked public key"),
    }
    # One block below the SLH start would not call the signer: the node is past it.
    check(int(env.cli("getblockcount")) + 1 >= SLH_HEIGHT, "the next block carries an SLH output")
    for name, (signer, needle) in signers.items():
        m = load_ark0(env.producer_env(ARK0_SLH_SIGNER=signer))
        # The short timeout only for the signer that hangs: the Python fakes generate a key and
        # sign, which can take longer than 2 s, and must fail in the check they test, not time out.
        if name == "a signer that hangs":
            m.SLH_SIGN_TIMEOUT = 2
        calls = []
        original = m.cli_stdin
        def counting(method, *args, _original=original, **kw):
            calls.append(method)
            return _original(method, *args, **kw)
        m.cli_stdin = counting
        start = time.time()
        try:
            m.mine_block()
            error = None
        except RuntimeError as e:
            error = str(e)
        check(error is not None and needle in error, f"{name}: mine_block stops ({error})")
        check(calls.count("submitblock") == 0, f"{name}: submitblock called 0 times")
        check(time.time() - start < m.SLH_SIGN_TIMEOUT + 10, f"{name}: within its timeout ({m.SLH_SIGN_TIMEOUT} s)")
    with open(pidfile, encoding="utf-8") as f:
        check(not os.path.exists(f"/proc/{int(f.read())}"), "the hung signer was killed")

    # The loop goes on: with the real signer again, the next block is made and submitted once.
    m = load_ark0(env.producer_env())
    calls = []
    original = m.cli_stdin
    m.cli_stdin = lambda method, *args, **kw: (calls.append(method), original(method, *args, **kw))[1]
    tip = int(env.cli("getblockcount"))
    _, blockhash, result = m.mine_block()
    check(result is None and calls.count("submitblock") == 1 and int(env.cli("getblockcount")) == tip + 1,
          "after the failures the next block is made and submitted once")
    from test_framework.signet_slh import check_slh_signature
    t = []
    pub = bytes.fromhex(subprocess.run([real, "-pubkey", env.slh_key_file], stdout=subprocess.PIPE, check=True).stdout.decode().strip())
    from test_framework.messages import CBlock, from_hex
    from test_framework.signet_slh import signet_slh_message, signet_slh_signature
    block = from_hex(CBlock(), env.cli("getblock", blockhash, "0"))
    msg, sig = signet_slh_message(block, env.challenge), signet_slh_signature(block)
    for _ in range(50):
        a = time.perf_counter()
        check_slh_signature(pub, msg, sig)
        t.append(time.perf_counter() - a)
    t.sort()
    print(f"producer's Python verification: median {t[25] * 1e3:.1f} ms, max {t[-1] * 1e3:.1f} ms of 50", flush=True)


def test_gates(env):
    tmpl_ark0 = {"signet_challenge": ARK0_CHALLENGE, "height": 50000}
    tmpl_other = {"signet_challenge": env.challenge.hex(), "height": 50000}
    ark0_pq = {"ARK0_PQ_BLOCK_FROM": "18000", "ARK0_PQ_BLOCK_KEY_SHA256": "eee8548f51f25492de3ae430b7ce7ec549edfaa663b8f577f026ce0498fdd6ad"}
    no_slh = {"ARK0_SLH_BLOCK_KEY_FILE": None, "ARK0_SLH_BLOCK_FROM": None, "ARK0_SLH_BLOCK_KEY_SHA256": None}

    m = load_ark0(env.producer_env(**ark0_pq, ARK0_SLH_BLOCK_FROM="58080"))
    check(m.ARK0_SLH_BLOCK_HEIGHT is None and m.ARK0_SLH_KEY_SHA256 is None, "ark0.py: M4-B is undecided on Ark-0")
    check(raises(lambda: m.check_block_keys(tmpl_ark0), "M4-B is not active on Ark-0"),
          "Ark-0, undecided: the ARK0_SLH_* variables stop the producer")
    m = load_ark0(env.producer_env(**ark0_pq, **no_slh))
    m.check_block_keys(tmpl_ark0)
    check(True, "Ark-0, undecided, no ARK0_SLH_* variable: the producer goes on with M2 as before")

    def decided(**overrides):
        module = load_ark0(env.producer_env(**ark0_pq, **overrides))
        module.ARK0_SLH_BLOCK_HEIGHT = 60000
        module.ARK0_SLH_KEY_SHA256 = hashlib.sha256(env.slh_pubkey).hexdigest()
        return module
    m = decided(**no_slh)
    check(raises(lambda: m.check_block_keys(tmpl_ark0), "Ark-0 blocks need ARK0_SLH_BLOCK_KEY_FILE"),
          "Ark-0, decided: missing key variables stop the producer")
    m = decided(ARK0_SLH_BLOCK_FROM="58080", ARK0_SLH_BLOCK_KEY_SHA256="11" * 32)
    check(raises(lambda: m.check_block_keys(tmpl_ark0), "the key of the Ark-0 rule"),
          "Ark-0, decided: a wrong public key hash stops the producer")
    m = decided(ARK0_SLH_BLOCK_FROM="58081")
    check(raises(lambda: m.check_block_keys(tmpl_ark0), "must be at most 58080"),
          "Ark-0, decided: a start later than the height minus 1,920 stops the producer")
    m = decided(ARK0_SLH_BLOCK_FROM="58080")
    m.check_block_keys(tmpl_ark0)
    check(True, "Ark-0, decided: a start at the height minus 1,920 with the right key passes")
    m = decided(ARK0_SLH_BLOCK_FROM="58080", ARK0_PQ_BLOCK_SEED_FILE=None)
    check(raises(lambda: m.check_block_keys(tmpl_ark0), "needs ARK0_PQ_BLOCK_SEED_FILE"),
          "the SLH-DSA block signature without the ML-DSA-44 one stops the producer")

    m = load_ark0(env.producer_env(ARK0_SLH_BLOCK_KEY_SHA256=None))
    check(raises(lambda: m.check_block_keys(tmpl_other), "go together"), "another signet: an incomplete set stops the producer")
    m = load_ark0(env.producer_env(ARK0_SLH_BLOCK_FROM=str(PQ_HEIGHT - 1)))
    check(raises(lambda: m.check_block_keys(tmpl_other), "at or above ARK0_PQ_BLOCK_FROM"),
          "another signet: an SLH start below the ML-DSA-44 start stops the producer")
    m = load_ark0(env.producer_env())
    m.check_block_keys(tmpl_other)
    check(True, "another signet with all variables passes")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--builddir", required=True)
    parser.add_argument("--srcdir", required=True)
    parser.add_argument("--keep", action="store_true", help="keep the temporary directory")
    env = Env(parser.parse_args())
    print(f"python -O: {bool(sys.flags.optimize)}; directory {env.tmp}", flush=True)
    try:
        if not sys.flags.optimize:
            env.start()
            test_mining(env)
            test_signer_faults(env)
        test_gates(env)
    finally:
        env.stop()
        if not parser.parse_args().keep:
            shutil.rmtree(env.tmp, ignore_errors=True)
    print("all producer checks passed")


if __name__ == "__main__":
    main()

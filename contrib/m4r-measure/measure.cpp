// Copyright (c) 2026 duncan0k
// Distributed under the MIT software license, see the accompanying
// file COPYING or http://www.opensource.org/licenses/mit-license.php.
//
// The single-check gate of the M4-R validation weight (docs/m4r/m4r-rescue-design-2026-10-23.md,
// section 2.5): per-sample wall and thread CPU times of voleith_verify_s1, through the C ABI of the
// node's own static library, for the three inputs the design names, and of BIP 340 verification
// with the node's libsecp256k1, each run continuously and with a pause before every sample. Never
// part of a node. See README.md.

#include <secp256k1.h>
#include <secp256k1_extrakeys.h>
#include <secp256k1_schnorrsig.h>
#include <voleith.h>

#include <algorithm>
#include <cassert>
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <iterator>
#include <string>
#include <thread>
#include <sys/resource.h>
#include <time.h>
#include <vector>

using Bytes = std::vector<unsigned char>;

static Bytes ReadFile(const std::string& path)
{
    std::ifstream f{path, std::ios::binary};
    if (!f) { std::fprintf(stderr, "cannot read %s\n", path.c_str()); std::exit(2); }
    return Bytes{std::istreambuf_iterator<char>{f}, {}};
}

static double ThreadCpuNs()
{
    timespec ts;
    clock_gettime(CLOCK_THREAD_CPUTIME_ID, &ts);
    return ts.tv_sec * 1e9 + ts.tv_nsec;
}

struct Sample { double wall_ns, cpu_ns; };

template <typename F>
static std::vector<Sample> Run(F f, int count, int pause_us)
{
    std::vector<Sample> out;
    out.reserve(count);
    for (int i = 0; i < count; ++i) {
        if (pause_us) std::this_thread::sleep_for(std::chrono::microseconds{pause_us});
        const double c0{ThreadCpuNs()};
        const auto w0{std::chrono::steady_clock::now()};
        f();
        const auto w1{std::chrono::steady_clock::now()};
        const double c1{ThreadCpuNs()};
        out.push_back({std::chrono::duration<double, std::nano>(w1 - w0).count(), c1 - c0});
    }
    return out;
}

static double Quantile(std::vector<double> v, double q)
{
    std::sort(v.begin(), v.end());
    // Nearest rank.
    size_t rank{static_cast<size_t>(q * v.size() + 0.999999999)};
    rank = std::clamp<size_t>(rank, 1, v.size());
    return v[rank - 1];
}

static void Report(const char* name, const char* mode, const std::vector<Sample>& s)
{
    std::vector<double> wall, cpu;
    for (const auto& x : s) { wall.push_back(x.wall_ns); cpu.push_back(x.cpu_ns); }
    std::printf("%-22s %-10s n=%-6zu wall median %10.1f us p99 %10.1f us max %10.1f us | cpu median %10.1f us p99 %10.1f us zero-cpu %zu\n",
                name, mode, s.size(), Quantile(wall, 0.5) / 1e3, Quantile(wall, 0.99) / 1e3, Quantile(wall, 1.0) / 1e3,
                Quantile(cpu, 0.5) / 1e3, Quantile(cpu, 0.99) / 1e3,
                static_cast<size_t>(std::count_if(cpu.begin(), cpu.end(), [](double c) { return c <= 0; })));
}

int main(int argc, char** argv)
{
    if (argc != 5) {
        std::fprintf(stderr, "usage: measure <input dir> <rescue samples> <schnorr samples> <pause us>\n");
        return 2;
    }
    const std::string dir{argv[1]};
    const int n_rescue{std::atoi(argv[2])}, n_schnorr{std::atoi(argv[3])}, pause_us{std::atoi(argv[4])};
    // Written by prepare.py: K, suffix, y; the 96-byte m and its proof; a 65,535-byte message and its proof.
    const Bytes k{ReadFile(dir + "/k")}, y{ReadFile(dir + "/y")}, suffix{ReadFile(dir + "/suffix")};
    const Bytes m96{ReadFile(dir + "/m96")}, proof96{ReadFile(dir + "/proof96")};
    const Bytes m_long{ReadFile(dir + "/m65535")}, proof_long{ReadFile(dir + "/proof65535")}, y_long{ReadFile(dir + "/y65535")};
    assert(y.size() == 32 && suffix.size() == 1 && m96.size() == 96 && m_long.size() == 65535);
    Bytes worst{proof96};
    // chall3 = ff..ff: the most XOR work a verification does (SPEC.md section 11); invalid.
    std::fill(worst.end() - 16, worst.end(), 0xff);

    auto verify = [&](const Bytes& m, const Bytes& proof, const Bytes& yy) {
        return voleith_verify_s1(k.data(), k.size(), suffix[0], yy.data(), m.data(), m.size(), proof.data(), proof.size());
    };
    if (verify(m96, proof96, y) != 1 || verify(m_long, proof_long, y_long) != 1 || verify(m96, worst, y) != 0) {
        std::fprintf(stderr, "the inputs do not verify as expected\n");
        return 1;
    }

    // BIP 340 with the node's libsecp256k1.
    secp256k1_context* ctx{secp256k1_context_create(SECP256K1_CONTEXT_NONE)};
    unsigned char seckey[32], msg[32], sig[64];
    for (int i = 0; i < 32; ++i) { seckey[i] = static_cast<unsigned char>(i + 1); msg[i] = static_cast<unsigned char>(0xa5 ^ i); }
    secp256k1_keypair keypair;
    assert(secp256k1_keypair_create(ctx, &keypair, seckey));
    secp256k1_xonly_pubkey pubkey;
    assert(secp256k1_keypair_xonly_pub(ctx, &pubkey, nullptr, &keypair));
    assert(secp256k1_schnorrsig_sign32(ctx, sig, msg, &keypair, nullptr));
    assert(secp256k1_schnorrsig_verify(ctx, sig, msg, 32, &pubkey));

    volatile int sink{0};
    // Warm up both, as a node that has verified before.
    for (int i = 0; i < 3; ++i) sink = sink + verify(m96, proof96, y);
    for (int i = 0; i < 1000; ++i) sink = sink + secp256k1_schnorrsig_verify(ctx, sig, msg, 32, &pubkey);

    const char* mode{pause_us ? "paused" : "continuous"};
    Report("rescue-worst-invalid", mode, Run([&] { sink = sink + verify(m96, worst, y); }, n_rescue, pause_us));
    Report("rescue-msg-65535", mode, Run([&] { sink = sink + verify(m_long, proof_long, y_long); }, n_rescue, pause_us));
    Report("rescue-valid", mode, Run([&] { sink = sink + verify(m96, proof96, y); }, n_rescue, pause_us));
    Report("schnorr", mode, Run([&] { sink = sink + secp256k1_schnorrsig_verify(ctx, sig, msg, 32, &pubkey); }, n_schnorr, pause_us));
    secp256k1_context_destroy(ctx);
    rusage usage;
    getrusage(RUSAGE_SELF, &usage);
    std::printf("peak resident set size of the process: %ld KiB\n", usage.ru_maxrss);
    return sink == -1;
}

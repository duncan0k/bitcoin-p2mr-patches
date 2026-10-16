/* Known-answer driver for the vendored SLH-DSA-SHA2-128s verifier.
 *
 * Reads the vector list written by gen_vectors.py, verifies every vector, and for every vector that is
 * expected valid also one copy of its signature with a single bit flipped, expected invalid. Prints one
 * line per vector, "ID EXPECTED GOT", then a last line "sha256 DIGEST vectors N mismatches M" where
 * DIGEST is the SHA-256 of all the lines before it. The exit status is 1 if any GOT differs from EXPECTED.
 *
 * Vector kinds:
 *   wrap  SLHDSA128SVerify of src/crypto/slhdsa128s.cpp, as the script interpreter calls it.
 *   ext   pure SLH-DSA (slh_verify) with a message of any length: SLHDSA128SVerify's length checks
 *         except the one on the message, which NIST ACVP vectors do not meet.
 *   int   the internal interface (slh_verify_internal), with the same checks on the key and signature.
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "sha2_api.h"
#include "slh_dsa.h"

#define PK_SIZE 32u
#define SIG_SIZE 7856u
#define MAX_CTX 255u

int m4cc_wrapper_verify(const unsigned char *pk, size_t pk_len, const unsigned char *msg, size_t msg_len,
                        const unsigned char *ctx, size_t ctx_len, const unsigned char *sig, size_t sig_len);

static unsigned char EMPTY[1] = {0};

static unsigned char *unhex(const char *s, size_t *len)
{
    size_t n = strcmp(s, "-") == 0 ? 0 : strlen(s) / 2;
    unsigned char *b = malloc(n + 1);
    if (!b) exit(2);
    for (size_t i = 0; i < n; ++i) {
        unsigned v;
        if (sscanf(s + 2 * i, "%2x", &v) != 1) exit(2);
        b[i] = (unsigned char)v;
    }
    *len = n;
    return b;
}

static int verify(char kind, const unsigned char *pk, size_t pk_len, const unsigned char *msg, size_t msg_len,
                  const unsigned char *ctx, size_t ctx_len, const unsigned char *sig, size_t sig_len)
{
    if (kind == 'w') return m4cc_wrapper_verify(pk, pk_len, msg, msg_len, ctx, ctx_len, sig, sig_len);
    if (pk_len != PK_SIZE || sig_len != SIG_SIZE) return 0;
    if (kind == 'e') {
        if (ctx_len > MAX_CTX) return 0;
        return slh_verify(msg_len ? msg : EMPTY, msg_len, sig, sig_len, ctx_len ? ctx : EMPTY, ctx_len, pk,
                          &slh_dsa_sha2_128s) == 1;
    }
    return slh_verify_internal(msg_len ? msg : EMPTY, msg_len, sig, sig_len, pk, &slh_dsa_sha2_128s) == 1;
}

static unsigned char *g_out;
static size_t g_out_len, g_out_cap;
static unsigned g_mismatches, g_count;

static void emit(const char *id, int expected, int got)
{
    char line[256];
    int n = snprintf(line, sizeof line, "%s %d %d\n", id, expected, got);
    if (g_out_len + (size_t)n > g_out_cap) {
        g_out_cap = (g_out_cap + (size_t)n) * 2;
        g_out = realloc(g_out, g_out_cap);
        if (!g_out) exit(2);
    }
    memcpy(g_out + g_out_len, line, (size_t)n);
    g_out_len += (size_t)n;
    fputs(line, stdout);
    ++g_count;
    if (expected != got) ++g_mismatches;
}

int main(int argc, char **argv)
{
    if (argc != 2) {
        fprintf(stderr, "usage: %s VECTORS\n", argv[0]);
        return 2;
    }
    FILE *f = fopen(argv[1], "r");
    if (!f) {
        perror(argv[1]);
        return 2;
    }
    fprintf(stderr, "byte order: %s, pointer size: %u bits\n",
#if __BYTE_ORDER__ == __ORDER_BIG_ENDIAN__
            "big endian",
#else
            "little endian",
#endif
            (unsigned)(8 * sizeof(void *)));

    static char line[40000];
    unsigned flips = 0;
    while (fgets(line, sizeof line, f)) {
        char id[128], kind[8], tok[5][16400];
        int expected;
        if (sscanf(line, "%127s %7s %d %16399s %16399s %16399s %16399s", id, kind, &expected, tok[0], tok[1], tok[2],
                   tok[3]) != 7) {
            fprintf(stderr, "bad vector line\n");
            return 2;
        }
        size_t pk_len, msg_len, ctx_len, sig_len;
        unsigned char *pk = unhex(tok[0], &pk_len), *msg = unhex(tok[1], &msg_len), *ctx = unhex(tok[2], &ctx_len),
                      *sig = unhex(tok[3], &sig_len);
        const char k = kind[0];
        emit(id, expected, verify(k, pk, pk_len, msg, msg_len, ctx, ctx_len, sig, sig_len));
        if (expected == 1 && sig_len == SIG_SIZE) {
            /* One bit of the signature, at a position that differs from vector to vector. */
            const size_t bit = ((size_t)flips * 104729u + 13u) % ((size_t)SIG_SIZE * 8u);
            ++flips;
            sig[bit / 8] ^= (unsigned char)(1u << (bit % 8));
            char fid[160];
            snprintf(fid, sizeof fid, "%s.flip%zu", id, bit);
            emit(fid, 0, verify(k, pk, pk_len, msg, msg_len, ctx, ctx_len, sig, sig_len));
        }
        free(pk);
        free(msg);
        free(ctx);
        free(sig);
    }
    fclose(f);

    unsigned char md[32];
    sha2_256(md, g_out, g_out_len);
    printf("sha256 ");
    for (int i = 0; i < 32; ++i) printf("%02x", md[i]);
    printf(" vectors %u mismatches %u\n", g_count, g_mismatches);
    return g_mismatches ? 1 : 0;
}

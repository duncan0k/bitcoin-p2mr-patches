// Exposes SLHDSA128SVerify, unmodified, to the C driver.
#include <crypto/slhdsa128s.h>

#include <span>

extern "C" int m4cc_wrapper_verify(const unsigned char* pk, size_t pk_len, const unsigned char* msg, size_t msg_len,
                                   const unsigned char* ctx, size_t ctx_len, const unsigned char* sig, size_t sig_len)
{
    return SLHDSA128SVerify(std::span<const unsigned char>{pk, pk_len}, std::span<const unsigned char>{msg, msg_len},
                            std::span<const unsigned char>{ctx, ctx_len}, std::span<const unsigned char>{sig, sig_len})
               ? 1
               : 0;
}

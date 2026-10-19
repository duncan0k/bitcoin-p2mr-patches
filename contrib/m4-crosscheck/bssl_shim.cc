// Reaches BoringSSL's internal SLH-DSA-SHA2-128s verification (FIPS 205, Algorithm 19) for the C driver.
#include <crypto/fipsmodule/bcm_interface.h>

extern "C" int m4cc_bssl_verify_internal(const unsigned char* sig, size_t sig_len, const unsigned char* pk,
                                         const unsigned char* msg, size_t msg_len)
{
    // No header and no context: the message is the one the internal interface takes.
    return bssl::bcm_success(bssl::BCM_slhdsa_sha2_128s_verify_internal(sig, sig_len, pk, nullptr, nullptr, 0, msg, msg_len)) ? 1 : 0;
}

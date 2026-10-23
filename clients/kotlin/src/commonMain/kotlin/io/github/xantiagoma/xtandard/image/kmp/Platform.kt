package io.github.xantiagoma.xtandard.image.kmp

import io.ktor.client.engine.HttpClientEngineFactory

// The only platform-specific code: the cryptographic primitives come from the platform's own
// implementation (javax.crypto and SecureRandom on the JVM and Android, CommonCrypto and
// SecRandomCopyBytes on Apple platforms), and each platform has its Ktor engine.

/** HMAC-SHA256 of data under key (any key length, including empty). */
internal expect fun hmacSha256(
    key: ByteArray,
    data: ByteArray,
): ByteArray

/** AES-CBC with PKCS#7 padding; key is 16, 24 or 32 bytes and iv 16 bytes. Returns the ciphertext only. */
internal expect fun aesCbcEncrypt(
    key: ByteArray,
    iv: ByteArray,
    plaintext: ByteArray,
): ByteArray

/** n bytes from the platform's cryptographically secure generator. */
internal expect fun secureRandomBytes(n: Int): ByteArray

/** The Ktor engine of the default HTTP client: OkHttp on the JVM and Android, Darwin (URLSession) on Apple. */
internal expect fun defaultEngine(): HttpClientEngineFactory<*>

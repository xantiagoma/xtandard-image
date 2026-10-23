package io.github.xantiagoma.xtandard.image.kmp

import io.ktor.client.engine.HttpClientEngineFactory
import io.ktor.client.engine.okhttp.OkHttp
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.IvParameterSpec
import javax.crypto.spec.SecretKeySpec

private val random = SecureRandom()

internal actual fun hmacSha256(
    key: ByteArray,
    data: ByteArray,
): ByteArray {
    val mac = Mac.getInstance("HmacSHA256")
    // SecretKeySpec refuses an empty key; HMAC pads keys with zeros, so one zero byte is the same key.
    mac.init(SecretKeySpec(if (key.isEmpty()) ByteArray(1) else key, "HmacSHA256"))
    return mac.doFinal(data)
}

internal actual fun aesCbcEncrypt(
    key: ByteArray,
    iv: ByteArray,
    plaintext: ByteArray,
): ByteArray {
    // PKCS5Padding is PKCS#7 for AES's 16-byte blocks.
    val c = Cipher.getInstance("AES/CBC/PKCS5Padding")
    c.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), IvParameterSpec(iv))
    return c.doFinal(plaintext)
}

internal actual fun secureRandomBytes(n: Int): ByteArray = ByteArray(n).also { random.nextBytes(it) }

internal actual fun defaultEngine(): HttpClientEngineFactory<*> = OkHttp

@file:OptIn(ExperimentalForeignApi::class)

package io.github.xantiagoma.xtandard.image.kmp

import io.ktor.client.engine.HttpClientEngineFactory
import io.ktor.client.engine.darwin.Darwin
import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.addressOf
import kotlinx.cinterop.alloc
import kotlinx.cinterop.convert
import kotlinx.cinterop.memScoped
import kotlinx.cinterop.ptr
import kotlinx.cinterop.usePinned
import kotlinx.cinterop.value
import platform.CoreCrypto.CCCrypt
import platform.CoreCrypto.CCHmac
import platform.CoreCrypto.CC_SHA256_DIGEST_LENGTH
import platform.CoreCrypto.kCCAlgorithmAES
import platform.CoreCrypto.kCCBlockSizeAES128
import platform.CoreCrypto.kCCEncrypt
import platform.CoreCrypto.kCCHmacAlgSHA256
import platform.CoreCrypto.kCCOptionPKCS7Padding
import platform.CoreCrypto.kCCSuccess
import platform.Security.SecRandomCopyBytes
import platform.Security.kSecRandomDefault
import platform.posix.size_tVar

internal actual fun hmacSha256(
    key: ByteArray,
    data: ByteArray,
): ByteArray {
    val out = ByteArray(CC_SHA256_DIGEST_LENGTH)
    key.usePinned { k ->
        data.usePinned { d ->
            out.usePinned { o ->
                CCHmac(
                    kCCHmacAlgSHA256,
                    if (key.isEmpty()) null else k.addressOf(0),
                    key.size.convert(),
                    if (data.isEmpty()) null else d.addressOf(0),
                    data.size.convert(),
                    o.addressOf(0),
                )
            }
        }
    }
    return out
}

internal actual fun aesCbcEncrypt(
    key: ByteArray,
    iv: ByteArray,
    plaintext: ByteArray,
): ByteArray {
    val capacity = plaintext.size + kCCBlockSizeAES128.toInt()
    val out = ByteArray(capacity)
    return memScoped {
        val moved = alloc<size_tVar>()
        val status =
            key.usePinned { k ->
                iv.usePinned { v ->
                    plaintext.usePinned { p ->
                        out.usePinned { o ->
                            CCCrypt(
                                kCCEncrypt,
                                kCCAlgorithmAES,
                                kCCOptionPKCS7Padding,
                                k.addressOf(0),
                                key.size.convert(),
                                v.addressOf(0),
                                if (plaintext.isEmpty()) null else p.addressOf(0),
                                plaintext.size.convert(),
                                o.addressOf(0),
                                capacity.convert(),
                                moved.ptr,
                            )
                        }
                    }
                }
            }
        check(status == kCCSuccess) { "CCCrypt failed with status $status" }
        out.copyOf(moved.value.toInt())
    }
}

internal actual fun secureRandomBytes(n: Int): ByteArray {
    val out = ByteArray(n)
    if (n == 0) return out
    val status = out.usePinned { SecRandomCopyBytes(kSecRandomDefault, n.convert(), it.addressOf(0)) }
    check(status == 0) { "SecRandomCopyBytes failed with status $status" }
    return out
}

internal actual fun defaultEngine(): HttpClientEngineFactory<*> = Darwin

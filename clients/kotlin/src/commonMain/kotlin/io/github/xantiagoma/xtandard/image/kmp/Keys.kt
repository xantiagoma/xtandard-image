package io.github.xantiagoma.xtandard.image.kmp

/**
 * A signing key: `XI_SIGNING_KEYS` and `XI_SIGNING_SALTS` (imgproxy `IMGPROXY_KEY` and `IMGPROXY_SALT`), plus the
 * signature size (`XI_SIGNING_SIZE`).
 *
 * A key shipped inside a mobile or desktop app can be extracted from it and then signs any URL; see the README
 * ("Keys in apps") for the alternatives. Immutable; [toString] does not reveal the key or the salt.
 */
public class SigningKey private constructor(
    internal val key: ByteArray,
    internal val salt: ByteArray,
    /** The signature size in bytes before encoding, 1 to 32. */
    public val size: Int,
) {
    init {
        if (size !in Constants.MIN_SIGNATURE_SIZE..Constants.MAX_SIGNATURE_SIZE) {
            fail(
                ErrorCode.INVALID_KEY,
                "signature size $size out of range [${Constants.MIN_SIGNATURE_SIZE}, ${Constants.MAX_SIGNATURE_SIZE}]",
            )
        }
    }

    /**
     * This key with another signature size.
     *
     * @param size the signature size in bytes before encoding, 1 to 32
     * @return a new key
     * @throws UrlBuildException [ErrorCode.INVALID_KEY] for a size out of range
     */
    public fun withSize(size: Int): SigningKey = SigningKey(key, salt, size)

    override fun toString(): String = "SigningKey(size=$size)"

    /** Factories. */
    public companion object {
        /**
         * A key from hex strings, as in the server configuration.
         *
         * @param keyHex the HMAC key, hex (either case)
         * @param saltHex the salt, hex
         * @param size the signature size in bytes, 1 to 32
         * @return the key
         * @throws UrlBuildException [ErrorCode.INVALID_KEY] when either is not hex or the size is out of range
         */
        public fun of(
            keyHex: String,
            saltHex: String,
            size: Int = Constants.DEFAULT_SIGNATURE_SIZE,
        ): SigningKey = SigningKey(Text.hex(keyHex, "signing key"), Text.hex(saltHex, "signing salt"), size)

        /**
         * A key from raw bytes (copied).
         *
         * @param key the HMAC key
         * @param salt the salt
         * @param size the signature size in bytes, 1 to 32
         * @return the key
         * @throws UrlBuildException [ErrorCode.INVALID_KEY] for a size out of range
         */
        public fun of(
            key: ByteArray,
            salt: ByteArray,
            size: Int = Constants.DEFAULT_SIGNATURE_SIZE,
        ): SigningKey = SigningKey(key.copyOf(), salt.copyOf(), size)
    }
}

/**
 * An encryption key: the first of `XI_ENCRYPTION_KEYS` (imgproxy `IMGPROXY_SOURCE_URL_ENCRYPTION_KEY`), for `enc/`
 * sources and PDF passwords.
 *
 * Without a fixed IV every token gets a fresh random one, as the server's own encoder does. A fixed IV ([withIv])
 * makes URLs deterministic and reveals when two URLs share a source: use it only for tests or cache keys.
 *
 * The key decrypts every token made with it, and a key shipped inside an app can be extracted from it.
 * Immutable; [toString] does not reveal the key.
 */
public class EncryptionKey private constructor(
    internal val key: ByteArray,
    internal val iv: ByteArray?,
) {
    init {
        if (key.size !in Constants.ENCRYPTION_KEY_SIZES) {
            fail(ErrorCode.INVALID_KEY, "encryption key is ${key.size} bytes, want 16, 24 or 32")
        }
        if (iv != null && iv.size != Constants.IV_SIZE) fail(ErrorCode.INVALID_KEY, "iv is not ${Constants.IV_SIZE} bytes")
    }

    /**
     * This key with a fixed IV (deterministic tokens).
     *
     * @param ivHex the IV, hex: 16 bytes
     * @return a new key
     * @throws UrlBuildException [ErrorCode.INVALID_KEY] when it is not 16 bytes of hex
     */
    public fun withIv(ivHex: String): EncryptionKey = EncryptionKey(key, Text.hex(ivHex, "iv"))

    /**
     * This key with a fixed IV (deterministic tokens).
     *
     * @param iv the IV: 16 bytes (copied)
     * @return a new key
     * @throws UrlBuildException [ErrorCode.INVALID_KEY] when it is not 16 bytes
     */
    public fun withIv(iv: ByteArray): EncryptionKey = EncryptionKey(key, iv.copyOf())

    override fun toString(): String = "EncryptionKey(bytes=${key.size}${if (iv == null) "" else ", fixed iv"})"

    /** Factories. */
    public companion object {
        /**
         * A key from hex, as in the server configuration.
         *
         * @param keyHex the AES key, hex: 16, 24 or 32 bytes
         * @return the key
         * @throws UrlBuildException [ErrorCode.INVALID_KEY] when it is not hex or has another size
         */
        public fun of(keyHex: String): EncryptionKey = EncryptionKey(Text.hex(keyHex, "encryption key"), null)

        /**
         * A key from raw bytes (copied).
         *
         * @param key the AES key: 16, 24 or 32 bytes
         * @return the key
         * @throws UrlBuildException [ErrorCode.INVALID_KEY] for another size
         */
        public fun of(key: ByteArray): EncryptionKey = EncryptionKey(key.copyOf(), null)
    }
}

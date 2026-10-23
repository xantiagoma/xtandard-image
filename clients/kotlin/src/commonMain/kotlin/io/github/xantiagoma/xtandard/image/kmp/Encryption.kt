package io.github.xantiagoma.xtandard.image.kmp

/**
 * Source URL and PDF password encryption, byte-compatible with the server (`internal/security/crypt`):
 * `token = base64url_nopad(IV[16] || AES-CBC(key, IV, PKCS#7(utf8(plaintext))))`.
 *
 * The key decrypts every token made with it: encrypt on a server, or read the README ("Keys in apps") before
 * shipping a key in an app.
 */
public object Encryption {
    /**
     * Encrypts a source URL into an `enc/` token.
     *
     * @param source the source URL
     * @param key the encryption key
     * @return the token (without `enc/`)
     * @throws UrlBuildException [ErrorCode.INVALID_SOURCE] for an empty source or one with control characters
     */
    public fun encryptSource(
        source: String,
        key: EncryptionKey,
    ): String = seal(Text.checkText(source, ErrorCode.INVALID_SOURCE, "source"), key)

    /**
     * Encrypts the password of a password-protected PDF into a `pdf_password` (`pdp`) token: the `enc/` format over
     * the password, with the same key. The server accepts the option on signed URLs only.
     *
     * @param password the plain-text password
     * @param key the encryption key
     * @return the token
     * @throws UrlBuildException [ErrorCode.INVALID_PASSWORD] for an empty password, one with control characters,
     *   or one longer than 1024 UTF-8 bytes
     */
    public fun encryptPdfPassword(
        password: String,
        key: EncryptionKey,
    ): String {
        Text.checkText(password, ErrorCode.INVALID_PASSWORD, "password")
        if (Text.utf8(password).size > Constants.MAX_PDF_PASSWORD_BYTES) {
            fail(ErrorCode.INVALID_PASSWORD, "password is longer than ${Constants.MAX_PDF_PASSWORD_BYTES} bytes")
        }
        return seal(password, key)
    }

    private fun seal(
        plaintext: String,
        key: EncryptionKey,
    ): String {
        val iv = key.iv ?: secureRandomBytes(Constants.IV_SIZE)
        return Text.b64url(iv + aesCbcEncrypt(key.key, iv, Text.utf8(plaintext)))
    }
}

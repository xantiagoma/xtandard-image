<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Internal\Text;

/**
 * Source URL and PDF password encryption, byte-compatible with the server (internal/security/crypt).
 *
 * token = base64url_unpadded(IV[16] ‖ AES-CBC(key, IV, PKCS#7(utf8(plaintext)))).
 * Encrypt on a server: the key decrypts every token.
 */
final class Encryption
{
    private function __construct() {}

    /**
     * Encrypts a source URL into an enc/ token.
     *
     * @throws UrlBuildException invalid_source when the source is empty or has control characters
     */
    public static function encryptSource(string $source, EncryptionKey $key): string
    {
        return $key->seal(Text::checkText($source, ErrorCode::InvalidSource, 'source'));
    }

    /**
     * Encrypts the password of a protected PDF into a pdf_password (pdp) token.
     *
     * The token is the enc/ format over the password, with the same key. The
     * server accepts the option on signed URLs only.
     *
     * @throws UrlBuildException invalid_password when the password is empty, has
     *                           control characters or is over 1024 UTF-8 bytes
     */
    public static function encryptPdfPassword(#[\SensitiveParameter] string $password, EncryptionKey $key): string
    {
        Text::checkText($password, ErrorCode::InvalidPassword, 'password');
        if (\strlen($password) > Constants::MAX_PDF_PASSWORD_BYTES) {
            throw new UrlBuildException(ErrorCode::InvalidPassword, 'password is longer than ' . Constants::MAX_PDF_PASSWORD_BYTES . ' bytes');
        }

        return $key->seal($password);
    }
}

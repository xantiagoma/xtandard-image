<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Internal\Text;

/**
 * An encryption key: the first of XI_ENCRYPTION_KEYS (imgproxy IMGPROXY_SOURCE_URL_ENCRYPTION_KEY).
 *
 * The constructor takes hex; EncryptionKey::raw() takes bytes. Leave the IV
 * null for a fresh random IV per token, as the server's own encoder does. A
 * fixed IV makes URLs deterministic and shows when two URLs share a source:
 * use it only for tests or cache keys. Secrets never appear in var_dump().
 */
final readonly class EncryptionKey
{
    /** AES key bytes: 16, 24 or 32. */
    private string $key;

    /** A fixed 16-byte IV, or null for a random one per token. */
    private ?string $iv;

    /**
     * @param string $key AES key, hex: 16, 24 or 32 bytes
     * @param string|null $iv a fixed 16-byte IV, hex
     *
     * @throws UrlBuildException invalid_key
     */
    public function __construct(#[\SensitiveParameter] string $key, #[\SensitiveParameter] ?string $iv = null)
    {
        $k = Text::hexBytes($key, 'encryption key');
        if (!\in_array(\strlen($k), Constants::ENCRYPTION_KEY_SIZES, true)) {
            throw new UrlBuildException(ErrorCode::InvalidKey, \sprintf('encryption key is %d bytes, want 16, 24 or 32', \strlen($k)));
        }
        $v = null === $iv ? null : Text::hexBytes($iv, 'iv');
        if (null !== $v && Constants::IV_SIZE !== \strlen($v)) {
            throw new UrlBuildException(ErrorCode::InvalidKey, \sprintf('iv is not %d bytes', Constants::IV_SIZE));
        }
        $this->key = $k;
        $this->iv = $v;
    }

    /**
     * An encryption key from raw bytes.
     *
     * @throws UrlBuildException invalid_key
     */
    public static function raw(#[\SensitiveParameter] string $key, #[\SensitiveParameter] ?string $iv = null): self
    {
        return new self(bin2hex($key), null === $iv ? null : bin2hex($iv));
    }

    /**
     * base64url(IV ‖ AES-CBC(key, IV, PKCS#7(plaintext))).
     *
     * @internal use Encryption::encryptSource() or Encryption::encryptPdfPassword()
     */
    public function seal(#[\SensitiveParameter] string $plaintext): string
    {
        $iv = $this->iv ?? random_bytes(Constants::IV_SIZE);
        $cipher = 'aes-' . (\strlen($this->key) * 8) . '-cbc';
        $ct = openssl_encrypt($plaintext, $cipher, $this->key, \OPENSSL_RAW_DATA, $iv);
        if (false === $ct) {
            throw new \RuntimeException("openssl_encrypt {$cipher} failed");
        }

        return Text::b64url($iv . $ct);
    }

    /** @return array{fixed_iv: bool} */
    public function __debugInfo(): array
    {
        return ['fixed_iv' => null !== $this->iv];
    }
}

<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Internal\Text;

/**
 * A signing key: XI_SIGNING_KEYS / XI_SIGNING_SALTS (imgproxy IMGPROXY_KEY / IMGPROXY_SALT).
 *
 * The constructor takes hex, as in the server config; SigningKey::raw() takes bytes.
 * The key and salt never appear in var_dump() or print_r().
 */
final readonly class SigningKey
{
    /** HMAC key bytes. */
    private string $key;

    /** Salt bytes, prepended to the message. */
    private string $salt;

    /**
     * @param string $key HMAC key, hex
     * @param string $salt salt, hex
     * @param int $size signature size in bytes before encoding, 1 to 32 (XI_SIGNING_SIZE)
     *
     * @throws UrlBuildException invalid_key
     */
    public function __construct(
        #[\SensitiveParameter]
        string $key,
        #[\SensitiveParameter]
        string $salt,
        public int $size = Constants::DEFAULT_SIGNATURE_SIZE,
    ) {
        if ($size < Constants::MIN_SIGNATURE_SIZE || $size > Constants::MAX_SIGNATURE_SIZE) {
            throw new UrlBuildException(ErrorCode::InvalidKey, \sprintf(
                'signature size %d out of range [%d, %d]',
                $size,
                Constants::MIN_SIGNATURE_SIZE,
                Constants::MAX_SIGNATURE_SIZE,
            ));
        }
        $this->key = Text::hexBytes($key, 'signing key');
        $this->salt = Text::hexBytes($salt, 'signing salt');
    }

    /**
     * A signing key from raw bytes.
     *
     * @throws UrlBuildException invalid_key
     */
    public static function raw(
        #[\SensitiveParameter]
        string $key,
        #[\SensitiveParameter]
        string $salt,
        int $size = Constants::DEFAULT_SIGNATURE_SIZE,
    ): self {
        return new self(bin2hex($key), bin2hex($salt), $size);
    }

    /**
     * The salted HMAC-SHA256 of a message, truncated to the signature size, unencoded.
     *
     * @internal use Signing::sign()
     */
    public function mac(string $message): string
    {
        return substr(hash_hmac('sha256', $this->salt . $message, $this->key, true), 0, $this->size);
    }

    /** @return array{size: int} */
    public function __debugInfo(): array
    {
        return ['size' => $this->size];
    }
}

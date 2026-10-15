<?php

declare(strict_types=1);

namespace Xtandard\Image\Exception;

use Xtandard\Image\ErrorCode;

/**
 * A URL, token or header cannot be built from the given input.
 *
 * $errorCode is the language-neutral code (clients/README.md); getCode() stays 0.
 */
final class UrlBuildException extends \InvalidArgumentException implements XtandardException
{
    public function __construct(
        public readonly ErrorCode $errorCode,
        string $message,
    ) {
        parent::__construct($message);
    }
}

<?php

declare(strict_types=1);

namespace Xtandard\Image\Exception;

use Psr\Http\Client\ClientExceptionInterface;

/**
 * The request got no response (connection, TLS, timeout); the PSR-18 exception is the previous one.
 */
final class TransportException extends \RuntimeException implements XtandardException
{
    public function __construct(string $message, ClientExceptionInterface $previous)
    {
        parent::__construct($message, 0, $previous);
    }
}

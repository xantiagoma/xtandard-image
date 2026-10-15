<?php

declare(strict_types=1);

namespace Xtandard\Image\Exception;

use Psr\Http\Message\ResponseInterface;
use Xtandard\Image\ResponseTypes;

/**
 * A non-2xx response.
 *
 * $kind, the message and $requestId come from the server's JSON error envelope;
 * a body that is not the envelope (a proxy's error page, say) gives kind
 * "unknown" and the body text. getCode() is the HTTP status. The subclass
 * follows the status: BadRequestException (400), ForbiddenException (403),
 * NotFoundException (404), NotAcceptableException (406),
 * UnprocessableException (422), TooManyRequestsException (429),
 * ServerException (5xx), ServiceUnavailableException (503) and
 * GatewayException (502, 504).
 *
 * @phpstan-import-type ErrorResponse from ResponseTypes
 */
class HttpException extends \RuntimeException implements XtandardException
{
    /** The error kind (invalid_url, unauthorized, not_found, …) or "unknown". */
    public readonly string $kind;

    /** The request ID from the envelope, also in the server logs. */
    public readonly ?string $requestId;

    /**
     * @param int $status the HTTP status
     * @param string $url the request URL
     * @param string $message the client-safe message
     * @param ErrorResponse|null $body the parsed envelope, when the body was one
     * @param ResponseInterface|null $response the response, its body already read
     */
    final public function __construct(
        public readonly int $status,
        public readonly string $url,
        string $message,
        public readonly ?array $body = null,
        public readonly ?ResponseInterface $response = null,
    ) {
        parent::__construct($message, $status);
        $this->kind = null === $body ? 'unknown' : $body['error']['kind'];
        $this->requestId = null === $body ? null : ($body['error']['request_id'] ?? null);
    }

    /**
     * The exception class of an HTTP status.
     *
     * @return class-string<self>
     */
    public static function classFor(int $status): string
    {
        return match (true) {
            400 === $status => BadRequestException::class,
            403 === $status => ForbiddenException::class,
            404 === $status => NotFoundException::class,
            406 === $status => NotAcceptableException::class,
            422 === $status => UnprocessableException::class,
            429 === $status => TooManyRequestsException::class,
            502 === $status, 504 === $status => GatewayException::class,
            503 === $status => ServiceUnavailableException::class,
            $status >= 500 => ServerException::class,
            default => self::class,
        };
    }

    /**
     * The exception of a status, of the class classFor() names.
     *
     * @param ErrorResponse|null $body
     */
    public static function fromStatus(
        int $status,
        string $url,
        string $message,
        ?array $body = null,
        ?ResponseInterface $response = null,
    ): self {
        $class = self::classFor($status);

        return new $class($status, $url, $message, $body, $response);
    }
}

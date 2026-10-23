<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;

/**
 * A query dialect request sent with the HTTP QUERY method (RFC 10008, URL-DIALECTS §4.4).
 *
 * The parameters move from the URL to the request content, so long option
 * chains, watermark text or encrypted sources stay out of URLs. The server
 * must have XI_SERVER_QUERY_METHOD on (the default); many CDNs and proxies
 * neither cache nor forward QUERY.
 */
final readonly class QueryMethodRequest
{
    /** The content type of a QUERY request's content. */
    public const CONTENT_TYPE = 'application/x-www-form-urlencoded';

    /** Always "QUERY". */
    public string $method;

    /** application/x-www-form-urlencoded. */
    public string $contentType;

    /**
     * @param string $url the URL without its query
     * @param string $body the URL's query exactly as built, xi_sig included: the server
     *                     signs, limits and caches it as the GET URL, since xi_sig does not
     *                     cover the method
     */
    private function __construct(
        public string $url,
        public string $body,
    ) {
        $this->method = 'QUERY';
        $this->contentType = self::CONTENT_TYPE;
    }

    /**
     * Splits a built query dialect URL (/q/…?url=…) at its first "?".
     *
     * @throws UrlBuildException invalid_request for a URL without a query, with an empty one, or with a fragment
     */
    public static function fromUrl(string $url): self
    {
        $i = strpos($url, '?');
        if (false === $i || $i === \strlen($url) - 1 || str_contains($url, '#')) {
            throw new UrlBuildException(ErrorCode::InvalidRequest, 'a QUERY request needs a query dialect URL with a query and no fragment');
        }

        return new self(substr($url, 0, $i), substr($url, $i + 1));
    }
}

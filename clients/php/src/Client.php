<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Psr\Http\Client\ClientExceptionInterface;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestFactoryInterface;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\StreamFactoryInterface;
use Xtandard\Image\Exception\HttpException;
use Xtandard\Image\Exception\TransportException;
use Xtandard\Image\Exception\UrlBuildException;

/**
 * An HTTP client for xtandard-image on any PSR-18 client and PSR-17 request factory.
 *
 * It covers the JSON and health endpoints (/info, /healthz, /readyz, the
 * playground's api/meta) and media fetches (images, /video results with Range
 * and HEAD, HLS and DASH manifests). URLs are built and signed with
 * $client->urls, an ImgproxyUrlBuilder. Non-2xx responses throw an
 * HttpException subclass chosen by status; transport failures throw
 * TransportException.
 *
 *     $xi = new Client('http://localhost:8080', new \GuzzleHttp\Client(), new \GuzzleHttp\Psr7\HttpFactory(),
 *         signing: new SigningKey($key, $salt));
 *     $info = $xi->info('s3://bucket/cat.jpg', ['size' => true, 'format' => true]);
 *     $head = $xi->video('s3://bucket/talk.mov', ['vd' => 5], extension: 'webm', method: 'HEAD');
 *
 * Symfony's Psr18Client is both the client and the request factory: new Client($url, new Psr18Client()).
 *
 * HTTP QUERY (RFC 10008) is opt-in: media($url, method: 'QUERY') sends one
 * query dialect URL as a QUERY request (QueryMethodRequest), and
 * queryMethod: true does so for every media() URL under $queryMount when no
 * method is given. Off by default: the server needs XI_SERVER_QUERY_METHOD
 * (on by default), and many CDNs and proxies neither cache nor forward QUERY.
 *
 * @phpstan-import-type ProcessingOptions from OptionTypes
 * @phpstan-import-type InfoOptions from OptionTypes
 * @phpstan-import-type VideoOptions from OptionTypes
 * @phpstan-import-type InfoResponse from ResponseTypes
 * @phpstan-import-type ErrorResponse from ResponseTypes
 */
final readonly class Client
{
    /** The URL builder this client uses (base URL, keys and URL settings). */
    public ImgproxyUrlBuilder $urls;

    /** The server origin, without a trailing slash. */
    public string $baseUrl;

    private RequestFactoryInterface $requests;

    private string $playgroundMeta;

    private ?StreamFactoryInterface $streams;

    private string $queryMount;

    /**
     * @param string $baseUrl server origin, plus the imgproxy mount prefix if any
     * @param ClientInterface $http any PSR-18 client (Guzzle, Symfony HttpClient, …)
     * @param RequestFactoryInterface|null $requestFactory a PSR-17 request factory; default: $http when it is one
     * @param SigningKey|null $signing signs the URLs the client builds
     * @param EncryptionKey|null $encryption key for encrypted sources and PDF passwords
     * @param SourceEncoding|value-of<SourceEncoding> $sourceEncoding default source encoding
     * @param OptionNameStyle|value-of<OptionNameStyle> $optionNames how option names are written
     * @param array<string, string> $headers headers sent with every request
     * @param string|null $playgroundBaseUrl origin of the admin listener for playgroundMeta(); default $baseUrl
     * @param string $playgroundPath XI_PLAYGROUND_PATH
     * @param bool $queryMethod send media() URLs under $queryMount with HTTP QUERY when no method is given (off by default)
     * @param string $queryMount the query dialect's mount (XI_DIALECTS_QUERY_PREFIX), for $queryMethod
     * @param StreamFactoryInterface|null $streamFactory a PSR-17 stream factory for QUERY content; default: $requestFactory or $http when it is one
     *
     * @throws \InvalidArgumentException when no request factory is given and $http is not one
     * @throws UrlBuildException for an unknown source encoding
     */
    public function __construct(
        string $baseUrl,
        private ClientInterface $http,
        ?RequestFactoryInterface $requestFactory = null,
        ?SigningKey $signing = null,
        ?EncryptionKey $encryption = null,
        string $unsignedSignature = Constants::DEFAULT_UNSIGNED,
        SourceEncoding|string $sourceEncoding = SourceEncoding::Base64,
        ?int $splitSource = null,
        string $separator = Constants::DEFAULT_SEPARATOR,
        OptionNameStyle|string $optionNames = OptionNameStyle::Short,
        private array $headers = [],
        ?string $playgroundBaseUrl = null,
        string $playgroundPath = '/_playground/',
        private bool $queryMethod = false,
        string $queryMount = Constants::QUERY_MOUNT,
        ?StreamFactoryInterface $streamFactory = null,
    ) {
        $factory = $requestFactory ?? ($http instanceof RequestFactoryInterface ? $http : null);
        if (null === $factory) {
            throw new \InvalidArgumentException('pass a PSR-17 RequestFactoryInterface: the PSR-18 client is not one');
        }
        $this->requests = $factory;
        $this->streams = $streamFactory
            ?? ($factory instanceof StreamFactoryInterface ? $factory : ($http instanceof StreamFactoryInterface ? $http : null));
        $this->queryMount = rtrim($queryMount, '/');
        $this->baseUrl = rtrim($baseUrl, '/');
        $this->urls = new ImgproxyUrlBuilder(
            baseUrl: $this->baseUrl,
            signing: $signing,
            unsignedSignature: $unsignedSignature,
            encryption: $encryption,
            sourceEncoding: $sourceEncoding,
            splitSource: $splitSource,
            separator: $separator,
            optionNames: $optionNames,
        );
        $origin = rtrim($playgroundBaseUrl ?? $this->baseUrl, '/');
        $this->playgroundMeta = $origin . '/' . trim($playgroundPath, '/') . '/api/meta';
    }

    /**
     * Formats a byte range as a Range header value (bytes=0-1023, bytes=-500).
     *
     * @throws UrlBuildException invalid_range
     */
    public static function rangeHeader(ByteRange|ByteSuffix|string $range): string
    {
        return ByteRange::header($range);
    }

    /**
     * GET /healthz: returns while the process can serve HTTP.
     *
     * @throws HttpException|TransportException
     */
    public function healthz(): void
    {
        $url = $this->baseUrl . '/healthz';
        $this->checked($this->send('GET', $url, 'text/plain'), $url);
    }

    /**
     * GET /readyz: the readiness state; 503 (starting, draining) is not an error.
     *
     * @throws HttpException|TransportException
     */
    public function readyz(): Readiness
    {
        $url = $this->baseUrl . '/readyz';
        $res = $this->send('GET', $url, 'text/plain');
        $status = $res->getStatusCode();
        if (200 !== $status && 503 !== $status) {
            throw self::httpError($res, $url);
        }
        $state = trim((string) $res->getBody());

        return match ($state) {
            'ready', 'starting', 'draining' => new Readiness(200 === $status && 'ready' === $state, $state),
            default => throw new HttpException($status, $url, \sprintf('unexpected readiness state %s', var_export($state, true)), null, $res),
        };
    }

    /**
     * GET /info for a source, with the URL built (and signed) from the arguments.
     *
     * @param InfoOptions|null $options
     * @param SourceEncoding|value-of<SourceEncoding>|null $sourceEncoding
     *
     * @return InfoResponse
     *
     * @throws UrlBuildException|HttpException|TransportException
     */
    public function info(
        string $source,
        ?array $options = null,
        SourceEncoding|string|null $sourceEncoding = null,
        #[\SensitiveParameter]
        ?string $pdfPassword = null,
    ): array {
        return $this->infoAt($this->urls->infoUrl($source, $options, $sourceEncoding, $pdfPassword));
    }

    /**
     * GET a prebuilt /info/… path or URL.
     *
     * @return InfoResponse
     *
     * @throws HttpException|TransportException
     */
    public function infoAt(string $url): array
    {
        /** @var InfoResponse */
        return $this->json($this->absolute($url));
    }

    /**
     * GET (or HEAD) any URL or server path: an image or video URL, a query URL, a stream part.
     *
     * The body is whatever the PSR-18 client gives: read it with getBody().
     * 'QUERY' sends a query dialect URL's query as the request content (RFC 10008);
     * with no $method, the client's queryMethod picks QUERY for URLs under its
     * queryMount and GET for the rest.
     *
     * @param 'GET'|'HEAD'|'QUERY'|null $method
     * @param array<string, string> $headers
     *
     * @throws UrlBuildException|HttpException|TransportException
     */
    public function media(
        string $url,
        ?string $method = null,
        ByteRange|ByteSuffix|string|null $range = null,
        string $accept = '*/*',
        array $headers = [],
    ): ResponseInterface {
        if (null !== $range) {
            $headers['Range'] = ByteRange::header($range);
        }

        $abs = $this->absolute($url);
        if ('QUERY' === $method || (null === $method && $this->queryMethod && $this->underQueryMount($abs))) {
            $q = QueryMethodRequest::fromUrl($abs);

            return $this->checked($this->send($q->method, $q->url, $accept, [...$headers, 'Content-Type' => $q->contentType], $q->body), $abs);
        }

        return $this->checked($this->send($method ?? 'GET', $abs, $accept, $headers), $abs);
    }

    /**
     * GET (or HEAD) a processed image; $accept drives format negotiation.
     *
     * @param ProcessingOptions|Pipelines<ProcessingOptions>|null $options
     * @param SourceEncoding|value-of<SourceEncoding>|null $sourceEncoding
     * @param 'GET'|'HEAD' $method
     * @param array<string, string> $headers
     *
     * @throws UrlBuildException|HttpException|TransportException
     */
    public function image(
        string $source,
        array|Pipelines|null $options = null,
        ImageFormat|string|null $extension = null,
        SourceEncoding|string|null $sourceEncoding = null,
        #[\SensitiveParameter]
        ?string $pdfPassword = null,
        string $method = 'GET',
        string $accept = '*/*',
        array $headers = [],
    ): ResponseInterface {
        $url = $this->urls->url($source, $options, $extension, $sourceEncoding, $pdfPassword);

        return $this->media($url, $method, null, $accept, $headers);
    }

    /**
     * GET (or HEAD) a /video result: video, audio or a waveform; $range asks for part of it (206).
     *
     * @param VideoOptions|null $options
     * @param SourceEncoding|value-of<SourceEncoding>|null $sourceEncoding
     * @param 'GET'|'HEAD' $method
     * @param array<string, string> $headers
     *
     * @throws UrlBuildException|HttpException|TransportException
     */
    public function video(
        string $source,
        ?array $options = null,
        VideoContainer|string|null $extension = null,
        SourceEncoding|string|null $sourceEncoding = null,
        string $method = 'GET',
        ByteRange|ByteSuffix|string|null $range = null,
        string $accept = '*/*',
        array $headers = [],
    ): ResponseInterface {
        $url = $this->urls->videoUrl($source, $options, $extension, $sourceEncoding);

        return $this->media($url, $method, $range, $accept, $headers);
    }

    /**
     * GET an HLS or DASH manifest (/video/…@m3u8 or @mpd).
     *
     * @param StreamProtocol|value-of<StreamProtocol> $protocol
     * @param list<int>|null $renditions
     * @param SegmentFormat|value-of<SegmentFormat>|null $segmentFormat
     * @param VideoOptions|null $options
     * @param SourceEncoding|value-of<SourceEncoding>|null $sourceEncoding
     * @param array<string, string> $headers
     *
     * @throws UrlBuildException|HttpException|TransportException
     */
    public function stream(
        string $source,
        StreamProtocol|string $protocol,
        ?array $renditions = null,
        ?int $segmentDuration = null,
        SegmentFormat|string|null $segmentFormat = null,
        ?array $options = null,
        SourceEncoding|string|null $sourceEncoding = null,
        array $headers = [],
    ): StreamManifest {
        $url = $this->urls->streamUrl($source, $protocol, $renditions, $segmentDuration, $segmentFormat, $options, $sourceEncoding);
        $p = $protocol instanceof StreamProtocol ? $protocol : StreamProtocol::from($protocol);
        $ctype = $p->contentType();
        $res = $this->checked($this->send('GET', $url, "{$ctype}, */*;q=0.5", $headers), $url);
        $got = $res->getHeaderLine('Content-Type');

        return new StreamManifest($url, '' === $got ? $ctype : $got, (string) $res->getBody());
    }

    /**
     * GET the playground's api/meta (needs XI_PLAYGROUND_ENABLED; admin listener by default).
     *
     * @return array<string, mixed>
     *
     * @throws HttpException|TransportException
     */
    public function playgroundMeta(): array
    {
        return $this->json($this->playgroundMeta);
    }

    /**
     * @return array<string, mixed>
     *
     * @throws HttpException|TransportException
     */
    private function json(string $url): array
    {
        $res = $this->checked($this->send('GET', $url, 'application/json'), $url);
        try {
            $data = json_decode((string) $res->getBody(), true, 512, \JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            throw new HttpException($res->getStatusCode(), $url, 'response is not JSON: ' . $e->getMessage(), null, $res);
        }
        if (!\is_array($data) || ([] !== $data && array_is_list($data))) {
            throw new HttpException($res->getStatusCode(), $url, 'response is not a JSON object', null, $res);
        }

        /** @var array<string, mixed> $data */
        return $data;
    }

    /** Whether an absolute URL's path is under the query dialect mount (for queryMethod). */
    private function underQueryMount(string $url): bool
    {
        // Not parse_url(): it fails on imgproxy paths such as /info/<sig>/s:1/<source>.
        $path = (string) preg_replace('~^[a-zA-Z][a-zA-Z0-9+.-]*://[^/?#]*~', '', explode('#', explode('?', $url, 2)[0], 2)[0]);

        return $path === $this->queryMount || str_starts_with($path, $this->queryMount . '/');
    }

    private function absolute(string $url): string
    {
        if (1 === preg_match('/^[a-zA-Z][a-zA-Z0-9+.-]*:/', $url)) {
            return $url;
        }

        return $this->baseUrl . (str_starts_with($url, '/') ? '' : '/') . $url;
    }

    /**
     * @param array<string, string> $headers
     *
     * @throws TransportException
     */
    private function send(string $method, string $url, string $accept, array $headers = [], ?string $body = null): ResponseInterface
    {
        $req = $this->requests->createRequest($method, $url);
        foreach ([...$this->headers, ...$headers, 'Accept' => $accept] as $name => $value) {
            $req = $req->withHeader($name, $value);
        }
        if (null !== $body) {
            if (null !== $this->streams) {
                $req = $req->withBody($this->streams->createStream($body));
            } elseif ($req->getBody()->isWritable()) {
                $req->getBody()->write($body);
            } else {
                throw new \LogicException("{$method} {$url}: pass a PSR-17 StreamFactoryInterface to send request content");
            }
        }
        try {
            return $this->http->sendRequest($req);
        } catch (ClientExceptionInterface $e) {
            throw new TransportException("{$method} {$url}: {$e->getMessage()}", $e);
        }
    }

    /** @throws HttpException */
    private function checked(ResponseInterface $res, string $url): ResponseInterface
    {
        $status = $res->getStatusCode();
        if ($status < 200 || $status > 299) {
            throw self::httpError($res, $url);
        }

        return $res;
    }

    /** Converts a non-2xx response into an HttpException subclass. */
    private static function httpError(ResponseInterface $res, string $url): HttpException
    {
        $text = (string) $res->getBody();
        $body = self::envelope($text);
        $message = null !== $body
            ? $body['error']['message']
            : ('' !== trim($text) ? trim($text) : ('' !== $res->getReasonPhrase() ? $res->getReasonPhrase() : 'HTTP ' . $res->getStatusCode()));

        return HttpException::fromStatus($res->getStatusCode(), $url, $message, $body, $res);
    }

    /** @return ErrorResponse|null */
    private static function envelope(string $text): ?array
    {
        try {
            $v = json_decode($text, true, 512, \JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return null;
        }
        if (!\is_array($v) || !\is_array($v['error'] ?? null)) {
            return null;
        }
        $e = $v['error'];
        if (!\is_string($e['kind'] ?? null) || !\is_string($e['message'] ?? null)) {
            return null;
        }
        // A kind this client does not know yet (a newer server) reads as "unknown".
        $error = ['kind' => (ErrorKind::tryFrom($e['kind']) ?? ErrorKind::Unknown)->value, 'message' => $e['message']];
        if (\is_string($e['request_id'] ?? null)) {
            $error['request_id'] = $e['request_id'];
        }

        return ['error' => $error];
    }
}

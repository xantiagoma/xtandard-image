<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Internal\ChainState;
use Xtandard\Image\Internal\Options;
use Xtandard\Image\Internal\Streams;
use Xtandard\Image\Internal\Text;

/**
 * Builds query dialect URLs (docs/URL-DIALECTS.md §4; the server needs XI_DIALECTS_QUERY_ENABLED=true).
 *
 *     {baseUrl}{mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&xi_sig=…]
 *     {baseUrl}{mount}/video?url=<source>&<video option>=<arg,arg>…[&xi_sig=…]
 *     {baseUrl}{mount}/<imaginary endpoint>?url=<source>&<param>=…[&xi_sig=…]
 *
 * With signing, every URL ends in an xi_sig over the path after the mount and the query:
 *
 *     $q = new QueryUrlBuilder(baseUrl: 'https://img.example.com', signing: new SigningKey($key, $salt));
 *     $q->url('s3://bucket/cat.jpg', params: ['w' => 300, 'f' => 'webp'], options: ['bl' => 2]);
 *     // https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
 *
 * Parameters are an array keyed by name (in order) or a list of [key, value]
 * pairs (keys may repeat); null values are skipped.
 *
 * @phpstan-import-type ProcessingOptions from OptionTypes
 * @phpstan-import-type VideoOptions from OptionTypes
 * @phpstan-import-type QueryImageParams from OptionTypes
 * @phpstan-import-type ImaginaryParams from OptionTypes
 * @phpstan-import-type ImaginaryEndpoint from OptionTypes
 * @phpstan-import-type QueryValue from OptionTypes
 * @phpstan-import-type Entry from Options
 *
 * @phpstan-type ParamPairs list<array{string, QueryValue|null}>
 */
final readonly class QueryUrlBuilder
{
    public OptionNameStyle $optionNames;

    /**
     * @param string $baseUrl server origin (and any path before the mount)
     * @param string $mount the query dialect's mount prefix (XI_DIALECTS_QUERY_PREFIX)
     * @param SigningKey|null $signing signs with xi_sig (the imgproxy key and salt); without it URLs are unsigned
     * @param EncryptionKey|null $encryption key for PDF passwords (XI_ENCRYPTION_KEYS)
     * @param string $separator argument separator inside o=
     * @param OptionNameStyle|value-of<OptionNameStyle> $optionNames short (default), canonical or given
     */
    public function __construct(
        public string $baseUrl = '',
        public string $mount = Constants::QUERY_MOUNT,
        public ?SigningKey $signing = null,
        public ?EncryptionKey $encryption = null,
        public string $separator = Constants::DEFAULT_SEPARATOR,
        OptionNameStyle|string $optionNames = OptionNameStyle::Short,
    ) {
        $this->optionNames = $optionNames instanceof OptionNameStyle ? $optionNames : OptionNameStyle::from($optionNames);
    }

    /**
     * Builds a native /q/?url=… URL: native keys, then imgproxy options as o=.
     *
     * @param QueryImageParams|ParamPairs|null $params native keys (w, fit, g, f, q, preset, …)
     * @param ProcessingOptions|Pipelines<ProcessingOptions>|null $options imgproxy options, written as o=
     *
     * @throws UrlBuildException
     */
    public function url(
        string $source,
        ?array $params = null,
        array|Pipelines|null $options = null,
        ImageFormat|string|null $extension = null,
        #[\SensitiveParameter]
        ?string $pdfPassword = null,
    ): string {
        return $this->buildImage(Options::pipelines($options), $source, self::pairs($params), Streams::extension($extension), $pdfPassword);
    }

    /**
     * Builds a /q/video URL; crop and flip are written c and fl.
     *
     * Pipelines are written in order, so a key may repeat (later wins argument
     * by argument).
     *
     * @param VideoOptions|Pipelines<VideoOptions>|null $options
     *
     * @throws UrlBuildException
     */
    public function videoUrl(string $source, array|Pipelines|null $options = null, VideoContainer|string|null $extension = null): string
    {
        return $this->buildVideo(array_merge(...Options::pipelines($options)), $source, Streams::extension($extension));
    }

    /**
     * Builds a /q/video HLS or DASH manifest URL (format=m3u8|mpd).
     *
     * @param StreamProtocol|value-of<StreamProtocol> $protocol
     * @param list<int>|null $renditions
     * @param SegmentFormat|value-of<SegmentFormat>|null $segmentFormat
     * @param VideoOptions|null $options
     *
     * @throws UrlBuildException
     */
    public function streamUrl(
        string $source,
        StreamProtocol|string $protocol,
        ?array $renditions = null,
        ?int $segmentDuration = null,
        SegmentFormat|string|null $segmentFormat = null,
        ?array $options = null,
    ): string {
        [$entries, $ext] = Streams::parts(Options::pipelines($options), $protocol, $renditions, $segmentDuration, $segmentFormat);

        return $this->buildVideo($entries, $source, $ext);
    }

    /**
     * Builds an imaginary endpoint URL (/q/resize?url=…&width=300).
     *
     * @param ImaginaryEndpoint $endpoint
     * @param ImaginaryParams|ParamPairs|null $params
     *
     * @throws UrlBuildException
     */
    public function imaginaryUrl(string $endpoint, string $source, ?array $params = null): string
    {
        self::checkEndpoint($endpoint);

        return $this->finish("/{$endpoint}", [['url', Text::checkText($source, ErrorCode::InvalidSource, 'source')], ...self::pairs($params)]);
    }

    /**
     * Callers outside static analysis may pass any string.
     *
     * @throws UrlBuildException invalid_endpoint
     */
    private static function checkEndpoint(string $endpoint): void
    {
        if (1 !== preg_match('/^[a-z]+$/D', $endpoint) || 'video' === $endpoint) {
            throw new UrlBuildException(ErrorCode::InvalidEndpoint, \sprintf('invalid imaginary endpoint %s', var_export($endpoint, true)));
        }
    }

    /** Starts a fluent /q/?url=… URL for a source. */
    public function image(string $source): QueryUrlChain
    {
        return new QueryUrlChain(
            fn(ChainState $s): string => $this->buildImage($s->pipelines, $s->source, self::pairs($s->params), $s->extension, $s->pdfPassword),
            $source,
        );
    }

    /** Starts a fluent /q/video URL for a source. */
    public function video(string $source): QueryVideoUrlChain
    {
        return new QueryVideoUrlChain(
            function (ChainState $s): string {
                if (\count(array_filter($s->pipelines, static fn(array $p): bool => [] !== $p)) > 1) {
                    throw new UrlBuildException(ErrorCode::InvalidRequest, '/q/video URLs have no chained pipelines');
                }

                return $this->buildVideo(array_merge(...$s->pipelines), $s->source, $s->extension);
            },
            $source,
        );
    }

    /**
     * @param list<list<Entry>> $pipelines
     * @param list<array{string, string}> $pairs
     *
     * @throws UrlBuildException
     */
    private function buildImage(array $pipelines, string $source, array $pairs, ?string $ext, #[\SensitiveParameter] ?string $pdfPassword): string
    {
        $sep = Streams::separator($this->separator, Constants::FORBIDDEN_QUERY_SEPARATORS);
        $style = $this->optionNames;
        $all = [['url', Text::checkText($source, ErrorCode::InvalidSource, 'source')], ...$pairs];
        $chains = [];
        foreach ($pipelines as $p) {
            if ([] === $p) {
                continue;
            }
            $chains[] = implode('/', array_map(static fn(array $e): string => Options::processing()->formatEntry($e, $sep, $style), $p));
        }
        if ([] !== $chains) {
            $all[] = ['o', implode('/' . Constants::PIPELINE_SEPARATOR . '/', $chains)];
        }
        if (null !== $ext && '' !== $ext) {
            $all[] = ['format', Text::checkExtension($ext)];
        }
        if (null !== $pdfPassword) {
            $token = Streams::passwordToken($this->encryption, $this->signing, $pdfPassword);
            $all[] = [OptionNameStyle::Canonical === $style ? Constants::PDF_PASSWORD_NAME : Constants::PDF_PASSWORD_SHORT, $token];
        }

        return $this->finish('/', $all);
    }

    /**
     * A /q/video URL: every video option is a key, its arguments joined by ",".
     *
     * @param list<Entry> $entries
     *
     * @throws UrlBuildException
     */
    private function buildVideo(array $entries, string $source, ?string $ext): string
    {
        $all = [['url', Text::checkText($source, ErrorCode::InvalidSource, 'source')]];
        $video = Options::video();
        foreach ($entries as [$name, $args]) {
            $key = $video->writtenName($name, $this->optionNames);
            if ([] === $args) {
                throw new UrlBuildException(ErrorCode::InvalidArgument, "option {$name}: at least one argument is required");
            }
            $key = Constants::NATIVE_VIDEO_KEYS[$key] ?? $key;
            $values = [];
            foreach ($args as $i => $a) {
                $where = "option {$name} argument " . ($i + 1);
                $s = Options::formatArg($a, ',', $where);
                if (str_contains($s, ':')) {
                    throw new UrlBuildException(ErrorCode::InvalidArgument, "{$where}: '{$s}' contains ':'");
                }
                $values[] = $s;
            }
            $all[] = [$key, implode(',', $values)];
        }
        if (null !== $ext && '' !== $ext) {
            $all[] = ['format', Text::checkExtension($ext)];
        }

        return $this->finish('/video', $all);
    }

    /**
     * @param list<array{string, string}> $pairs
     *
     * @throws UrlBuildException
     */
    private function finish(string $path, array $pairs): string
    {
        $base = rtrim($this->baseUrl, '/');
        $mount = rtrim($this->mount, '/');
        $query = implode('&', array_map(
            static fn(array $p): string => Text::escapeQueryValue($p[0]) . '=' . Text::escapeQueryValue($p[1]),
            $pairs,
        ));
        if (null !== $this->signing) {
            $query = Signing::signXiQuery($path, $query, $this->signing);
        }

        return "{$base}{$mount}{$path}?{$query}";
    }

    /**
     * Native keys as written pairs: booleans true/false, numbers like ECMAScript; nulls skipped.
     *
     * @param array<mixed>|null $params
     *
     * @return list<array{string, string}>
     *
     * @throws UrlBuildException invalid_argument for an unsupported value
     */
    private static function pairs(?array $params): array
    {
        if (null === $params) {
            return [];
        }
        $items = [];
        if (array_is_list($params)) {
            foreach ($params as $p) {
                if (!\is_array($p) || !\is_string($p[0] ?? null) || !\array_key_exists(1, $p)) {
                    throw new UrlBuildException(ErrorCode::InvalidArgument, 'query parameters must be [key, value] pairs');
                }
                $items[] = [$p[0], $p[1]];
            }
        } else {
            foreach ($params as $k => $v) {
                $items[] = [(string) $k, $v];
            }
        }
        $out = [];
        foreach ($items as [$k, $v]) {
            if (null === $v) {
                continue;
            }
            $out[] = [$k, match (true) {
                \is_bool($v) => $v ? 'true' : 'false',
                \is_int($v), \is_float($v) => Text::formatNumber($v, "query key {$k}"),
                \is_string($v) => $v,
                $v instanceof \BackedEnum => (string) $v->value,
                default => throw new UrlBuildException(ErrorCode::InvalidArgument, "query key {$k}: unsupported value type " . get_debug_type($v)),
            }];
        }

        return $out;
    }
}

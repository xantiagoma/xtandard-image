<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Internal\ChainState;
use Xtandard\Image\Internal\Options;
use Xtandard\Image\Internal\Streams;
use Xtandard\Image\Internal\Text;

/**
 * Builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server (docs/URL-DIALECTS.md §3.1).
 *
 *     {baseUrl}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
 *     {baseUrl}/video/{signature}/{video option}/…/{source}   (signs "/video" + path)
 *
 * Sources are base64url (default), plain/ (percent-escaped, "@ext") or enc/
 * (AES-CBC); base64 and enc sources take ".ext".
 *
 *     $img = new ImgproxyUrlBuilder(baseUrl: 'https://img.example.com', signing: new SigningKey($key, $salt));
 *     $img->url('s3://bucket/cat.jpg', ['rs' => ['fill', 300, 400], 'q' => 80], extension: 'webp');
 *     $img->image('s3://bucket/cat.jpg')->resize('fill', 300, 400)->quality(80)->extension('webp')->build();
 *     $img->videoUrl('s3://bucket/talk.mov', ['rs' => ['fill', 640, 360], 'vs' => 12, 'vd' => 8], extension: 'webm');
 *     $img->streamUrl('s3://bucket/talk.mov', protocol: StreamProtocol::Hls, renditions: [360, 720]);
 *
 * Methods throw UrlBuildException with an ErrorCode when a URL cannot be built.
 *
 * @phpstan-import-type ProcessingOptions from OptionTypes
 * @phpstan-import-type InfoOptions from OptionTypes
 * @phpstan-import-type VideoOptions from OptionTypes
 * @phpstan-import-type Entry from Options
 */
final readonly class ImgproxyUrlBuilder
{
    public SourceEncoding $sourceEncoding;

    public OptionNameStyle $optionNames;

    /**
     * @param string $baseUrl prepended to every path: the server origin and the
     *                        dialect's mount prefix, if any; "" gives root-relative paths
     * @param SigningKey|null $signing signs URLs; without it the signature segment is $unsignedSignature
     * @param string $unsignedSignature signature segment of unsigned URLs: insecure, unsafe or _
     * @param EncryptionKey|null $encryption key for encrypted sources and PDF passwords
     * @param SourceEncoding|value-of<SourceEncoding> $sourceEncoding default source encoding
     * @param int|null $splitSource splits base64 and encrypted sources into "/"-separated chunks of this many characters
     * @param string $separator argument separator (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR)
     * @param OptionNameStyle|value-of<OptionNameStyle> $optionNames short (default), canonical or given
     *
     * @throws UrlBuildException invalid_request for an unknown source encoding
     */
    public function __construct(
        public string $baseUrl = '',
        public ?SigningKey $signing = null,
        public string $unsignedSignature = Constants::DEFAULT_UNSIGNED,
        public ?EncryptionKey $encryption = null,
        SourceEncoding|string $sourceEncoding = SourceEncoding::Base64,
        public ?int $splitSource = null,
        public string $separator = Constants::DEFAULT_SEPARATOR,
        OptionNameStyle|string $optionNames = OptionNameStyle::Short,
    ) {
        $this->sourceEncoding = Streams::encoding($sourceEncoding);
        $this->optionNames = $optionNames instanceof OptionNameStyle ? $optionNames : OptionNameStyle::from($optionNames);
    }

    /**
     * Builds a processing URL; Pipelines chains several options arrays (/-/).
     *
     * @param string $source the source (https://, s3://, xi://, local:///, …)
     * @param ProcessingOptions|Pipelines<ProcessingOptions>|null $options options keyed by name or alias, in URL order
     * @param ImageFormat|string|null $extension output format extension (@webp / .webp)
     * @param SourceEncoding|value-of<SourceEncoding>|null $sourceEncoding overrides the configured source encoding
     * @param string|null $pdfPassword plain-text password of a protected PDF (needs encryption and signing)
     *
     * @throws UrlBuildException
     */
    public function url(
        string $source,
        array|Pipelines|null $options = null,
        ImageFormat|string|null $extension = null,
        SourceEncoding|string|null $sourceEncoding = null,
        #[\SensitiveParameter]
        ?string $pdfPassword = null,
    ): string {
        return $this->build('process', Options::pipelines($options), $source, $sourceEncoding, Streams::extension($extension), $pdfPassword);
    }

    /**
     * Builds an /info URL.
     *
     * @param InfoOptions|null $options
     * @param SourceEncoding|value-of<SourceEncoding>|null $sourceEncoding
     *
     * @throws UrlBuildException
     */
    public function infoUrl(
        string $source,
        ?array $options = null,
        SourceEncoding|string|null $sourceEncoding = null,
        #[\SensitiveParameter]
        ?string $pdfPassword = null,
    ): string {
        return $this->build('info', Options::pipelines($options), $source, $sourceEncoding, null, $pdfPassword);
    }

    /**
     * Builds a /video URL (video transformation, audio or waveform; the extension is the container).
     *
     * @param VideoOptions|null $options
     * @param SourceEncoding|value-of<SourceEncoding>|null $sourceEncoding
     *
     * @throws UrlBuildException
     */
    public function videoUrl(
        string $source,
        ?array $options = null,
        VideoContainer|string|null $extension = null,
        SourceEncoding|string|null $sourceEncoding = null,
    ): string {
        return $this->build('video', Options::pipelines($options), $source, $sourceEncoding, Streams::extension($extension), null);
    }

    /**
     * Builds an HLS or DASH manifest URL (/video/…@m3u8 or @mpd).
     *
     * @param StreamProtocol|value-of<StreamProtocol> $protocol hls or dash
     * @param list<int>|null $renditions rungs by the short side of the frame (at most 8); default: the server's ladder
     * @param int|null $segmentDuration segment length in whole seconds (1 to 30)
     * @param SegmentFormat|value-of<SegmentFormat>|null $segmentFormat fmp4 (CMAF, default) or ts (MPEG-TS, HLS only)
     * @param VideoOptions|null $options other /video options (trim, crop, quality, audio, …) for every rendition
     * @param SourceEncoding|value-of<SourceEncoding>|null $sourceEncoding overrides the configured source encoding
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
        SourceEncoding|string|null $sourceEncoding = null,
    ): string {
        [$entries, $ext] = Streams::parts(Options::pipelines($options), $protocol, $renditions, $segmentDuration, $segmentFormat);

        return $this->build('video', [$entries], $source, $sourceEncoding, $ext, null);
    }

    /** Starts a fluent processing URL for a source. */
    public function image(string $source): ImgproxyUrlChain
    {
        return new ImgproxyUrlChain($this->runner('process'), $source);
    }

    /** Starts a fluent /info URL for a source. */
    public function info(string $source): ImgproxyInfoUrlChain
    {
        return new ImgproxyInfoUrlChain($this->runner('info'), $source);
    }

    /** Starts a fluent /video URL for a source. */
    public function video(string $source): ImgproxyVideoUrlChain
    {
        return new ImgproxyVideoUrlChain($this->runner('video'), $source);
    }

    /**
     * @param 'process'|'info'|'video' $endpoint
     *
     * @return \Closure(ChainState): string
     */
    private function runner(string $endpoint): \Closure
    {
        return fn(ChainState $s): string => $this->build($endpoint, $s->pipelines, $s->source, $s->encoding, $s->extension, $s->pdfPassword);
    }

    /**
     * @param 'process'|'info'|'video' $endpoint
     * @param list<list<Entry>> $pipelines
     *
     * @throws UrlBuildException
     */
    private function build(
        string $endpoint,
        array $pipelines,
        string $source,
        SourceEncoding|string|null $encoding,
        ?string $ext,
        #[\SensitiveParameter]
        ?string $pdfPassword,
    ): string {
        $sep = Streams::separator($this->separator, Constants::FORBIDDEN_SEPARATORS);
        $style = $this->optionNames;
        $cat = match ($endpoint) {
            'info' => Options::info(),
            'video' => Options::video(),
            'process' => Options::processing(),
        };
        $nonEmpty = array_values(array_filter($pipelines, static fn(array $p): bool => [] !== $p));
        if ('process' !== $endpoint && \count($nonEmpty) > 1) {
            throw new UrlBuildException(ErrorCode::InvalidRequest, "/{$endpoint} URLs have no chained pipelines");
        }
        if ('video' === $endpoint && null !== $pdfPassword) {
            throw new UrlBuildException(ErrorCode::InvalidRequest, '/video URLs take no pdf_password');
        }
        $segments = [];
        if (null !== $pdfPassword) {
            $token = Streams::passwordToken($this->encryption, $this->signing, $pdfPassword);
            $segments[] = (OptionNameStyle::Canonical === $style ? Constants::PDF_PASSWORD_NAME : Constants::PDF_PASSWORD_SHORT) . $sep . $token;
        }
        foreach ($nonEmpty as $i => $p) {
            if ($i > 0) {
                $segments[] = Constants::PIPELINE_SEPARATOR;
            }
            foreach ($p as $e) {
                $segments[] = $cat->formatEntry($e, $sep, $style);
            }
        }
        $segments[] = $this->sourceSegment($source, Streams::encoding($encoding ?? $this->sourceEncoding), $ext);
        $path = '/' . implode('/', $segments);
        $base = rtrim($this->baseUrl, '/');
        if ('video' === $endpoint) {
            $sig = null === $this->signing ? $this->unsignedSignature : Signing::sign('/video' . $path, $this->signing);

            return "{$base}/video/{$sig}{$path}";
        }
        $signed = null === $this->signing ? "/{$this->unsignedSignature}{$path}" : Signing::signImgproxyPath($path, $this->signing);

        return $base . ('info' === $endpoint ? '/info' : '') . $signed;
    }

    /** @throws UrlBuildException */
    private function sourceSegment(string $source, SourceEncoding $encoding, ?string $ext): string
    {
        Text::checkText($source, ErrorCode::InvalidSource, 'source');
        $e = null === $ext || '' === $ext ? null : Text::checkExtension($ext);

        return match ($encoding) {
            SourceEncoding::Plain => 'plain/' . Text::escapePlainSource($source) . (null === $e ? '' : "@{$e}"),
            SourceEncoding::Base64 => $this->split(Text::b64url($source)) . (null === $e ? '' : ".{$e}"),
            SourceEncoding::Encrypted => null === $this->encryption
                ? throw new UrlBuildException(ErrorCode::MissingEncryption, 'encrypted sources need an encryption key')
                : Constants::ENCRYPTED_SOURCE_PREFIX . $this->split(Encryption::encryptSource($source, $this->encryption)) . (null === $e ? '' : ".{$e}"),
        };
    }

    private function split(string $token): string
    {
        $n = $this->splitSource;
        if (null === $n || $n <= 0 || \strlen($token) <= $n) {
            return $token;
        }

        return implode('/', str_split($token, $n));
    }
}

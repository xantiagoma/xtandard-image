<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Internal\Text;

/**
 * Source references (docs/URL-DIALECTS.md §3.1), the string a URL carries as its
 * source, and the escapes the builders use.
 *
 *     https://host/path         an http source
 *     s3://bucket/key           gs://bucket/key   abs://container/key
 *     local:///path             ftp://host/path   sftp://host/path
 *     xi://<source>/<key>       a named source (XI_SOURCES)
 */
final class Sources
{
    private function __construct() {}

    /**
     * A named source reference, xi://<name>/<key>. The key may be relative or,
     * for an http source, an absolute URL:
     *
     *     Sources::xiSource('photos', '2026/cat.jpg');                // xi://photos/2026/cat.jpg
     *     Sources::xiSource('web', 'https://cdn.example.com/a.jpg');  // xi://web/https://cdn.example.com/a.jpg
     *
     * @throws UrlBuildException invalid_name when the name is not a source name
     */
    public static function xiSource(string $name, string $key): string
    {
        if (1 !== preg_match('/' . Constants::XI_NAME_PATTERN . '/D', $name)) {
            throw new UrlBuildException(ErrorCode::InvalidName, \sprintf('invalid source name %s', var_export($name, true)));
        }

        return "xi://{$name}/" . ltrim($key, '/');
    }

    /**
     * A source reference for a scheme: the bucket, container, host or source
     * name, then the key. local takes only a path (local:///<path>):
     *
     *     Sources::sourceUrl(SourceScheme::S3, 'photos', 'cat.jpg');  // s3://photos/cat.jpg
     *     Sources::sourceUrl('local', '', 'img/cat.jpg');             // local:///img/cat.jpg
     *
     * @param SourceScheme|value-of<SourceScheme> $scheme
     *
     * @throws UrlBuildException invalid_name when the authority is missing, has "/", "?",
     *                           "#" or whitespace, or is given for local
     */
    public static function sourceUrl(SourceScheme|string $scheme, string $authority, string $key): string
    {
        $s = $scheme instanceof SourceScheme ? $scheme : self::scheme($scheme);
        if (null === $s) {
            throw new UrlBuildException(ErrorCode::InvalidName, \sprintf('unknown source scheme %s', var_export($scheme, true)));
        }
        if (SourceScheme::Xi === $s) {
            return self::xiSource($authority, $key);
        }
        if (SourceScheme::Local === $s) {
            if ('' !== $authority) {
                throw new UrlBuildException(ErrorCode::InvalidName, 'local sources have no authority: local:///<path>');
            }

            return 'local:///' . ltrim($key, '/');
        }
        if ('' === $authority || 1 === preg_match('/[\/?#\s]/', $authority)) {
            throw new UrlBuildException(ErrorCode::InvalidName, \sprintf('invalid %s authority %s', $s->value, var_export($authority, true)));
        }

        return "{$s->value}://{$authority}/" . ltrim($key, '/');
    }

    /** The scheme of a name, or null; callers outside static analysis may pass any string. */
    private static function scheme(string $name): ?SourceScheme
    {
        return SourceScheme::tryFrom($name);
    }

    /**
     * Percent-escapes a source for a plain/ URL; the server path-unescapes it.
     *
     * RFC 3986 unreserved characters, the sub-delims, ":" and "/" are kept;
     * everything else ("%", "@", "?", "#", spaces, non-ASCII) is percent-encoded
     * byte by byte (UTF-8).
     */
    public static function escapePlainSource(string $source): string
    {
        return Text::escapePlainSource($source);
    }

    /** Percent-escapes a query key or value, keeping ":", "/" and "," readable. */
    public static function escapeQueryValue(string $value): string
    {
        return Text::escapeQueryValue($value);
    }
}

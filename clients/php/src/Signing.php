<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Internal\Text;

/**
 * URL signing, byte-compatible with the server (internal/security/sign).
 *
 * - imgproxy: base64url(HMAC-SHA256(key, salt ‖ path)[:size]), the signature
 *   segment of /{signature}/{options}/{source}; /video URLs sign "/video" + path.
 * - xi_sig: the same construction over path ‖ "?" ‖ query-without-xi_sig,
 *   appended as the last query parameter (docs/URL-DIALECTS.md §2).
 */
final class Signing
{
    private function __construct() {}

    /**
     * Signs a message with the salted HMAC-SHA256 of the imgproxy and xi_sig schemes.
     *
     * Like the server, a message without a leading "/" is signed as if it had one.
     */
    public static function sign(string $message, SigningKey $key): string
    {
        $m = str_starts_with($message, '/') ? $message : '/' . $message;

        return Text::b64url($key->mac($m));
    }

    /** "/" + signature + path, for an imgproxy path (everything after the signature segment). */
    public static function signImgproxyPath(string $path, SigningKey $key): string
    {
        $p = str_starts_with($path, '/') ? $path : '/' . $path;

        return '/' . self::sign($p, $key) . $p;
    }

    /**
     * Splits a raw query into its xi_sig value and the message xi_sig signs: the
     * path, then "?" and the other non-empty parameters in their order and encoding.
     *
     * @throws UrlBuildException invalid_request when the query has two xi_sig
     */
    public static function xiMessage(string $path, string $rawQuery): XiMessage
    {
        $sig = null;
        $kept = [];
        foreach (explode('&', $rawQuery) as $part) {
            if ('' === $part) {
                continue;
            }
            $eq = strpos($part, '=');
            $k = false === $eq ? $part : substr($part, 0, $eq);
            if (Constants::XI_SIG_PARAM === $k) {
                if (null !== $sig) {
                    throw new UrlBuildException(ErrorCode::InvalidRequest, 'query has more than one ' . Constants::XI_SIG_PARAM . ' parameter');
                }
                $sig = false === $eq ? '' : substr($part, $eq + 1);
                continue;
            }
            $kept[] = $part;
        }

        return new XiMessage($sig, [] === $kept ? $path : $path . '?' . implode('&', $kept));
    }

    /**
     * $rawQuery without any xi_sig and with a fresh xi_sig for $path appended last.
     *
     * $path is the escaped path the dialect sees, after its mount prefix.
     *
     * @throws UrlBuildException invalid_request when the query has two xi_sig
     */
    public static function signXiQuery(string $path, string $rawQuery, SigningKey $key): string
    {
        $msg = self::xiMessage($path, str_starts_with($rawQuery, '?') ? substr($rawQuery, 1) : $rawQuery)->message;
        $sig = self::sign($msg, $key);
        $q = substr($msg, \strlen($path));

        return '' === $q ? Constants::XI_SIG_PARAM . '=' . $sig : substr($q, 1) . '&' . Constants::XI_SIG_PARAM . '=' . $sig;
    }

    /**
     * Signs a URL (absolute, or a path with an optional query) with xi_sig.
     *
     * $mount is the dialect's mount prefix, which the signed path excludes ("/q"
     * for the query dialect). A fragment is dropped.
     *
     * @throws UrlBuildException invalid_request when the path is outside the mount or the query has two xi_sig
     */
    public static function signXiUrl(string $url, SigningKey $key, string $mount = ''): string
    {
        $noHash = explode('#', $url, 2)[0];
        $qm = strpos($noHash, '?');
        $head = false === $qm ? $noHash : substr($noHash, 0, $qm);
        $query = false === $qm ? '' : substr($noHash, $qm + 1);
        $pathStart = 0;
        if (1 === preg_match('/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//', $head)) {
            $slashes = strpos($head, '//');
            $found = false === $slashes ? false : strpos($head, '/', $slashes + 2);
            $pathStart = false === $found ? -1 : $found;
        }
        $origin = $pathStart < 0 ? $head : substr($head, 0, $pathStart);
        $path = $pathStart < 0 ? '/' : substr($head, $pathStart);
        $m = rtrim($mount, '/');
        if ('' !== $m) {
            if ($path !== $m && !str_starts_with($path, $m . '/')) {
                throw new UrlBuildException(ErrorCode::InvalidRequest, "path {$path} is not under the mount {$m}");
            }
            $path = substr($path, \strlen($m));
            if ('' === $path) {
                $path = '/';
            }
        }

        return $origin . $m . $path . '?' . self::signXiQuery($path, $query, $key);
    }
}

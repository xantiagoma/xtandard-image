<?php

declare(strict_types=1);

// Function forms of the helpers, named like the other official clients
// (clients/README.md, "Required API surface"). Composer autoloads this file;
// each function calls the static method of the same name.

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;

if (!\function_exists(__NAMESPACE__ . '\imgproxy')) {
    /**
     * An ImgproxyUrlBuilder (named arguments as its constructor).
     *
     * @param SourceEncoding|value-of<SourceEncoding> $sourceEncoding
     * @param OptionNameStyle|value-of<OptionNameStyle> $optionNames
     *
     * @throws UrlBuildException
     */
    function imgproxy(
        string $baseUrl = '',
        ?SigningKey $signing = null,
        string $unsignedSignature = Constants::DEFAULT_UNSIGNED,
        ?EncryptionKey $encryption = null,
        SourceEncoding|string $sourceEncoding = SourceEncoding::Base64,
        ?int $splitSource = null,
        string $separator = Constants::DEFAULT_SEPARATOR,
        OptionNameStyle|string $optionNames = OptionNameStyle::Short,
    ): ImgproxyUrlBuilder {
        return new ImgproxyUrlBuilder($baseUrl, $signing, $unsignedSignature, $encryption, $sourceEncoding, $splitSource, $separator, $optionNames);
    }

    /**
     * A QueryUrlBuilder (named arguments as its constructor).
     *
     * @param OptionNameStyle|value-of<OptionNameStyle> $optionNames
     */
    function queryUrls(
        string $baseUrl = '',
        string $mount = Constants::QUERY_MOUNT,
        ?SigningKey $signing = null,
        ?EncryptionKey $encryption = null,
        string $separator = Constants::DEFAULT_SEPARATOR,
        OptionNameStyle|string $optionNames = OptionNameStyle::Short,
    ): QueryUrlBuilder {
        return new QueryUrlBuilder($baseUrl, $mount, $signing, $encryption, $separator, $optionNames);
    }

    /** Signing::sign(). */
    function sign(string $message, SigningKey $key): string
    {
        return Signing::sign($message, $key);
    }

    /** Signing::signImgproxyPath(). */
    function signImgproxyPath(string $path, SigningKey $key): string
    {
        return Signing::signImgproxyPath($path, $key);
    }

    /**
     * Signing::xiMessage().
     *
     * @throws UrlBuildException
     */
    function xiMessage(string $path, string $rawQuery): XiMessage
    {
        return Signing::xiMessage($path, $rawQuery);
    }

    /**
     * Signing::signXiQuery().
     *
     * @throws UrlBuildException
     */
    function signXiQuery(string $path, string $rawQuery, SigningKey $key): string
    {
        return Signing::signXiQuery($path, $rawQuery, $key);
    }

    /**
     * Signing::signXiUrl().
     *
     * @throws UrlBuildException
     */
    function signXiUrl(string $url, SigningKey $key, string $mount = ''): string
    {
        return Signing::signXiUrl($url, $key, $mount);
    }

    /**
     * Encryption::encryptSource().
     *
     * @throws UrlBuildException
     */
    function encryptSource(string $source, EncryptionKey $key): string
    {
        return Encryption::encryptSource($source, $key);
    }

    /**
     * Encryption::encryptPdfPassword().
     *
     * @throws UrlBuildException
     */
    function encryptPdfPassword(#[\SensitiveParameter] string $password, EncryptionKey $key): string
    {
        return Encryption::encryptPdfPassword($password, $key);
    }

    /**
     * Sources::xiSource().
     *
     * @throws UrlBuildException
     */
    function xiSource(string $name, string $key): string
    {
        return Sources::xiSource($name, $key);
    }

    /**
     * Sources::sourceUrl().
     *
     * @param SourceScheme|value-of<SourceScheme> $scheme
     *
     * @throws UrlBuildException
     */
    function sourceUrl(SourceScheme|string $scheme, string $authority, string $key): string
    {
        return Sources::sourceUrl($scheme, $authority, $key);
    }

    /** Sources::escapePlainSource(). */
    function escapePlainSource(string $source): string
    {
        return Sources::escapePlainSource($source);
    }

    /** Sources::escapeQueryValue(). */
    function escapeQueryValue(string $value): string
    {
        return Sources::escapeQueryValue($value);
    }

    /**
     * ByteRange::header().
     *
     * @throws UrlBuildException
     */
    function rangeHeader(ByteRange|ByteSuffix|string $range): string
    {
        return ByteRange::header($range);
    }
}

<?php

declare(strict_types=1);

namespace Xtandard\Image;

/**
 * Why a URL, token or header could not be built: the language-neutral codes of
 * clients/spec/conformance.json (clients/README.md).
 */
enum ErrorCode: string
{
    /** An option name that is not in the catalogue. */
    case UnknownOption = 'unknown_option';
    /** No arguments, a forbidden character, a non-finite number or an unsupported type. */
    case InvalidArgument = 'invalid_argument';
    /** An empty source, or one with control characters. */
    case InvalidSource = 'invalid_source';
    /** An extension that is not alphanumeric. */
    case InvalidExtension = 'invalid_extension';
    /** A separator that is not one character, or that the grammar uses. */
    case InvalidSeparator = 'invalid_separator';
    /** A PDF password without a signing key. */
    case MissingSigning = 'missing_signing';
    /** An encrypted source or a PDF password without an encryption key. */
    case MissingEncryption = 'missing_encryption';
    /** Key material that is not hex, a key or IV of the wrong size, a signature size outside 1 to 32. */
    case InvalidKey = 'invalid_key';
    /** An empty password, one with control characters, or one over 1024 UTF-8 bytes. */
    case InvalidPassword = 'invalid_password';
    /** Chained /info or /video pipelines, a stream option given as an option, an unknown protocol, … */
    case InvalidRequest = 'invalid_request';
    /** A bad xi:// source name or authority. */
    case InvalidName = 'invalid_name';
    /** An imaginary endpoint that is not lower-case letters, or is "video". */
    case InvalidEndpoint = 'invalid_endpoint';
    /** A negative range value, or an end before the start. */
    case InvalidRange = 'invalid_range';
}

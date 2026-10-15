using System.Net;

namespace Xtandard.Image;

/// <summary>Why a URL, token or header could not be built: the codes of clients/spec/conformance.json.</summary>
public enum UrlBuildErrorCode
{
    /// <summary><c>unknown_option</c>: an option name not in the catalogue.</summary>
    UnknownOption,

    /// <summary><c>invalid_argument</c>: no arguments, a forbidden character, a non-finite number, or <c>:</c> in a <c>/q/video</c> argument.</summary>
    InvalidArgument,

    /// <summary><c>invalid_source</c>: an empty source, or a source with control characters.</summary>
    InvalidSource,

    /// <summary><c>invalid_extension</c>: an extension that is not alphanumeric.</summary>
    InvalidExtension,

    /// <summary><c>invalid_separator</c>: a separator that is not one character, or is forbidden.</summary>
    InvalidSeparator,

    /// <summary><c>missing_signing</c>: a PDF password without a signing key.</summary>
    MissingSigning,

    /// <summary><c>missing_encryption</c>: an encrypted source or a PDF password without an encryption key.</summary>
    MissingEncryption,

    /// <summary><c>invalid_key</c>: key material that is not hex, a key of the wrong size, an IV that is not 16 bytes, a signature size outside 1–32.</summary>
    InvalidKey,

    /// <summary><c>invalid_password</c>: an empty password, one with control characters, or one over 1024 UTF-8 bytes.</summary>
    InvalidPassword,

    /// <summary><c>invalid_request</c>: chained /info or /video pipelines, a stream option given as an option, an unknown protocol, <c>ts</c> on DASH, a URL outside the mount, two <c>xi_sig</c>.</summary>
    InvalidRequest,

    /// <summary><c>invalid_name</c>: a bad <c>xi://</c> source name or authority.</summary>
    InvalidName,

    /// <summary><c>invalid_endpoint</c>: an imaginary endpoint that is not lower-case letters, or is <c>video</c>.</summary>
    InvalidEndpoint,

    /// <summary><c>invalid_range</c>: a negative range value, or an end before the start.</summary>
    InvalidRange,
}

/// <summary>The base class of every exception this package throws.</summary>
public class XtandardException : Exception
{
    /// <summary>Creates the exception.</summary>
    public XtandardException()
    {
    }

    /// <summary>Creates the exception.</summary>
    /// <param name="message">The message.</param>
    public XtandardException(string message)
        : base(message)
    {
    }

    /// <summary>Creates the exception.</summary>
    /// <param name="message">The message.</param>
    /// <param name="innerException">The cause.</param>
    public XtandardException(string message, Exception innerException)
        : base(message, innerException)
    {
    }
}

/// <summary>A URL, token or header cannot be built from the given input.</summary>
public sealed class UrlBuildException : XtandardException
{
    /// <summary>Creates the exception.</summary>
    /// <param name="code">The language-neutral error code.</param>
    /// <param name="message">The message.</param>
    public UrlBuildException(UrlBuildErrorCode code, string message)
        : base(message)
    {
        Code = code;
    }

    /// <summary>The error code.</summary>
    public UrlBuildErrorCode Code { get; }

    /// <summary>The error code as written in clients/spec/conformance.json (<c>unknown_option</c>, …).</summary>
    public string CodeName => CodeNameOf(Code);

    /// <summary>The conformance name of a code (<c>unknown_option</c>, …).</summary>
    /// <param name="code">The code.</param>
    /// <returns>The snake_case name.</returns>
    public static string CodeNameOf(UrlBuildErrorCode code) => code switch
    {
        UrlBuildErrorCode.UnknownOption => "unknown_option",
        UrlBuildErrorCode.InvalidArgument => "invalid_argument",
        UrlBuildErrorCode.InvalidSource => "invalid_source",
        UrlBuildErrorCode.InvalidExtension => "invalid_extension",
        UrlBuildErrorCode.InvalidSeparator => "invalid_separator",
        UrlBuildErrorCode.MissingSigning => "missing_signing",
        UrlBuildErrorCode.MissingEncryption => "missing_encryption",
        UrlBuildErrorCode.InvalidKey => "invalid_key",
        UrlBuildErrorCode.InvalidPassword => "invalid_password",
        UrlBuildErrorCode.InvalidRequest => "invalid_request",
        UrlBuildErrorCode.InvalidName => "invalid_name",
        UrlBuildErrorCode.InvalidEndpoint => "invalid_endpoint",
        UrlBuildErrorCode.InvalidRange => "invalid_range",
        _ => throw new ArgumentOutOfRangeException(nameof(code)),
    };
}

/// <summary>The request got no response: connection, TLS or timeout. The cause is the inner exception.</summary>
public sealed class XtandardTransportException : XtandardException
{
    /// <summary>Creates the exception.</summary>
    /// <param name="message">The message.</param>
    /// <param name="innerException">The cause.</param>
    public XtandardTransportException(string message, Exception innerException)
        : base(message, innerException)
    {
    }
}

/// <summary>
/// A non-2xx response. <see cref="Kind"/>, the message and <see cref="RequestId"/> come from the
/// server's JSON error envelope; a body that is not the envelope (a proxy's error page) gives kind
/// <c>unknown</c> and the body text.
/// </summary>
public class XtandardHttpException : XtandardException
{
    /// <summary>Creates the exception.</summary>
    /// <param name="status">The HTTP status.</param>
    /// <param name="url">The request URL.</param>
    /// <param name="message">The client-safe message.</param>
    /// <param name="body">The parsed envelope, when the body was one.</param>
    public XtandardHttpException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
        : base(message)
    {
        Status = status;
        Url = url;
        Body = body;
    }

    /// <summary>The HTTP status.</summary>
    public HttpStatusCode Status { get; }

    /// <summary>The request URL.</summary>
    public string Url { get; }

    /// <summary>The parsed error envelope, when the body was one.</summary>
    public ErrorResponse? Body { get; }

    /// <summary>The error kind (<c>invalid_url</c>, <c>unauthorized</c>, <c>not_found</c>, …) or <c>unknown</c>.</summary>
    public string Kind => Body?.Error.Kind ?? "unknown";

    /// <summary>The request ID from the envelope, also in the server logs.</summary>
    public string? RequestId => Body?.Error.RequestId;

    /// <summary>Creates the exception class of a status.</summary>
    /// <param name="status">The HTTP status.</param>
    /// <param name="url">The request URL.</param>
    /// <param name="message">The client-safe message.</param>
    /// <param name="body">The parsed envelope, when the body was one.</param>
    /// <returns>The exception.</returns>
    public static XtandardHttpException For(HttpStatusCode status, string url, string message, ErrorResponse? body = null) => (int)status switch
    {
        400 => new BadRequestException(status, url, message, body),
        403 => new ForbiddenException(status, url, message, body),
        404 => new NotFoundException(status, url, message, body),
        406 => new NotAcceptableException(status, url, message, body),
        422 => new UnprocessableException(status, url, message, body),
        429 => new TooManyRequestsException(status, url, message, body),
        502 or 504 => new GatewayException(status, url, message, body),
        503 => new ServiceUnavailableException(status, url, message, body),
        >= 500 => new ServerErrorException(status, url, message, body),
        _ => new XtandardHttpException(status, url, message, body),
    };
}

/// <summary>400: the URL is invalid (kind <c>invalid_url</c>).</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public sealed class BadRequestException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : XtandardHttpException(status, url, message, body);

/// <summary>403: a bad or missing signature (<c>unauthorized</c>) or a refused source or option (<c>forbidden</c>).</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public sealed class ForbiddenException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : XtandardHttpException(status, url, message, body);

/// <summary>404: the source does not exist (<c>not_found</c>) or the URL expired (<c>expired</c>).</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public sealed class NotFoundException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : XtandardHttpException(status, url, message, body);

/// <summary>406: no acceptable output format.</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public sealed class NotAcceptableException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : XtandardHttpException(status, url, message, body);

/// <summary>422: the source is too large, unsupported, corrupt or over a processing limit.</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public sealed class UnprocessableException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : XtandardHttpException(status, url, message, body);

/// <summary>429: the server is overloaded; retry later.</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public sealed class TooManyRequestsException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : XtandardHttpException(status, url, message, body);

/// <summary>5xx: the server, an upstream source or a timeout failed.</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public class ServerErrorException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : XtandardHttpException(status, url, message, body);

/// <summary>503: the server is unavailable or draining.</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public sealed class ServiceUnavailableException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : ServerErrorException(status, url, message, body);

/// <summary>502 and 504: an upstream error or timeout.</summary>
/// <param name="status">The HTTP status.</param>
/// <param name="url">The request URL.</param>
/// <param name="message">The client-safe message.</param>
/// <param name="body">The parsed envelope.</param>
public sealed class GatewayException(HttpStatusCode status, string url, string message, ErrorResponse? body = null)
    : ServerErrorException(status, url, message, body);

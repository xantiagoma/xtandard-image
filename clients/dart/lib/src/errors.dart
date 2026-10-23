/// Exceptions: URL-building errors with a language-neutral code, HTTP
/// errors by status, and transport failures.
library;

import 'generated/constants.g.dart';
import 'generated/responses.g.dart';

/// Why a URL, token or header could not be built: the codes of
/// `clients/spec/conformance.json`, shared by every official client.
enum ErrorCode {
  /// An option name not in the catalogue.
  unknownOption('unknown_option'),

  /// No arguments, a forbidden character, a non-finite number, an
  /// unsupported type, or `:` in a `/q/video` argument.
  invalidArgument('invalid_argument'),

  /// An empty source, or a source with control characters.
  invalidSource('invalid_source'),

  /// An extension that does not match [extensionPattern].
  invalidExtension('invalid_extension'),

  /// A separator that is not one character, or is forbidden.
  invalidSeparator('invalid_separator'),

  /// A PDF password without a signing key.
  missingSigning('missing_signing'),

  /// An encrypted source or a PDF password without an encryption key.
  missingEncryption('missing_encryption'),

  /// Key material that is not hex, a key of the wrong size, an IV that is
  /// not 16 bytes, or a signature size outside 1 to 32.
  invalidKey('invalid_key'),

  /// An empty password, one with control characters, or one over 1024
  /// UTF-8 bytes.
  invalidPassword('invalid_password'),

  /// Chained `/info` or `/video` pipelines, a stream option given as an
  /// option, a segment format the protocol refuses, a URL outside the
  /// mount, or two `xi_sig`.
  invalidRequest('invalid_request'),

  /// A bad `xi://` source name or source authority.
  invalidName('invalid_name'),

  /// An imaginary endpoint that is not lower-case letters, or is `video`.
  invalidEndpoint('invalid_endpoint'),

  /// A negative range value, or an end before the start.
  invalidRange('invalid_range');

  const ErrorCode(this.value);

  /// The code as written in the conformance fixture (`invalid_source`).
  final String value;
}

/// Every exception this package throws: a [UrlBuildException], a
/// [XtandardHttpException] or a [TransportException].
sealed class XtandardException implements Exception {
  const XtandardException(this.message);

  /// A description of the failure, safe to log.
  final String message;
}

/// A URL, token or header cannot be built from the given input.
final class UrlBuildException extends XtandardException {
  /// Creates the exception.
  const UrlBuildException(this.code, super.message);

  /// The language-neutral error code.
  final ErrorCode code;

  @override
  String toString() => 'UrlBuildException(${code.value}): $message';
}

/// The request got no response: connection, TLS, a timeout or an abort.
final class TransportException extends XtandardException {
  /// Creates the exception.
  const TransportException(super.message, {this.cause});

  /// The underlying error (an `http.ClientException`, a `TimeoutException`,
  /// …), when there is one.
  final Object? cause;

  @override
  String toString() => 'TransportException: $message';
}

/// A non-2xx response.
///
/// [kind], [message] and [requestId] come from the server's JSON error
/// envelope. A body that is not the envelope (a proxy's error page, say)
/// gives kind `unknown` and the body text. The subclasses follow the
/// status: [BadRequestException] (400), [ForbiddenException] (403),
/// [NotFoundException] (404), [NotAcceptableException] (406),
/// [UnprocessableException] (422), [TooManyRequestsException] (429),
/// [ServerException] (5xx) with [GatewayException] (502, 504) and
/// [ServiceUnavailableException] (503).
class XtandardHttpException extends XtandardException {
  /// Creates the exception; prefer [XtandardHttpException.forStatus].
  XtandardHttpException(this.status, this.url, super.message, {this.body})
      : kind = body?.error.kind ?? 'unknown',
        requestId = body?.error.requestId;

  /// The exception class of [status].
  factory XtandardHttpException.forStatus(
    int status,
    String url,
    String message, {
    ErrorResponse? body,
  }) =>
      switch (status) {
        400 => BadRequestException(status, url, message, body: body),
        403 => ForbiddenException(status, url, message, body: body),
        404 => NotFoundException(status, url, message, body: body),
        406 => NotAcceptableException(status, url, message, body: body),
        422 => UnprocessableException(status, url, message, body: body),
        429 => TooManyRequestsException(status, url, message, body: body),
        502 || 504 => GatewayException(status, url, message, body: body),
        503 => ServiceUnavailableException(status, url, message, body: body),
        >= 500 => ServerException(status, url, message, body: body),
        _ => XtandardHttpException(status, url, message, body: body),
      };

  /// The HTTP status.
  final int status;

  /// The request URL.
  final String url;

  /// The error kind (`invalid_url`, `unauthorized`, `not_found`, …) or
  /// `unknown`.
  final String kind;

  /// The request ID from the envelope, also in the server logs.
  final String? requestId;

  /// The parsed envelope, when the body was one.
  final ErrorResponse? body;

  /// [kind] as an [ErrorKind], or null for `unknown` or a newer kind.
  ErrorKind? get errorKind => ErrorKind.fromValue(kind);

  @override
  String toString() => '$runtimeType($status $kind): $message';
}

/// 400: the URL is invalid (kind `invalid_url`).
final class BadRequestException extends XtandardHttpException {
  /// Creates the exception.
  BadRequestException(super.status, super.url, super.message, {super.body});
}

/// 403: a bad or missing signature (`unauthorized`) or a refused source or
/// option (`forbidden`).
final class ForbiddenException extends XtandardHttpException {
  /// Creates the exception.
  ForbiddenException(super.status, super.url, super.message, {super.body});
}

/// 404: the source does not exist (`not_found`) or the URL expired
/// (`expired`).
final class NotFoundException extends XtandardHttpException {
  /// Creates the exception.
  NotFoundException(super.status, super.url, super.message, {super.body});
}

/// 406: no acceptable output format.
final class NotAcceptableException extends XtandardHttpException {
  /// Creates the exception.
  NotAcceptableException(super.status, super.url, super.message, {super.body});
}

/// 422: the source is too large, unsupported, corrupt or over a processing
/// limit.
final class UnprocessableException extends XtandardHttpException {
  /// Creates the exception.
  UnprocessableException(super.status, super.url, super.message, {super.body});
}

/// 429: the server is overloaded; retry later.
final class TooManyRequestsException extends XtandardHttpException {
  /// Creates the exception.
  TooManyRequestsException(
    super.status,
    super.url,
    super.message, {
    super.body,
  });
}

/// 5xx: the server, an upstream source or a timeout failed.
class ServerException extends XtandardHttpException {
  /// Creates the exception.
  ServerException(super.status, super.url, super.message, {super.body});
}

/// 503: the server is unavailable or draining.
final class ServiceUnavailableException extends ServerException {
  /// Creates the exception.
  ServiceUnavailableException(
    super.status,
    super.url,
    super.message, {
    super.body,
  });
}

/// 502 and 504: an upstream error or timeout.
final class GatewayException extends ServerException {
  /// Creates the exception.
  GatewayException(super.status, super.url, super.message, {super.body});
}

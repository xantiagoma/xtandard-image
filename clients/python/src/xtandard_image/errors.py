"""Exceptions: URL-building errors with a language-neutral code, and HTTP errors by status."""

from __future__ import annotations

from typing import TYPE_CHECKING, Literal

if TYPE_CHECKING:
    from ._generated.responses import ErrorResponse

ErrorCode = Literal[
    "unknown_option",
    "invalid_argument",
    "invalid_source",
    "invalid_extension",
    "invalid_separator",
    "missing_signing",
    "missing_encryption",
    "invalid_key",
    "invalid_password",
    "invalid_request",
    "invalid_name",
    "invalid_endpoint",
    "invalid_range",
]
"""Why a URL could not be built; the codes of clients/spec/conformance.json."""


class XtandardError(Exception):
    """Base class of every exception this package raises."""


class URLBuildError(XtandardError, ValueError):
    """A URL, token or header cannot be built from the given input.

    Attributes:
        code: The language-neutral error code (see clients/README.md).
    """

    def __init__(self, code: ErrorCode, message: str) -> None:
        super().__init__(message)
        self.code: ErrorCode = code


class TransportError(XtandardError):
    """The request did not get a response (connection, TLS, timeout); the httpx error is the cause."""


class HTTPError(XtandardError):
    """A non-2xx response.

    ``kind``, ``message`` and ``request_id`` come from the server's JSON error
    envelope; a body that is not the envelope (a proxy's error page, say)
    yields kind ``"unknown"`` and the body text.

    Attributes:
        status: The HTTP status.
        kind: The error kind (``invalid_url``, ``unauthorized``, ``not_found``, …) or ``"unknown"``.
        request_id: The request ID from the envelope, also in the server logs.
        body: The parsed envelope, when the body was one.
        url: The request URL.
    """

    def __init__(self, status: int, url: str, message: str, body: ErrorResponse | None = None) -> None:
        super().__init__(message)
        self.status = status
        self.url = url
        self.body = body
        self.kind: str = body["error"]["kind"] if body is not None else "unknown"
        self.request_id: str | None = body["error"].get("request_id") if body is not None else None

    @property
    def message(self) -> str:
        """The client-safe message."""
        return str(self)


class BadRequestError(HTTPError):
    """400: the URL is invalid (kind ``invalid_url``)."""


class ForbiddenError(HTTPError):
    """403: a bad or missing signature (``unauthorized``) or a refused source or option (``forbidden``)."""


class NotFoundError(HTTPError):
    """404: the source does not exist (``not_found``) or the URL expired (``expired``)."""


class NotAcceptableError(HTTPError):
    """406: no acceptable output format."""


class UnprocessableError(HTTPError):
    """422: the source is too large, unsupported, corrupt or over a processing limit."""


class TooManyRequestsError(HTTPError):
    """429: the server is overloaded; retry later."""


class ServerError(HTTPError):
    """5xx: the server, an upstream source or a timeout failed."""


class ServiceUnavailableError(ServerError):
    """503: the server is unavailable or draining."""


class GatewayError(ServerError):
    """502 and 504: an upstream error or timeout."""


def error_class(status: int) -> type[HTTPError]:
    """Returns the exception class of an HTTP status."""
    specific: dict[int, type[HTTPError]] = {
        400: BadRequestError,
        403: ForbiddenError,
        404: NotFoundError,
        406: NotAcceptableError,
        422: UnprocessableError,
        429: TooManyRequestsError,
        502: GatewayError,
        503: ServiceUnavailableError,
        504: GatewayError,
    }
    if status in specific:
        return specific[status]
    return ServerError if status >= 500 else HTTPError

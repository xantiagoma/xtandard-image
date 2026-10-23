"""HTTP clients for xtandard-image on httpx: :class:`Client` (sync) and :class:`AsyncClient`.

They cover the JSON and health endpoints (``/info``, ``/healthz``,
``/readyz``, the playground's ``api/meta``) and media fetches (images,
``/video`` results with Range and HEAD, HLS and DASH manifests). URLs are
built (and signed) with :class:`~xtandard_image.ImgproxyURLBuilder`.
Non-2xx responses raise an :class:`~xtandard_image.errors.HTTPError`
subclass chosen by status; transport failures raise
:class:`~xtandard_image.errors.TransportError`.
"""

from __future__ import annotations

import json
import re
import urllib.parse
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Literal, cast

import httpx

from ._generated.constants import DEFAULT_SEPARATOR, DEFAULT_UNSIGNED, STREAM_MANIFESTS
from .builder import ImgproxyURLBuilder
from .errors import HTTPError, TransportError, URLBuildError, error_class
from .query import query_method_request

if TYPE_CHECKING:
    from collections.abc import Mapping, Sequence
    from types import TracebackType

    from ._generated.constants import SegmentFormat, SourceEncoding, StreamProtocol, VideoContainer
    from ._generated.options import InfoOptions, ProcessingOptions, VideoOptions
    from ._generated.responses import ErrorResponse, InfoResponse
    from .encryption import EncryptionKey
    from .options import OptionNameStyle
    from .signing import SigningKey

__all__ = [
    "AsyncClient",
    "ByteRange",
    "ByteSuffix",
    "Client",
    "Readiness",
    "StreamManifest",
    "range_header",
]

ReadyState = Literal["ready", "starting", "draining"]
Method = Literal["GET", "HEAD"]
MediaMethod = Literal["GET", "HEAD", "QUERY"]


@dataclass(frozen=True)
class ByteRange:
    """Bytes ``start`` to ``end`` (inclusive), or to the end when ``end`` is None."""

    start: int
    end: int | None = None


@dataclass(frozen=True)
class ByteSuffix:
    """The last ``length`` bytes."""

    length: int


def _non_negative(n: object, what: str) -> int:
    if isinstance(n, bool) or not isinstance(n, int) or n < 0:
        raise URLBuildError("invalid_range", f"range {what} {n!r} is not a non-negative integer")
    return n


def range_header(r: str | ByteRange | ByteSuffix) -> str:
    """Formats a byte range as a Range header value (``bytes=0-1023``, ``bytes=-500``)."""
    if isinstance(r, str):
        return r
    if isinstance(r, ByteSuffix):
        return f"bytes=-{_non_negative(r.length, 'suffix')}"
    start = _non_negative(r.start, "start")
    if r.end is None:
        return f"bytes={start}-"
    end = _non_negative(r.end, "end")
    if end < start:
        raise URLBuildError("invalid_range", f"range end {end} is before start {start}")
    return f"bytes={start}-{end}"


@dataclass(frozen=True)
class Readiness:
    """The ``/readyz`` answer: ``ready`` is true only for 200 ``ready``."""

    ready: bool
    state: ReadyState


@dataclass(frozen=True)
class StreamManifest:
    """A fetched HLS playlist or DASH MPD.

    Attributes:
        url: The (signed) manifest URL; hand it to a player.
        content_type: ``application/vnd.apple.mpegurl`` or ``application/dash+xml``.
        body: The manifest; its child URLs (``/_stream/<ticket>/…``) carry a ticket and need no signing.
    """

    url: str
    content_type: str
    body: str


_SCHEME = re.compile(r"^[a-zA-Z][a-zA-Z0-9+.-]*:")


def _envelope(text: str) -> ErrorResponse | None:
    try:
        v: object = json.loads(text)
    except ValueError:
        return None
    if not isinstance(v, dict):
        return None
    err = cast("dict[str, object]", v).get("error")
    if not isinstance(err, dict):
        return None
    e = cast("dict[str, object]", err)
    if not isinstance(e.get("kind"), str) or not isinstance(e.get("message"), str):
        return None
    return cast("ErrorResponse", v)


def to_http_error(res: httpx.Response) -> HTTPError:
    """Converts a non-2xx response (body already read) into an :class:`HTTPError` subclass."""
    try:
        text = res.text
    except (httpx.ResponseNotRead, UnicodeDecodeError):
        text = ""
    body = _envelope(text)
    if body is not None:
        message = body["error"]["message"]
    else:
        message = text.strip() or res.reason_phrase or f"HTTP {res.status_code}"
    return error_class(res.status_code)(res.status_code, str(res.request.url), message, body)


class _Core:
    """What the sync and async clients share: URLs, headers, response checks."""

    def __init__(
        self,
        base_url: str,
        *,
        signing: SigningKey | None,
        encryption: EncryptionKey | None,
        unsigned_signature: str,
        source_encoding: SourceEncoding,
        split_source: int | None,
        separator: str,
        option_names: OptionNameStyle,
        headers: Mapping[str, str] | None,
        playground_base_url: str | None,
        playground_path: str,
        query_method: bool,
        query_mount: str,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.urls = ImgproxyURLBuilder(
            base_url=self.base_url,
            signing=signing,
            unsigned_signature=unsigned_signature,
            encryption=encryption,
            source_encoding=source_encoding,
            split_source=split_source,
            separator=separator,
            option_names=option_names,
        )
        self._headers = dict(headers or {})
        origin = (playground_base_url or self.base_url).rstrip("/")
        self._playground_meta = f"{origin}/{playground_path.strip('/')}/api/meta"
        self._query_method = query_method
        self._query_mount = query_mount.rstrip("/")

    def _abs(self, url: str) -> str:
        if _SCHEME.match(url):
            return url
        return f"{self.base_url}{'' if url.startswith('/') else '/'}{url}"

    def _headers_for(
        self, accept: str, range_: str | ByteRange | ByteSuffix | None, headers: Mapping[str, str] | None
    ) -> dict[str, str]:
        h = {**self._headers, **(headers or {}), "Accept": accept}
        if range_ is not None:
            h["Range"] = range_header(range_)
        return h

    def _under_query_mount(self, url: str) -> bool:
        path = urllib.parse.urlsplit(url).path
        return path == self._query_mount or path.startswith(f"{self._query_mount}/")

    def _media_request(
        self, method: MediaMethod | None, url: str, headers: dict[str, str]
    ) -> tuple[MediaMethod, str, str | None]:
        """The method, URL and content of a media() call; a QUERY call moves the URL's query to the content."""
        if method == "QUERY" or (method is None and self._query_method and self._under_query_mount(url)):
            q = query_method_request(url)
            headers["Content-Type"] = q.content_type
            return q.method, q.url, q.body
        return method or "GET", url, None

    @staticmethod
    def _readiness(res: httpx.Response) -> Readiness:
        if res.status_code not in (200, 503):
            raise to_http_error(res)
        text = res.text.strip()
        states: dict[str, ReadyState] = {"ready": "ready", "starting": "starting", "draining": "draining"}
        state = states.get(text)
        if state is None:
            raise HTTPError(res.status_code, str(res.request.url), f"unexpected readiness state {text!r}")
        return Readiness(ready=res.status_code == 200 and state == "ready", state=state)

    @staticmethod
    def _manifest_type(protocol: str) -> str:
        m = STREAM_MANIFESTS.get(protocol)
        return m[1] if m else "application/octet-stream"


class Client(_Core):
    """A synchronous client.

    ::

        with Client("http://localhost:8080", signing=SigningKey(key, salt)) as xi:
            info = xi.info("s3://bucket/cat.jpg", {"size": True, "format": True})
            head = xi.video("s3://bucket/talk.mov", {"vd": 5}, extension="webm", method="HEAD")

    Args:
        base_url: Server origin, plus the imgproxy mount prefix if any.
        signing: Signs the URLs the client builds.
        encryption: Key for encrypted sources and PDF passwords.
        unsigned_signature: Signature segment of unsigned URLs.
        source_encoding: Default source encoding.
        split_source: Splits base64 and encrypted sources into chunks.
        separator: Argument separator.
        option_names: How option names are written.
        headers: Headers sent with every request.
        timeout: Request timeout in seconds (when the client creates its own httpx client).
        http_client: An httpx.Client to use (and not close); default: one owned by this client.
        playground_base_url: Origin of the admin listener for ``playground_meta()``; default ``base_url``.
        playground_path: XI_PLAYGROUND_PATH. Default ``/_playground/``.
        query_method: Send query dialect URLs (those under ``query_mount``) passed to
            :meth:`media` with the HTTP QUERY method (RFC 10008): the query moves into the
            request content. Off by default: the server needs XI_SERVER_QUERY_METHOD (on by
            default), and many CDNs and proxies neither cache nor forward QUERY.
            ``media(url, method="QUERY")`` does the same for one call.
        query_mount: The query dialect's mount path (XI_DIALECTS_QUERY_PREFIX), for
            ``query_method``. Default ``/q``.
    """

    def __init__(
        self,
        base_url: str,
        *,
        signing: SigningKey | None = None,
        encryption: EncryptionKey | None = None,
        unsigned_signature: str = DEFAULT_UNSIGNED,
        source_encoding: SourceEncoding = "base64",
        split_source: int | None = None,
        separator: str = DEFAULT_SEPARATOR,
        option_names: OptionNameStyle = "short",
        headers: Mapping[str, str] | None = None,
        timeout: float = 60.0,
        http_client: httpx.Client | None = None,
        playground_base_url: str | None = None,
        playground_path: str = "/_playground/",
        query_method: bool = False,
        query_mount: str = "/q",
    ) -> None:
        super().__init__(
            base_url,
            signing=signing,
            encryption=encryption,
            unsigned_signature=unsigned_signature,
            source_encoding=source_encoding,
            split_source=split_source,
            separator=separator,
            option_names=option_names,
            headers=headers,
            playground_base_url=playground_base_url,
            playground_path=playground_path,
            query_method=query_method,
            query_mount=query_mount,
        )
        self._owned = http_client is None
        self._http = http_client if http_client is not None else httpx.Client(timeout=timeout)

    def close(self) -> None:
        """Closes the underlying httpx client if this client created it."""
        if self._owned:
            self._http.close()

    def __enter__(self) -> Client:
        return self

    def __exit__(
        self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None
    ) -> None:
        self.close()

    def _send(
        self,
        method: MediaMethod,
        url: str,
        headers: dict[str, str],
        *,
        stream: bool = False,
        content: str | None = None,
    ) -> httpx.Response:
        try:
            req = self._http.build_request(method, url, headers=headers, content=content)
            return self._http.send(req, stream=stream)
        except httpx.TransportError as e:
            raise TransportError(f"{method} {url}: {e}") from e

    def _checked(self, res: httpx.Response) -> httpx.Response:
        if not res.is_success:
            res.read()
            res.close()
            raise to_http_error(res)
        return res

    def _json(self, url: str) -> Any:  # noqa: ANN401  # decoded JSON, typed by the caller
        return self._checked(self._send("GET", url, self._headers_for("application/json", None, None))).json()

    def healthz(self) -> None:
        """``GET /healthz``: returns while the process can serve HTTP."""
        self._checked(self._send("GET", f"{self.base_url}/healthz", self._headers_for("text/plain", None, None)))

    def readyz(self) -> Readiness:
        """``GET /readyz``: the readiness state; 503 (starting, draining) is not an error."""
        return self._readiness(
            self._send("GET", f"{self.base_url}/readyz", self._headers_for("text/plain", None, None))
        )

    def info(
        self,
        source: str,
        options: InfoOptions | None = None,
        *,
        source_encoding: SourceEncoding | None = None,
        pdf_password: str | None = None,
    ) -> InfoResponse:
        """``GET /info`` for a source, with the URL built (and signed) from the arguments."""
        url = self.urls.info_url(source, options, source_encoding=source_encoding, pdf_password=pdf_password)
        return cast("InfoResponse", self._json(url))

    def info_at(self, url: str) -> InfoResponse:
        """``GET`` a prebuilt ``/info/…`` path or URL."""
        return cast("InfoResponse", self._json(self._abs(url)))

    def media(
        self,
        url: str,
        *,
        method: MediaMethod | None = None,
        range: str | ByteRange | ByteSuffix | None = None,  # the HTTP header's name
        accept: str = "*/*",
        headers: Mapping[str, str] | None = None,
        stream: bool = False,
    ) -> httpx.Response:
        """``GET`` (or ``HEAD``) any URL or server path: an image or video URL, a query URL, a stream part.

        ``method`` defaults to GET, or to QUERY for a query dialect URL when the
        client has ``query_method``; ``method="QUERY"`` sends the URL's query as
        the request content (see :func:`~xtandard_image.query_method_request`).
        With ``stream=True`` the body is not read: iterate it, then close the response.
        """
        h = self._headers_for(accept, range, headers)
        m, u, content = self._media_request(method, self._abs(url), h)
        return self._checked(self._send(m, u, h, stream=stream, content=content))

    def image(
        self,
        source: str,
        options: ProcessingOptions | Sequence[ProcessingOptions] | None = None,
        *,
        extension: str | None = None,
        source_encoding: SourceEncoding | None = None,
        pdf_password: str | None = None,
        method: Method = "GET",
        accept: str = "*/*",
        headers: Mapping[str, str] | None = None,
        stream: bool = False,
    ) -> httpx.Response:
        """``GET`` (or ``HEAD``) a processed image; ``accept`` drives format negotiation."""
        url = self.urls.url(
            source, options, extension=extension, source_encoding=source_encoding, pdf_password=pdf_password
        )
        return self.media(url, method=method, accept=accept, headers=headers, stream=stream)

    def video(
        self,
        source: str,
        options: VideoOptions | None = None,
        *,
        extension: VideoContainer | str | None = None,
        source_encoding: SourceEncoding | None = None,
        method: Method = "GET",
        range: str | ByteRange | ByteSuffix | None = None,
        accept: str = "*/*",
        headers: Mapping[str, str] | None = None,
        stream: bool = False,
    ) -> httpx.Response:
        """``GET`` (or ``HEAD``) a ``/video`` result: video, audio or a waveform; ``range`` asks for part (206)."""
        url = self.urls.video_url(source, options, extension=extension, source_encoding=source_encoding)
        return self.media(url, method=method, range=range, accept=accept, headers=headers, stream=stream)

    def stream(
        self,
        source: str,
        *,
        protocol: StreamProtocol,
        renditions: Sequence[int] | None = None,
        segment_duration: int | None = None,
        segment_format: SegmentFormat | None = None,
        options: VideoOptions | None = None,
        source_encoding: SourceEncoding | None = None,
        headers: Mapping[str, str] | None = None,
    ) -> StreamManifest:
        """``GET`` an HLS or DASH manifest (``/video/…@m3u8`` or ``@mpd``)."""
        url = self.urls.stream_url(
            source,
            protocol=protocol,
            renditions=renditions,
            segment_duration=segment_duration,
            segment_format=segment_format,
            options=options,
            source_encoding=source_encoding,
        )
        ctype = self._manifest_type(protocol)
        res = self._checked(self._send("GET", url, self._headers_for(f"{ctype}, */*;q=0.5", None, headers)))
        return StreamManifest(url=url, content_type=res.headers.get("content-type", ctype), body=res.text)

    def playground_meta(self) -> dict[str, Any]:
        """``GET`` the playground's ``api/meta`` (needs XI_PLAYGROUND_ENABLED; admin listener by default)."""
        return cast("dict[str, Any]", self._json(self._playground_meta))


class AsyncClient(_Core):
    """An asynchronous client with the same methods as :class:`Client`, awaitable.

    ::

        async with AsyncClient("http://localhost:8080", signing=SigningKey(key, salt)) as xi:
            info = await xi.info("s3://bucket/cat.jpg", {"size": True})
    """

    def __init__(
        self,
        base_url: str,
        *,
        signing: SigningKey | None = None,
        encryption: EncryptionKey | None = None,
        unsigned_signature: str = DEFAULT_UNSIGNED,
        source_encoding: SourceEncoding = "base64",
        split_source: int | None = None,
        separator: str = DEFAULT_SEPARATOR,
        option_names: OptionNameStyle = "short",
        headers: Mapping[str, str] | None = None,
        timeout: float = 60.0,
        http_client: httpx.AsyncClient | None = None,
        playground_base_url: str | None = None,
        playground_path: str = "/_playground/",
        query_method: bool = False,
        query_mount: str = "/q",
    ) -> None:
        super().__init__(
            base_url,
            signing=signing,
            encryption=encryption,
            unsigned_signature=unsigned_signature,
            source_encoding=source_encoding,
            split_source=split_source,
            separator=separator,
            option_names=option_names,
            headers=headers,
            playground_base_url=playground_base_url,
            playground_path=playground_path,
            query_method=query_method,
            query_mount=query_mount,
        )
        self._owned = http_client is None
        self._http = http_client if http_client is not None else httpx.AsyncClient(timeout=timeout)

    async def aclose(self) -> None:
        """Closes the underlying httpx client if this client created it."""
        if self._owned:
            await self._http.aclose()

    async def __aenter__(self) -> AsyncClient:
        return self

    async def __aexit__(
        self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None
    ) -> None:
        await self.aclose()

    async def _send(
        self,
        method: MediaMethod,
        url: str,
        headers: dict[str, str],
        *,
        stream: bool = False,
        content: str | None = None,
    ) -> httpx.Response:
        try:
            req = self._http.build_request(method, url, headers=headers, content=content)
            return await self._http.send(req, stream=stream)
        except httpx.TransportError as e:
            raise TransportError(f"{method} {url}: {e}") from e

    async def _checked(self, res: httpx.Response) -> httpx.Response:
        if not res.is_success:
            await res.aread()
            await res.aclose()
            raise to_http_error(res)
        return res

    async def _json(self, url: str) -> Any:  # noqa: ANN401  # decoded JSON, typed by the caller
        res = await self._checked(await self._send("GET", url, self._headers_for("application/json", None, None)))
        return res.json()

    async def healthz(self) -> None:
        """``GET /healthz``: returns while the process can serve HTTP."""
        await self._checked(
            await self._send("GET", f"{self.base_url}/healthz", self._headers_for("text/plain", None, None))
        )

    async def readyz(self) -> Readiness:
        """``GET /readyz``: the readiness state; 503 (starting, draining) is not an error."""
        return self._readiness(
            await self._send("GET", f"{self.base_url}/readyz", self._headers_for("text/plain", None, None))
        )

    async def info(
        self,
        source: str,
        options: InfoOptions | None = None,
        *,
        source_encoding: SourceEncoding | None = None,
        pdf_password: str | None = None,
    ) -> InfoResponse:
        """``GET /info`` for a source, with the URL built (and signed) from the arguments."""
        url = self.urls.info_url(source, options, source_encoding=source_encoding, pdf_password=pdf_password)
        return cast("InfoResponse", await self._json(url))

    async def info_at(self, url: str) -> InfoResponse:
        """``GET`` a prebuilt ``/info/…`` path or URL."""
        return cast("InfoResponse", await self._json(self._abs(url)))

    async def media(
        self,
        url: str,
        *,
        method: MediaMethod | None = None,
        range: str | ByteRange | ByteSuffix | None = None,  # the HTTP header's name
        accept: str = "*/*",
        headers: Mapping[str, str] | None = None,
        stream: bool = False,
    ) -> httpx.Response:
        """``GET`` (or ``HEAD``, or ``QUERY``) any URL or server path, as :meth:`Client.media`.

        With ``stream=True`` the body is not read (``aclose()`` it).
        """
        h = self._headers_for(accept, range, headers)
        m, u, content = self._media_request(method, self._abs(url), h)
        return await self._checked(await self._send(m, u, h, stream=stream, content=content))

    async def image(
        self,
        source: str,
        options: ProcessingOptions | Sequence[ProcessingOptions] | None = None,
        *,
        extension: str | None = None,
        source_encoding: SourceEncoding | None = None,
        pdf_password: str | None = None,
        method: Method = "GET",
        accept: str = "*/*",
        headers: Mapping[str, str] | None = None,
        stream: bool = False,
    ) -> httpx.Response:
        """``GET`` (or ``HEAD``) a processed image."""
        url = self.urls.url(
            source, options, extension=extension, source_encoding=source_encoding, pdf_password=pdf_password
        )
        return await self.media(url, method=method, accept=accept, headers=headers, stream=stream)

    async def video(
        self,
        source: str,
        options: VideoOptions | None = None,
        *,
        extension: VideoContainer | str | None = None,
        source_encoding: SourceEncoding | None = None,
        method: Method = "GET",
        range: str | ByteRange | ByteSuffix | None = None,
        accept: str = "*/*",
        headers: Mapping[str, str] | None = None,
        stream: bool = False,
    ) -> httpx.Response:
        """``GET`` (or ``HEAD``) a ``/video`` result; ``range`` asks for part of it (206)."""
        url = self.urls.video_url(source, options, extension=extension, source_encoding=source_encoding)
        return await self.media(url, method=method, range=range, accept=accept, headers=headers, stream=stream)

    async def stream(
        self,
        source: str,
        *,
        protocol: StreamProtocol,
        renditions: Sequence[int] | None = None,
        segment_duration: int | None = None,
        segment_format: SegmentFormat | None = None,
        options: VideoOptions | None = None,
        source_encoding: SourceEncoding | None = None,
        headers: Mapping[str, str] | None = None,
    ) -> StreamManifest:
        """``GET`` an HLS or DASH manifest."""
        url = self.urls.stream_url(
            source,
            protocol=protocol,
            renditions=renditions,
            segment_duration=segment_duration,
            segment_format=segment_format,
            options=options,
            source_encoding=source_encoding,
        )
        ctype = self._manifest_type(protocol)
        res = await self._checked(await self._send("GET", url, self._headers_for(f"{ctype}, */*;q=0.5", None, headers)))
        return StreamManifest(url=url, content_type=res.headers.get("content-type", ctype), body=res.text)

    async def playground_meta(self) -> dict[str, Any]:
        """``GET`` the playground's ``api/meta``."""
        return cast("dict[str, Any]", await self._json(self._playground_meta))

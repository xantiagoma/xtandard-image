"""Query dialect URLs (docs/URL-DIALECTS.md §4), mounted at ``/q/``.

::

    {base_url}{mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&xi_sig=…]
    {base_url}{mount}/video?url=<source>&<video option>=<arg,arg>…[&xi_sig=…]
    {base_url}{mount}/<imaginary endpoint>?url=<source>&<param>=…[&xi_sig=…]

xi_sig covers the escaped path after the mount plus the query, so it is appended last.
"""

from __future__ import annotations

import re
import sys
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Final, Literal

from ._generated.constants import (
    DEFAULT_SEPARATOR,
    FORBIDDEN_QUERY_SEPARATORS,
    NATIVE_VIDEO_KEYS,
    PDF_PASSWORD_NAME,
    PDF_PASSWORD_SHORT,
    QUERY_MOUNT,
    ImaginaryEndpoint,
    ImaginaryParams,
    QueryImageParams,
    QueryValue,
    SegmentFormat,
    StreamProtocol,
    VideoContainer,
)
from ._generated.methods import ProcessingMethods, VideoMethods
from ._text import check_extension, check_text, escape_query_value, format_number
from .builder import ChainState, WithExtension, WithPipeline, password_token, stream_parts
from .builder import check_separator as _check_separator
from .errors import URLBuildError
from .options import (
    PROCESSING,
    VIDEO,
    OptionEntry,
    OptionNameStyle,
    format_arg,
    format_entry,
    pipelines_of,
    written_name,
)
from .signing import SigningKey, sign_xi_query

if sys.version_info >= (3, 11):
    from typing import Self
else:
    from typing_extensions import Self

if TYPE_CHECKING:
    from ._generated.options import ProcessingOptions, VideoOptions
    from .encryption import EncryptionKey

__all__ = [
    "QUERY_CONTENT_TYPE",
    "QueryMethodRequest",
    "QueryURLBuilder",
    "QueryURLChain",
    "QueryVideoURLChain",
    "query_method_request",
    "query_urls",
]

ParamList = Mapping[str, object] | Sequence[tuple[str, QueryValue | None]]
"""Native keys: a mapping (key order) or (key, value) pairs (keys may repeat)."""


@dataclass(frozen=True)
class QueryConfig:
    """Settings of query dialect URLs.

    Attributes:
        base_url: Server origin (and any path before the mount). Default ``""``.
        mount: The query dialect's mount prefix (XI_DIALECTS_QUERY_PREFIX). Default ``/q``.
        signing: Signs with xi_sig (the imgproxy key and salt); without it URLs are unsigned.
        encryption: Key for PDF passwords (XI_ENCRYPTION_KEYS).
        separator: Argument separator inside ``o=``.
        option_names: ``short`` (default), ``canonical`` or ``given``.
    """

    base_url: str = ""
    mount: str = QUERY_MOUNT
    signing: SigningKey | None = field(default=None, repr=False)
    encryption: EncryptionKey | None = field(default=None, repr=False)
    separator: str = DEFAULT_SEPARATOR
    option_names: OptionNameStyle = "short"


def _native_value(key: str, v: object) -> str:
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, int | float):
        return format_number(v, f"query key {key}")
    if isinstance(v, str):
        return v
    raise URLBuildError("invalid_argument", f"query key {key}: unsupported value type {type(v).__name__}")


def _param_pairs(params: ParamList | None) -> list[tuple[str, str]]:
    if params is None:
        return []
    items = params.items() if isinstance(params, Mapping) else params
    return [(k, _native_value(k, v)) for k, v in items if v is not None]


def _finish(cfg: QueryConfig, path: str, pairs: Sequence[tuple[str, str]]) -> str:
    base = cfg.base_url.rstrip("/")
    mount = cfg.mount.rstrip("/")
    query = "&".join(f"{escape_query_value(k)}={escape_query_value(v)}" for k, v in pairs)
    if cfg.signing is not None:
        query = sign_xi_query(path, query, cfg.signing)
    return f"{base}{mount}{path}?{query}"


def build_query_image(
    cfg: QueryConfig,
    pipelines: Sequence[Sequence[OptionEntry]],
    source: str,
    params: ParamList | None,
    ext: str | None,
    pdf_password: str | None,
) -> str:
    """A native ``/q/`` image URL; ``o=`` carries the option pipelines."""
    sep = _check_separator(cfg.separator, FORBIDDEN_QUERY_SEPARATORS)
    style = cfg.option_names
    pairs = [("url", check_text(source, "invalid_source", "source")), *_param_pairs(params)]
    non_empty = [p for p in pipelines if p]
    if non_empty:
        o = "/-/".join("/".join(format_entry(PROCESSING, e, sep, style) for e in p) for p in non_empty)
        pairs.append(("o", o))
    if ext:
        pairs.append(("format", check_extension(ext)))
    if pdf_password is not None:
        token = password_token(cfg.encryption, cfg.signing, pdf_password)
        pairs.append((PDF_PASSWORD_NAME if style == "canonical" else PDF_PASSWORD_SHORT, token))
    return _finish(cfg, "/", pairs)


def _single_pipeline(pipelines: Sequence[Sequence[OptionEntry]]) -> list[OptionEntry]:
    if len([p for p in pipelines if p]) > 1:
        raise URLBuildError("invalid_request", "/q/video URLs have no chained pipelines")
    return [e for p in pipelines for e in p]


def build_query_video(cfg: QueryConfig, entries: Sequence[OptionEntry], source: str, ext: str | None) -> str:
    """A ``/q/video`` URL: every video option is a key, its arguments joined by ``,``."""
    pairs = [("url", check_text(source, "invalid_source", "source"))]
    for name, args in entries:
        key = written_name(VIDEO, name, cfg.option_names)
        if not args:
            raise URLBuildError("invalid_argument", f"option {name}: at least one argument is required")
        key = NATIVE_VIDEO_KEYS.get(key, key)
        values: list[str] = []
        for i, a in enumerate(args):
            s = format_arg(a, ",", f"option {name} argument {i + 1}")
            if ":" in s:
                raise URLBuildError("invalid_argument", f"option {name} argument {i + 1}: {s!r} contains ':'")
            values.append(s)
        pairs.append((key, ",".join(values)))
    if ext:
        pairs.append(("format", check_extension(ext)))
    return _finish(cfg, "/video", pairs)


_ENDPOINT = re.compile(r"[a-z]+")


def build_imaginary(cfg: QueryConfig, endpoint: str, source: str, params: ParamList | None) -> str:
    """An imaginary endpoint URL (``/q/resize?url=…&width=300``)."""
    if not _ENDPOINT.fullmatch(endpoint) or endpoint == "video":
        raise URLBuildError("invalid_endpoint", f"invalid imaginary endpoint {endpoint!r}")
    return _finish(
        cfg, f"/{endpoint}", [("url", check_text(source, "invalid_source", "source")), *_param_pairs(params)]
    )


class QueryURLChain(WithExtension, WithPipeline, ProcessingMethods):
    """A fluent ``/q/?url=…`` URL: the option methods write ``o=``, ``param()`` a native key."""

    def param(self, key: str, value: QueryValue | None) -> Self:
        """Appends a native key (``w``, ``fit``, ``g``, …), written before ``o=``."""
        self._s.params.append((key, value))
        return self

    def with_pdf_password(self, password: str) -> Self:
        """Opens a password-protected PDF (``pdp=``; needs both keys)."""
        self._s.pdf_password = password
        return self


class QueryVideoURLChain(WithExtension, VideoMethods):
    """A fluent ``/q/video`` URL: one method per video option name and alias."""


class QueryURLBuilder:
    """Builds query dialect URLs (the server needs XI_DIALECTS_QUERY_ENABLED=true).

    With ``signing``, every URL ends in an ``xi_sig`` over the path after the mount and the query::

        q = QueryURLBuilder(base_url="https://img.example.com", signing=SigningKey(key, salt))
        q.url("s3://bucket/cat.jpg", params={"w": 300, "f": "webp"}, options={"bl": 2})
        # https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
    """

    def __init__(
        self,
        *,
        base_url: str = "",
        mount: str = QUERY_MOUNT,
        signing: SigningKey | None = None,
        encryption: EncryptionKey | None = None,
        separator: str = DEFAULT_SEPARATOR,
        option_names: OptionNameStyle = "short",
    ) -> None:
        self.config = QueryConfig(
            base_url=base_url,
            mount=mount,
            signing=signing,
            encryption=encryption,
            separator=separator,
            option_names=option_names,
        )

    def url(
        self,
        source: str,
        *,
        params: QueryImageParams | Sequence[tuple[str, QueryValue | None]] | None = None,
        options: ProcessingOptions | Sequence[ProcessingOptions] | None = None,
        extension: str | None = None,
        pdf_password: str | None = None,
    ) -> str:
        """Builds a native ``/q/?url=…`` URL: native keys, then imgproxy options as ``o=``."""
        return build_query_image(self.config, pipelines_of(options), source, params, extension, pdf_password)

    def video_url(
        self,
        source: str,
        options: VideoOptions | Sequence[VideoOptions] | None = None,
        *,
        extension: VideoContainer | str | None = None,
    ) -> str:
        """Builds a ``/q/video`` URL; ``crop`` and ``flip`` are written ``c`` and ``fl``.

        A sequence of option mappings is written in order, so a key may repeat
        (later wins argument by argument).
        """
        return build_query_video(self.config, [e for p in pipelines_of(options) for e in p], source, extension)

    def stream_url(
        self,
        source: str,
        *,
        protocol: StreamProtocol,
        renditions: Sequence[int] | None = None,
        segment_duration: int | None = None,
        segment_format: SegmentFormat | None = None,
        options: VideoOptions | None = None,
    ) -> str:
        """Builds a ``/q/video`` HLS or DASH manifest URL (``format=m3u8|mpd``)."""
        entries, ext = stream_parts(pipelines_of(options), protocol, renditions, segment_duration, segment_format)
        return build_query_video(self.config, entries, source, ext)

    def imaginary_url(
        self,
        endpoint: ImaginaryEndpoint,
        source: str,
        params: ImaginaryParams | Sequence[tuple[str, QueryValue | None]] | None = None,
    ) -> str:
        """Builds an imaginary endpoint URL (``/q/resize?url=…&width=300``)."""
        return build_imaginary(self.config, endpoint, source, params)

    def _image_run(self) -> Callable[[ChainState], str]:
        def run(s: ChainState) -> str:
            return build_query_image(self.config, s.pipelines, s.source, s.params, s.ext, s.pdf_password)

        return run

    def _video_run(self) -> Callable[[ChainState], str]:
        def run(s: ChainState) -> str:
            return build_query_video(self.config, _single_pipeline(s.pipelines), s.source, s.ext)

        return run

    def image(self, source: str) -> QueryURLChain:
        """Starts a fluent ``/q/?url=…`` URL for a source."""
        return QueryURLChain(self._image_run(), source)

    def video(self, source: str) -> QueryVideoURLChain:
        """Starts a fluent ``/q/video`` URL for a source."""
        return QueryVideoURLChain(self._video_run(), source)


query_urls = QueryURLBuilder
"""Alias of :class:`QueryURLBuilder`, named like the TypeScript client's factory."""

QUERY_CONTENT_TYPE: Final = "application/x-www-form-urlencoded"
"""The Content-Type of an HTTP QUERY request's content (the query dialect URL's query)."""


@dataclass(frozen=True, kw_only=True)
class QueryMethodRequest:
    """A query dialect request sent with the HTTP QUERY method (RFC 10008, URL-DIALECTS §4.4).

    Attributes:
        method: Always ``"QUERY"``.
        url: The URL without its query.
        content_type: :data:`QUERY_CONTENT_TYPE`.
        body: The URL's query exactly as built, ``xi_sig`` included: the server
            signs, limits and caches it as the GET URL, since ``xi_sig`` does
            not cover the method.
    """

    method: Literal["QUERY"] = "QUERY"
    url: str
    content_type: str = QUERY_CONTENT_TYPE
    body: str


def query_method_request(url: str) -> QueryMethodRequest:
    """Splits a built query dialect URL (``/q/…?url=…``) into an HTTP QUERY request.

    The parameters move from the URL to the content, so long option chains,
    watermark text or encrypted sources stay out of URLs. The server must
    have XI_SERVER_QUERY_METHOD on (the default); many CDNs and proxies
    neither cache nor forward QUERY.

    Raises:
        URLBuildError: ``invalid_request`` when the URL has no query, an empty
            query, or a fragment.
    """
    path, sep, query = url.partition("?")
    if not sep or not query or "#" in url:
        raise URLBuildError("invalid_request", "a QUERY request needs a query dialect URL with a query and no fragment")
    return QueryMethodRequest(url=path, body=query)

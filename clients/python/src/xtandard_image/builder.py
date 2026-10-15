"""imgproxy-dialect URL builder (docs/URL-DIALECTS.md §3.1).

::

    {base_url}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
    {base_url}/video/{signature}/{video option}/…/{source}   (signs "/video" + path)

Sources are base64url (default), ``plain/`` (percent-escaped, ``@ext``) or
``enc/`` (AES-CBC); base64 and enc sources take ``.ext``.
"""

from __future__ import annotations

import sys
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Literal

from ._generated.constants import (
    DEFAULT_SEGMENT_FORMAT,
    DEFAULT_SEPARATOR,
    DEFAULT_UNSIGNED,
    FORBIDDEN_SEPARATORS,
    PDF_PASSWORD_NAME,
    PDF_PASSWORD_SHORT,
    SEGMENT_FORMAT_PROTOCOLS,
    STREAM_MANIFESTS,
    QueryValue,
    SegmentFormat,
    SourceEncoding,
    StreamProtocol,
    VideoContainer,
)
from ._generated.methods import Arg, InfoMethods, ProcessingMethods, VideoMethods
from ._text import b64url, check_extension, check_text, escape_plain_source, utf8
from .encryption import EncryptionKey, encrypt_pdf_password, encrypt_source
from .errors import URLBuildError
from .options import INFO, PROCESSING, VIDEO, OptionEntry, OptionNameStyle, entries_of, format_entry, pipelines_of
from .signing import SigningKey, sign, sign_imgproxy_path

if sys.version_info >= (3, 11):
    from typing import Self
else:
    from typing_extensions import Self

if TYPE_CHECKING:
    from collections.abc import Callable, Mapping, Sequence

    from ._generated.options import InfoOptions, ProcessingOptions, VideoOptions

__all__ = [
    "ImgproxyInfoURLChain",
    "ImgproxyURLBuilder",
    "ImgproxyURLChain",
    "ImgproxyVideoURLChain",
    "imgproxy",
]

Endpoint = Literal["process", "info", "video"]


@dataclass(frozen=True)
class ImgproxyConfig:
    """Settings shared by every URL an :class:`ImgproxyURLBuilder` makes.

    Attributes:
        base_url: Prepended to every path: the server origin and the dialect's
            mount prefix, if any. Default ``""`` (root-relative paths).
        signing: Signs URLs with the imgproxy scheme; without it the signature
            segment is ``unsigned_signature``.
        unsigned_signature: Signature segment of unsigned URLs: ``insecure``, ``unsafe`` or ``_``.
        encryption: Key for ``encrypted`` sources and PDF passwords.
        source_encoding: Default source encoding (``base64``, ``plain`` or ``encrypted``).
        split_source: Splits base64 and encrypted sources into ``/``-separated chunks of this many characters.
        separator: Argument separator (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR).
        option_names: ``short`` (default), ``canonical`` or ``given`` (as written).
    """

    base_url: str = ""
    signing: SigningKey | None = field(default=None, repr=False)
    unsigned_signature: str = DEFAULT_UNSIGNED
    encryption: EncryptionKey | None = field(default=None, repr=False)
    source_encoding: SourceEncoding = "base64"
    split_source: int | None = None
    separator: str = DEFAULT_SEPARATOR
    option_names: OptionNameStyle = "short"


def check_separator(sep: str, forbidden: Sequence[str]) -> str:
    """Rejects a separator that is not one character or that the grammar uses."""
    if len(sep) != 1 or sep in forbidden:
        raise URLBuildError("invalid_separator", f"invalid argument separator {sep!r}")
    return sep


def _split(token: str, n: int | None) -> str:
    if not n or n <= 0 or len(token) <= n:
        return token
    return "/".join(token[i : i + n] for i in range(0, len(token), n))


def _source_segment(cfg: ImgproxyConfig, source: str, encoding: str, ext: str | None) -> str:
    check_text(source, "invalid_source", "source")
    e = None if ext is None or ext == "" else check_extension(ext)
    if encoding == "plain":
        return f"plain/{escape_plain_source(source)}" + (f"@{e}" if e else "")
    if encoding == "base64":
        return _split(b64url(utf8(source)), cfg.split_source) + (f".{e}" if e else "")
    if encoding == "encrypted":
        if cfg.encryption is None:
            raise URLBuildError("missing_encryption", "encrypted sources need an encryption key")
        token = encrypt_source(source, cfg.encryption)
        return f"enc/{_split(token, cfg.split_source)}" + (f".{e}" if e else "")
    raise URLBuildError("invalid_request", f"unknown source encoding {encoding!r}")


def password_token(encryption: EncryptionKey | None, signing: SigningKey | None, password: str) -> str:
    """The pdf_password token, checking that both keys are present."""
    if encryption is None:
        raise URLBuildError("missing_encryption", "pdf_password needs an encryption key")
    if signing is None:
        raise URLBuildError("missing_signing", "pdf_password needs signing: the server refuses it on unsigned URLs")
    return encrypt_pdf_password(password, encryption)


def _finish(cfg: ImgproxyConfig, endpoint: Endpoint, path: str) -> str:
    base = cfg.base_url.rstrip("/")
    if endpoint == "video":
        sig = sign("/video" + path, cfg.signing) if cfg.signing else cfg.unsigned_signature
        return f"{base}/video/{sig}{path}"
    signed = sign_imgproxy_path(path, cfg.signing) if cfg.signing else f"/{cfg.unsigned_signature}{path}"
    return f"{base}{'/info' if endpoint == 'info' else ''}{signed}"


def build_imgproxy(
    cfg: ImgproxyConfig,
    endpoint: Endpoint,
    pipelines: Sequence[Sequence[OptionEntry]],
    source: str,
    encoding: SourceEncoding | None,
    ext: str | None,
    pdf_password: str | None,
) -> str:
    """Builds a URL from option pipelines and a source."""
    sep = check_separator(cfg.separator, FORBIDDEN_SEPARATORS)
    style = cfg.option_names
    cat = INFO if endpoint == "info" else VIDEO if endpoint == "video" else PROCESSING
    non_empty = [p for p in pipelines if p]
    if endpoint != "process" and len(non_empty) > 1:
        raise URLBuildError("invalid_request", f"/{endpoint} URLs have no chained pipelines")
    if endpoint == "video" and pdf_password is not None:
        raise URLBuildError("invalid_request", "/video URLs take no pdf_password")
    segments: list[str] = []
    if pdf_password is not None:
        token = password_token(cfg.encryption, cfg.signing, pdf_password)
        segments.append(f"{PDF_PASSWORD_NAME if style == 'canonical' else PDF_PASSWORD_SHORT}{sep}{token}")
    for i, p in enumerate(non_empty):
        if i > 0:
            segments.append("-")
        segments.extend(format_entry(cat, e, sep, style) for e in p)
    segments.append(_source_segment(cfg, source, encoding or cfg.source_encoding, ext))
    return _finish(cfg, endpoint, "/" + "/".join(segments))


def stream_parts(
    pipelines: Sequence[Sequence[OptionEntry]],
    protocol: str,
    renditions: Sequence[int] | None,
    segment_duration: int | None,
    segment_format: str | None,
) -> tuple[list[OptionEntry], str]:
    """The video options and manifest container of a stream request."""
    entries = [e for p in pipelines for e in p]
    for name, _ in entries:
        if VIDEO.names.get(name) in ("stream", "segment_duration", "segment_format", "format"):
            raise URLBuildError(
                "invalid_request", f"stream requests set {name} through protocol, renditions and the segment settings"
            )
    manifest = STREAM_MANIFESTS.get(protocol)
    if manifest is None:
        raise URLBuildError("invalid_request", f"unknown stream protocol {protocol!r}")
    if renditions:
        entries.append(("stream", (protocol, *renditions)))
    if segment_duration is not None:
        entries.append(("segment_duration", (segment_duration,)))
    if segment_format is not None:
        protocols = SEGMENT_FORMAT_PROTOCOLS.get(segment_format)
        if protocols is None or protocol not in protocols:
            raise URLBuildError("invalid_request", f"segment format {segment_format!r} is not available for {protocol}")
        if segment_format != DEFAULT_SEGMENT_FORMAT:
            entries.append(("segment_format", (segment_format,)))
    return entries, manifest[0]


# ---- fluent form ------------------------------------------------------------


def _no_params() -> list[tuple[str, QueryValue | None]]:
    return []


@dataclass
class ChainState:
    """What a fluent chain collected."""

    source: str
    pipelines: list[list[OptionEntry]] = field(default_factory=lambda: [[]])
    params: list[tuple[str, QueryValue | None]] = field(default_factory=_no_params)
    encoding: SourceEncoding | None = None
    ext: str | None = None
    pdf_password: str | None = None


class ChainCore:
    """Shared state and control methods of the fluent builders."""

    def __init__(self, run: Callable[[ChainState], str], source: str) -> None:
        self._run = run
        self._s = ChainState(source)

    def option(self, name: str, *args: Arg) -> Self:
        """Appends one option occurrence (as the per-option methods do)."""
        self._s.pipelines[-1].append((name, args))
        return self

    def options(self, options: Mapping[str, object]) -> Self:
        """Appends every option of an options mapping, in key order."""
        self._s.pipelines[-1].extend(entries_of(options))
        return self

    def build(self) -> str:
        """Builds the URL."""
        return self._run(self._s)

    def __str__(self) -> str:
        return self.build()


class WithEncoding(ChainCore):
    """``encoding()``: the source encoding of one URL."""

    def encoding(self, encoding: SourceEncoding) -> Self:
        """Overrides the source encoding."""
        self._s.encoding = encoding
        return self


class WithExtension(ChainCore):
    """``extension()``: the output format or container."""

    def extension(self, ext: str) -> Self:
        """Sets the output extension (``@webp`` / ``.webp``, a container for /video)."""
        self._s.ext = ext
        return self


class WithPipeline(ChainCore):
    """``pipeline()``: chained pipelines."""

    def pipeline(self) -> Self:
        """Starts the next chained pipeline (``/-/``)."""
        self._s.pipelines.append([])
        return self


class WithPassword(ChainCore):
    """``with_pdf_password()``: a password-protected PDF source."""

    def with_pdf_password(self, password: str) -> Self:
        """Opens a password-protected PDF: the plain-text password is encrypted into ``pdp`` (needs both keys)."""
        self._s.pdf_password = password
        return self


class ImgproxyURLChain(WithEncoding, WithExtension, WithPipeline, WithPassword, ProcessingMethods):
    """A fluent processing URL: one method per option name and alias, ``pipeline()`` for ``/-/``."""


class ImgproxyInfoURLChain(WithEncoding, WithPassword, InfoMethods):
    """A fluent ``/info`` URL."""


class ImgproxyVideoURLChain(WithEncoding, WithExtension, VideoMethods):
    """A fluent ``/video`` URL: video transformations, audio and waveforms."""


class ImgproxyURLBuilder:
    """Builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server.

    ::

        img = ImgproxyURLBuilder(base_url="https://img.example.com", signing=SigningKey(key, salt))
        img.url("s3://bucket/cat.jpg", {"rs": ("fill", 300, 400), "q": 80}, extension="webp")
        img.image("s3://bucket/cat.jpg").resize("fill", 300, 400).quality(80).extension("webp").build()
        img.video_url("s3://bucket/talk.mov", {"rs": ("fill", 640, 360), "vs": 12, "vd": 8}, extension="webm")
        img.stream_url("s3://bucket/talk.mov", protocol="hls", renditions=[360, 720])
    """

    def __init__(
        self,
        *,
        base_url: str = "",
        signing: SigningKey | None = None,
        unsigned_signature: str = DEFAULT_UNSIGNED,
        encryption: EncryptionKey | None = None,
        source_encoding: SourceEncoding = "base64",
        split_source: int | None = None,
        separator: str = DEFAULT_SEPARATOR,
        option_names: OptionNameStyle = "short",
    ) -> None:
        self.config = ImgproxyConfig(
            base_url=base_url,
            signing=signing,
            unsigned_signature=unsigned_signature,
            encryption=encryption,
            source_encoding=source_encoding,
            split_source=split_source,
            separator=separator,
            option_names=option_names,
        )

    def url(
        self,
        source: str,
        options: ProcessingOptions | Sequence[ProcessingOptions] | None = None,
        *,
        extension: str | None = None,
        source_encoding: SourceEncoding | None = None,
        pdf_password: str | None = None,
    ) -> str:
        """Builds a processing URL; a sequence of option mappings chains pipelines.

        Args:
            source: The source URL (``https://``, ``s3://``, ``xi://``, ``local:///``, …).
            options: Processing options keyed by name or alias, in URL order.
            extension: Output format extension (``@webp`` / ``.webp``).
            source_encoding: Overrides the configured source encoding.
            pdf_password: Plain-text password of a protected PDF (needs ``encryption`` and ``signing``).
        """
        return build_imgproxy(
            self.config, "process", pipelines_of(options), source, source_encoding, extension, pdf_password
        )

    def info_url(
        self,
        source: str,
        options: InfoOptions | None = None,
        *,
        source_encoding: SourceEncoding | None = None,
        pdf_password: str | None = None,
    ) -> str:
        """Builds an ``/info`` URL."""
        return build_imgproxy(self.config, "info", pipelines_of(options), source, source_encoding, None, pdf_password)

    def video_url(
        self,
        source: str,
        options: VideoOptions | None = None,
        *,
        extension: VideoContainer | str | None = None,
        source_encoding: SourceEncoding | None = None,
    ) -> str:
        """Builds a ``/video`` URL (video transformation, audio or waveform; ``extension`` is the container)."""
        return build_imgproxy(self.config, "video", pipelines_of(options), source, source_encoding, extension, None)

    def stream_url(
        self,
        source: str,
        *,
        protocol: StreamProtocol,
        renditions: Sequence[int] | None = None,
        segment_duration: int | None = None,
        segment_format: SegmentFormat | None = None,
        options: VideoOptions | None = None,
        source_encoding: SourceEncoding | None = None,
    ) -> str:
        """Builds an HLS or DASH manifest URL (``/video/…@m3u8`` or ``@mpd``).

        Args:
            source: The video source.
            protocol: ``hls`` or ``dash``.
            renditions: Rungs by the short side of the frame (at most 8); default: the server's ladder.
            segment_duration: Segment length in whole seconds (1 to 30).
            segment_format: ``fmp4`` (CMAF, default) or ``ts`` (MPEG-TS, HLS only).
            options: Other ``/video`` options (trim, crop, quality, audio, …) applied to every rendition.
            source_encoding: Overrides the configured source encoding.
        """
        entries, ext = stream_parts(pipelines_of(options), protocol, renditions, segment_duration, segment_format)
        return build_imgproxy(self.config, "video", [entries], source, source_encoding, ext, None)

    def _run(self, endpoint: Endpoint) -> Callable[[ChainState], str]:
        def run(s: ChainState) -> str:
            return build_imgproxy(self.config, endpoint, s.pipelines, s.source, s.encoding, s.ext, s.pdf_password)

        return run

    def image(self, source: str) -> ImgproxyURLChain:
        """Starts a fluent processing URL for a source."""
        return ImgproxyURLChain(self._run("process"), source)

    def info(self, source: str) -> ImgproxyInfoURLChain:
        """Starts a fluent ``/info`` URL for a source."""
        return ImgproxyInfoURLChain(self._run("info"), source)

    def video(self, source: str) -> ImgproxyVideoURLChain:
        """Starts a fluent ``/video`` URL for a source."""
        return ImgproxyVideoURLChain(self._run("video"), source)


imgproxy = ImgproxyURLBuilder
"""Alias of :class:`ImgproxyURLBuilder`, named like the TypeScript client's factory."""

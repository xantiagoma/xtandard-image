"""Official Python client of xtandard-image.

Typed URL builders for the imgproxy dialect (processing, ``/info``,
``/video``, HLS/DASH streams) and the query dialect (``/q/``, ``/q/video``,
imaginary endpoints), imgproxy and xi_sig signing, ``enc/`` source and PDF
password encryption, and HTTP clients on httpx (:class:`Client`,
:class:`AsyncClient`). Build and sign URLs on a server: the keys sign and
decrypt everything.

::

    from xtandard_image import SigningKey, imgproxy

    img = imgproxy(base_url="https://img.example.com", signing=SigningKey(key=KEY, salt=SALT))
    img.url("s3://bucket/cat.jpg", {"rs": ("fill", 300, 400), "q": 80}, extension="webp")
"""

from ._generated.constants import (
    IMAGE_FORMATS,
    MAX_PDF_PASSWORD_BYTES,
    VIDEO_CONTAINERS,
    XI_SIG_PARAM,
    ErrorKind,
    ImageFormat,
    ImaginaryEndpoint,
    ImaginaryParams,
    QueryImageParams,
    QueryValue,
    SegmentFormat,
    SourceEncoding,
    SourceScheme,
    StreamProtocol,
    VideoContainer,
)
from ._generated.methods import Arg
from ._generated.options import (
    InfoOptionName,
    InfoOptions,
    ProcessingOptionName,
    ProcessingOptions,
    VideoOptionName,
    VideoOptions,
)
from ._generated.responses import ErrorBody, ErrorResponse, InfoResponse
from ._text import KeyMaterial, escape_query_value
from .builder import (
    ImgproxyInfoURLChain,
    ImgproxyURLBuilder,
    ImgproxyURLChain,
    ImgproxyVideoURLChain,
    imgproxy,
)
from .client import AsyncClient, ByteRange, ByteSuffix, Client, Readiness, StreamManifest, range_header
from .encryption import EncryptionKey, encrypt_pdf_password, encrypt_source
from .errors import (
    BadRequestError,
    ErrorCode,
    ForbiddenError,
    GatewayError,
    HTTPError,
    NotAcceptableError,
    NotFoundError,
    ServerError,
    ServiceUnavailableError,
    TooManyRequestsError,
    TransportError,
    UnprocessableError,
    URLBuildError,
    XtandardError,
)
from .options import OptionNameStyle
from .query import (
    QUERY_CONTENT_TYPE,
    QueryMethodRequest,
    QueryURLBuilder,
    QueryURLChain,
    QueryVideoURLChain,
    query_method_request,
    query_urls,
)
from .signing import SigningKey, sign, sign_imgproxy_path, sign_xi_query, sign_xi_url, xi_message
from .sources import escape_plain_source, source_url, xi_source

__version__ = "0.0.0"

__all__ = [
    "IMAGE_FORMATS",
    "MAX_PDF_PASSWORD_BYTES",
    "QUERY_CONTENT_TYPE",
    "VIDEO_CONTAINERS",
    "XI_SIG_PARAM",
    "Arg",
    "AsyncClient",
    "BadRequestError",
    "ByteRange",
    "ByteSuffix",
    "Client",
    "EncryptionKey",
    "ErrorBody",
    "ErrorCode",
    "ErrorKind",
    "ErrorResponse",
    "ForbiddenError",
    "GatewayError",
    "HTTPError",
    "ImageFormat",
    "ImaginaryEndpoint",
    "ImaginaryParams",
    "ImgproxyInfoURLChain",
    "ImgproxyURLBuilder",
    "ImgproxyURLChain",
    "ImgproxyVideoURLChain",
    "InfoOptionName",
    "InfoOptions",
    "InfoResponse",
    "KeyMaterial",
    "NotAcceptableError",
    "NotFoundError",
    "OptionNameStyle",
    "ProcessingOptionName",
    "ProcessingOptions",
    "QueryImageParams",
    "QueryMethodRequest",
    "QueryURLBuilder",
    "QueryURLChain",
    "QueryValue",
    "QueryVideoURLChain",
    "Readiness",
    "SegmentFormat",
    "ServerError",
    "ServiceUnavailableError",
    "SigningKey",
    "SourceEncoding",
    "SourceScheme",
    "StreamManifest",
    "StreamProtocol",
    "TooManyRequestsError",
    "TransportError",
    "URLBuildError",
    "UnprocessableError",
    "VideoContainer",
    "VideoOptionName",
    "VideoOptions",
    "XtandardError",
    "__version__",
    "encrypt_pdf_password",
    "encrypt_source",
    "escape_plain_source",
    "escape_query_value",
    "imgproxy",
    "query_method_request",
    "query_urls",
    "range_header",
    "sign",
    "sign_imgproxy_path",
    "sign_xi_query",
    "sign_xi_url",
    "source_url",
    "xi_message",
    "xi_source",
]

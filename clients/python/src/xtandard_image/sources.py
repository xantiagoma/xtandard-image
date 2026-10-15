"""Source references (docs/URL-DIALECTS.md §3.1): the string a URL carries as its source.

These helpers only join strings; the URL builders escape or encode the result::

    https://host/path         an http source
    s3://bucket/key           gs://bucket/key   abs://container/key
    local:///path             ftp://host/path   sftp://host/path
    xi://<source>/<key>       a named source (XI_SOURCES)
"""

from __future__ import annotations

import re

from ._generated.constants import XI_NAME_PATTERN, SourceScheme
from ._text import escape_plain_source
from .errors import URLBuildError

__all__ = ["SourceScheme", "escape_plain_source", "source_url", "xi_source"]

_NAME = re.compile(XI_NAME_PATTERN)
_BAD_AUTHORITY = re.compile(r"[/?#\s]")


def xi_source(name: str, key: str) -> str:
    """A named source reference, ``xi://<name>/<key>``.

    The key may be relative or, for an http source, an absolute URL::

        xi_source("photos", "2026/cat.jpg")                # xi://photos/2026/cat.jpg
        xi_source("web", "https://cdn.example.com/a.jpg")  # xi://web/https://cdn.example.com/a.jpg

    Raises:
        URLBuildError: The name is not a source name (``invalid_name``).
    """
    if not _NAME.fullmatch(name):
        raise URLBuildError("invalid_name", f"invalid source name {name!r}")
    return f"xi://{name}/{key.lstrip('/')}"


def source_url(scheme: SourceScheme, authority: str, key: str) -> str:
    """A source reference for a scheme: the bucket, container, host or source name, then the key.

    ``local`` takes only a path (``local:///<path>``)::

        source_url("s3", "photos", "cat.jpg")   # s3://photos/cat.jpg
        source_url("local", "", "img/cat.jpg")  # local:///img/cat.jpg

    Raises:
        URLBuildError: The authority is missing, has ``/``, ``?``, ``#`` or
            whitespace, or is given for ``local`` (``invalid_name``).
    """
    if scheme == "xi":
        return xi_source(authority, key)
    if scheme == "local":
        if authority != "":
            raise URLBuildError("invalid_name", "local sources have no authority: local:///<path>")
        return f"local:///{key.lstrip('/')}"
    if authority == "" or _BAD_AUTHORITY.search(authority):
        raise URLBuildError("invalid_name", f"invalid {scheme} authority {authority!r}")
    return f"{scheme}://{authority}/{key.lstrip('/')}"

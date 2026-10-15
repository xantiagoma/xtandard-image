from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

# clients/python/tests -> repository root
ROOT = Path(__file__).resolve().parents[3]


def load_json(rel: str) -> Any:
    return json.loads((ROOT / rel).read_text(encoding="utf-8"))


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"

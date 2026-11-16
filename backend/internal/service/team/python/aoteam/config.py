"""Portable paths and an explicit project identity."""
from __future__ import annotations

import json
import os
import re
import tempfile
from pathlib import Path


class ContractError(ValueError):
    pass


def identifier(value):
    if not isinstance(value, str) or not re.fullmatch(r"[a-z][a-z0-9_-]{0,63}", value):
        raise ContractError("IDs need 1..64 lowercase letters, digits, underscores or hyphens")
    return value


def text(value, name="text", limit=50000):
    if not isinstance(value, str) or not value.strip() or len(value) > limit:
        raise ContractError(f"{name} must contain 1..{limit} characters")
    return value


def atomic_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(dir=path.parent, prefix=".orchestrator-", suffix=".tmp")
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as stream:
            json.dump(value, stream, indent=2, ensure_ascii=False)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)

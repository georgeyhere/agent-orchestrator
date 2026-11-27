"""Portable paths and an explicit project identity."""
from __future__ import annotations

import json
import os
import re
import tempfile
from pathlib import Path


class ContractError(ValueError):
    pass


def configured_memory_root():
    """Honor the inherited environment and fresh Windows user/system settings."""
    value = os.environ.get("LLMWIKI_MEMORY_ROOT", "").strip()
    if value:
        return value
    if os.name == "nt":
        import winreg
        for hive, key in ((winreg.HKEY_CURRENT_USER, "Environment"),
                          (winreg.HKEY_LOCAL_MACHINE, r"SYSTEM\CurrentControlSet\Control\Session Manager\Environment")):
            try:
                with winreg.OpenKey(hive, key) as handle:
                    value, _ = winreg.QueryValueEx(handle, "LLMWIKI_MEMORY_ROOT")
                if isinstance(value, str) and value.strip():
                    return winreg.ExpandEnvironmentStrings(value).strip()
            except OSError:
                continue
    return None


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

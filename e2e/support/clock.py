"""Clock helpers for E2E testing."""
import time
from datetime import datetime, timezone


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def now_micro() -> int:
    return int(time.time() * 1_000_000)

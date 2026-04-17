from __future__ import annotations
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional
from collections import deque
import datetime

MAX_BREADCRUMBS = 100


@dataclass
class Breadcrumb:
    category: str
    message: str
    timestamp: str = field(default_factory=lambda: datetime.datetime.now(datetime.timezone.utc).isoformat())
    level: Optional[str] = None
    data: Optional[Dict[str, Any]] = None

    def to_dict(self) -> dict:
        d: dict = {"timestamp": self.timestamp, "category": self.category, "message": self.message}
        if self.level:
            d["level"] = self.level
        if self.data:
            d["data"] = self.data
        return d


_buffer: deque[Breadcrumb] = deque(maxlen=MAX_BREADCRUMBS)


def add_breadcrumb(category: str, message: str, level: Optional[str] = None, data: Optional[Dict[str, Any]] = None) -> None:
    _buffer.append(Breadcrumb(category=category, message=message, level=level, data=data))


def get_breadcrumbs() -> List[Breadcrumb]:
    return list(_buffer)


def clear_breadcrumbs() -> None:
    _buffer.clear()

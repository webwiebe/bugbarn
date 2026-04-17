from __future__ import annotations
from dataclasses import dataclass
from typing import Optional


@dataclass
class UserContext:
    id: Optional[str] = None
    email: Optional[str] = None
    username: Optional[str] = None

    def to_dict(self) -> dict:
        return {k: v for k, v in {"id": self.id, "email": self.email, "username": self.username}.items() if v}


_current_user: Optional[UserContext] = None


def set_user(id: Optional[str] = None, email: Optional[str] = None, username: Optional[str] = None) -> None:
    global _current_user
    _current_user = UserContext(id=id, email=email, username=username)


def clear_user() -> None:
    global _current_user
    _current_user = None


def get_user() -> Optional[UserContext]:
    return _current_user

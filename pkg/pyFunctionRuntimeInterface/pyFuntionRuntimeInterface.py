from dataclasses import dataclass
from typing import Optional


@dataclass
class Request:
    data: str
    id: str


@dataclass
class Response:
    data: str
    id: str


@dataclass
class Function:
    timeout: int
    address: str
    id: str
    request: Optional[Request] = None
    response: Optional[Response] = None



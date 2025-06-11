from pydantic import BaseModel
from typing import Any, Dict, Optional


class SekaiDataRetrieverResponse(BaseModel):
    raw_body: bytes
    decrypted_body: Dict[str, Any]
    status_code: int
    new_headers: Optional[Dict[str, str]] = None


class SekaiInheritDataRetrieverResponse(BaseModel):
    server: str
    user_id: int
    suite: Optional[bytes] = None
    mysekai: Optional[bytes] = None
    policy: Optional[str] = None

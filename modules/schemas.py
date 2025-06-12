from datetime import datetime
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


class APIResponse(BaseModel):
    status: Optional[int] = 200
    message: Optional[str] = "success"


class InheritInformation(BaseModel):
    inherit_id: str
    inherit_password: str


class DataChunk(BaseModel):
    request_url: str
    upload_id: str
    chunk_index: int
    total_chunks: int
    time: datetime
    data: bytes

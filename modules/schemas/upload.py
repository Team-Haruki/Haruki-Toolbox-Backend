from datetime import datetime
from pydantic import BaseModel


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

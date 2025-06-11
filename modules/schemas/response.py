from pydantic import BaseModel


class APIResponse(BaseModel):
    message: str

import orjson
import hashlib
from fastapi import Request
from typing import Any, Optional
from fastapi_cache import Coder, FastAPICache
from fastapi.encoders import jsonable_encoder


class ORJsonCoder(Coder):
    @classmethod
    def encode(cls, value: Any) -> bytes:
        return orjson.dumps(
            value,
            default=jsonable_encoder,
            option=orjson.OPT_NON_STR_KEYS | orjson.OPT_SERIALIZE_NUMPY,
        )

    @classmethod
    def decode(cls, value: bytes) -> Any:
        return orjson.loads(value)


def cache_key_builder(func, namespace: Optional[str] = "", request: Request = None, **kwargs) -> str:
    full_path = request.url.path
    query_string = str(request.url.query)
    query_hash = hashlib.md5(query_string.encode()).hexdigest() if query_string else "none"
    return f"{namespace}:{full_path}:query={query_hash}"


async def clear_cache_by_path(namespace: str, path: str, query_string: str = "") -> None:
    query_hash = hashlib.md5(query_string.encode()).hexdigest() if query_string else "none"
    key = f"fastapi-cache:{namespace}:{path}:query={query_hash}"
    await FastAPICache.clear(key=key)
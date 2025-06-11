from fastapi import FastAPI
from redis.asyncio import Redis
from fastapi_cache import FastAPICache
from contextlib import asynccontextmanager
from fastapi.responses import ORJSONResponse
from fastapi_cache.backends.redis import RedisBackend

from api.public import public_api
from api.private import private_api
from api.upload import ios_upload_api, general_upload_api, logger as upload_api_logger
from utils import mongo_client
from configs import REDIS_HOST, REDIS_PORT, REDIS_PASSWORD


@asynccontextmanager
async def lifespan(_app: FastAPI):
    await upload_api_logger.start()
    await mongo_client.aconnect()
    redis_client = Redis(
        host=REDIS_HOST, port=REDIS_PORT, password=REDIS_PASSWORD, decode_responses=False, encoding="utf-8"
    )
    FastAPICache.init(RedisBackend(redis_client), prefix="fastapi-cache")
    yield
    await upload_api_logger.stop()
    await mongo_client.close()


app = FastAPI(
    lifespan=lifespan,
    default_response_class=ORJSONResponse,
    docs_url=None,
    redoc_url=None,
    openapi_url=None,
)
app.include_router(public_api)
app.include_router(private_api)
app.include_router(ios_upload_api)
app.include_router(general_upload_api)

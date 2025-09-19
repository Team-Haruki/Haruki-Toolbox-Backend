from pathlib import Path
from redis.asyncio import Redis
from fastapi import FastAPI, Request
from fastapi_cache import FastAPICache
from contextlib import asynccontextmanager
from fastapi.responses import ORJSONResponse
from fastapi.middleware.cors import CORSMiddleware
from fastapi_cache.backends.redis import RedisBackend


from api.webhook import webhook
from api.public import public_api
from api.private import private_api
from api.upload import ios_upload_api, general_upload_api, logger as upload_api_logger
from modules.schemas import APIResponse
from modules.api.exception import APIException
from utils import mongo
from configs import REDIS_HOST, REDIS_PORT, REDIS_PASSWORD


@asynccontextmanager
async def lifespan(_app: FastAPI):
    await upload_api_logger.start()
    await mongo.client.aconnect()
    redis_client = Redis(
        host=REDIS_HOST, port=REDIS_PORT, password=REDIS_PASSWORD, decode_responses=False, encoding="utf-8"
    )
    FastAPICache.init(RedisBackend(redis_client), prefix="fastapi-cache")
    yield
    await upload_api_logger.stop()
    await mongo.client.close()


app = FastAPI(
    lifespan=lifespan,
    default_response_class=ORJSONResponse,
    docs_url=None,
    redoc_url=None,
    openapi_url=None,
)
app.include_router(webhook)
app.include_router(public_api)
app.include_router(private_api)
app.include_router(ios_upload_api)
app.include_router(general_upload_api)
allowed_origins = [
    "https://haruki.seiunx.com",
    "https://3-3.dev",
    "http://localhost:3000",
    "http://localhost:5173",
    "http://localhost:8080",
]
app.add_middleware(
    CORSMiddleware,
    allow_origins=allowed_origins,
    allow_credentials=True,
    allow_methods=["GET", "POST", "OPTIONS", "PUT", "DELETE"],
    allow_headers=["Origin", "Content-Type", "Accept", "Authorization"],
)
if (Path(__file__).parent / "friends/app.py").exists():
    from friends.app import app as friends_app
    app.mount("/misc", friends_app)
@app.exception_handler(APIException)
async def api_exception_handler(request: Request, exc: APIException) -> ORJSONResponse:
    return ORJSONResponse(
        status_code=exc.status,
        content=APIResponse(
            status=exc.status,
            message=exc.message,
        ).model_dump(),
    )

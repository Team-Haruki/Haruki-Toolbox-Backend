from fastapi import FastAPI
from contextlib import asynccontextmanager
from fastapi.responses import ORJSONResponse

@asynccontextmanager
async def lifespan(_app: FastAPI):
    yield


app = FastAPI(lifespan=lifespan, default_response_class=ORJSONResponse, docs_url=None, redoc_url=None, openapi_url=None)
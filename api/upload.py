from typing import Dict, List
from datetime import timedelta, datetime
from fastapi import APIRouter, Depends, Response, Request, HTTPException

from modules.logger import AsyncLogger
from modules.api.exception import APIException
from modules.cache_helpers import clear_cache_by_path
from modules.api.handle_data import handle_and_update_data
from modules.sekai_client.retriever import ProjectSekaiDataRetriever
from modules.schemas import InheritInformation, DataChunk, APIResponse
from modules.sekai_client.proxy_call import handle_proxy_upload, api_endpoint
from modules.api.depends import reject_en_mysekai_inherit, require_upload_type, parse_json_body
from modules.api.helpers import extract_upload_type_and_user_id, handle_upload, get_clear_cache_paths
from modules.enums import (
    SupportedInheritUploadServer,
    SupportedMysekaiUploadServer,
    SupportedSuiteUploadServer,
    UploadDataType,
    UploadPolicy,
)
from utils import mongo
from configs import PROXY

logger = AsyncLogger(__name__, level="DEBUG")
ios_upload_api = APIRouter(prefix="/ios")
general_upload_api = APIRouter(prefix="/general/{server}/{upload_type}/{policy}")
CHUNK_EXPIRE = timedelta(minutes=3)
data_chunks: Dict[str, List[DataChunk]] = {}


@ios_upload_api.post(
    "/script/upload",
    response_model=APIResponse,
    status_code=200,
    summary="JavaScript法上传用户数据",
    description="通过JavaScript模块分块上传用户数据",
)
async def script_upload_data(request: Request) -> APIResponse:
    """
    Original Author: NeuraXmy
    """
    script_version = request.headers.get("X-Script-Version", "unknown")
    original_url = request.headers["X-Original-Url"]
    upload_id = request.headers["X-Upload-Id"]
    chunk_index = int(request.headers["X-Chunk-Index"])
    total_chunks = int(request.headers["X-Total-Chunks"])
    policy = UploadPolicy(request.headers["X-Upload-Policy"])

    extracted = extract_upload_type_and_user_id(original_url)
    if not extracted:
        await logger.error(
            f"无法识别抓包数据类型: {original_url}, upload: {upload_id},"
            f" chunk: {chunk_index + 1}/{total_chunks} script_version:{script_version}"
        )
        raise HTTPException(status_code=400, detail="无法识别上传类型")
    upload_type, user_id = extracted

    server = None
    for server, _tuple in api_endpoint:
        if _tuple[1] in original_url:
            server = server
            break
    if not server:
        await logger.error(
            f"无法识别抓包数据游戏服务器: {original_url}, upload: {upload_id},"
            f" chunk: {chunk_index + 1}/{total_chunks} script_version:{script_version}"
        )
        raise HTTPException(status_code=400, detail="无法识别游戏服务器")

    now = datetime.now()
    body = await request.body()
    data_chunks.setdefault(upload_id, []).append(
        DataChunk(
            request_url=original_url,
            upload_id=upload_id,
            chunk_index=chunk_index,
            total_chunks=total_chunks,
            time=now,
            data=body,
        )
    )

    await logger.info(
        f"收到 {user_id} 的 {server}_{upload_type} 分块抓包数据块上传"
        f" ({chunk_index + 1}/{total_chunks} of {upload_id}, url={original_url},"
        f" script_version={script_version})"
    )

    if len(data_chunks[upload_id]) == total_chunks:
        chunks = sorted(data_chunks[upload_id], key=lambda c: c.chunk_index)
        payload = b"".join(c.data for c in chunks)
        result = await handle_and_update_data(
            payload,
            server,
            policy,
            mongo,
            upload_type,
            user_id=user_id,
        )
        if result.status != 200:
            raise APIException(result.status, result.error_message or "Unknown Error")
        data_chunks.pop(upload_id)
        await logger.info(
            f"收到 {user_id} 的 {server}_{upload_type} 分块抓包数据上传 ({upload_id}, script_version={script_version})"
        )
        for path in get_clear_cache_paths(server, upload_type, user_id):
            await clear_cache_by_path(**path)

    for upid in list(data_chunks.keys()):
        chunks = data_chunks[upid]
        data_chunks[upid] = [c for c in chunks if now - c.time < CHUNK_EXPIRE]
        if not data_chunks[upid]:
            del data_chunks[upid]

    return APIResponse(message="Successfully uploaded data.")


@ios_upload_api.get(
    "/proxy/{server}/{policy}/suite/user/{user_id}",
    response_model=None,
    status_code=200,
    summary="反代获取suite数据",
    description="通过iOS模块重定向至此API获取玩家suite数据",
)
async def proxy_suite(
    server: SupportedSuiteUploadServer, policy: UploadPolicy, user_id: int, request: Request
) -> Response:
    await logger.info(f"收到来自{server}服用户{user_id}的suite反代请求")
    result = await handle_proxy_upload(request, server, policy, user_id, UploadDataType.suite, PROXY, mongo)
    for path in get_clear_cache_paths(server, UploadDataType.suite, user_id):
        await clear_cache_by_path(**path)
    return result


@ios_upload_api.post(
    "/proxy/{server}/{policy}/user/{user_id}/mysekai",
    response_model=None,
    status_code=200,
    summary="反代获取mysekai数据",
    description="通过iOS模块重定向至此API获取玩家mysekai数据",
)
async def proxy_mysekai(
    server: SupportedMysekaiUploadServer, policy: UploadPolicy, user_id: int, request: Request
) -> Response:
    await logger.info(f"收到来自{server}服用户{user_id}的mysekai反代请求")
    result = await handle_proxy_upload(request, server, policy, user_id, UploadDataType.mysekai, PROXY, mongo)
    for path in get_clear_cache_paths(server, UploadDataType.mysekai, user_id):
        await clear_cache_by_path(**path)
    return result


@general_upload_api.post(
    "/upload",
    response_model=APIResponse,
    status_code=200,
    summary="上传玩家suite数据",
    description="上传玩家自己获取的suite数据",
)
async def upload_suite_data(
    server: SupportedSuiteUploadServer,
    policy: UploadPolicy,
    request: Request,
    _: None = require_upload_type(UploadDataType.suite),
) -> APIResponse:
    result = await handle_upload(await request.body(), server, policy, mongo, UploadDataType.suite.value)
    return APIResponse(message=f"{server.value.upper()} server user {result.user_id} successfully uploaded suite data.")


@general_upload_api.post(
    "/{user_id}/upload",
    response_model=APIResponse,
    status_code=200,
    summary="上传玩家mysekai数据",
    description="上传玩家自己获取的suite数据",
)
async def upload_mysekai_data(
    server: SupportedMysekaiUploadServer,
    policy: UploadPolicy,
    user_id: int,
    request: Request,
    _: None = require_upload_type(UploadDataType.mysekai),
) -> APIResponse:
    await handle_upload(await request.body(), server, policy, mongo, UploadDataType.mysekai.value, user_id)
    return APIResponse(message=f"{server.value.upper()} server user {user_id} successfully uploaded mysekai data.")


@general_upload_api.post(
    "/submit_inherit",
    response_model=APIResponse,
    status_code=200,
    summary="提交账号继承信息",
    description="上传玩家的日服/国际服账号继承码以自动获取所需数据",
    dependencies=[Depends(reject_en_mysekai_inherit)],
)
async def submit_inherit(
    server: SupportedInheritUploadServer,
    policy: UploadPolicy,
    upload_type: UploadDataType,
    data: InheritInformation = Depends(parse_json_body(InheritInformation)),
) -> APIResponse:
    retriever = ProjectSekaiDataRetriever(
        server=server, inherit=data, policy=policy, upload_type=upload_type, proxy=PROXY
    )
    result = await retriever.run()
    if retriever.is_error_exist:
        raise APIException(status=400, message=retriever.client.error_message)
    if upload_type == UploadDataType.mysekai:
        await handle_upload(result.mysekai, server, policy, mongo, upload_type, result.user_id)
    await handle_upload(result.suite, server, policy, mongo, UploadDataType.suite, result.user_id)
    return APIResponse(message=f"{result.server.upper()} server user {result.user_id} successfully uploaded data.")

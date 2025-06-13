import asyncio
from typing import TypeVar, Union
from aiohttp import ClientSession
from fastapi import Request, Response

from ..mongo import MongoDBManager
from ..schemas import SekaiDataRetrieverResponse
from ..api.handle_data import pre_handle_data, call_webhook
from ..enums import SupportedSuiteUploadServer, UploadDataType, UploadPolicy, SupportedMysekaiUploadServer
from .cryptor import unpack

T = TypeVar("T")
allowed_headers = {
    "user-agent",
    "cookie",
    "x-forwarded-for",
    "accept-language",
    "accept",
    "accept-encoding",
    "x-devicemodel",
    "x-app-hash",
    "x-operatingsystem",
    "x-kc",
    "x-unity-version",
    "x-app-version",
    "x-platform",
    "x-session-token",
    "x-asset-version",
    "x-request-id",
    "x-data-version",
    "content-type",
    "x-install-id",
}
api_endpoint = {
    SupportedSuiteUploadServer.jp: ("", ""),
    SupportedSuiteUploadServer.en: ("", ""),
    SupportedSuiteUploadServer.tw: ("", ""),
    SupportedSuiteUploadServer.kr: ("", ""),
    SupportedSuiteUploadServer.cn: ("", ""),
}
acquire_path = {UploadDataType.suite: "", UploadDataType.mysekai: ""}


async def clean_suite(suite: dict[str, T]) -> dict[str, T]:
    remove_keys = [
        "userActionSets",
        "userMusicAchievements",
        "userBillingShopItems",
        "userMaterials",
        "userUnitEpisodeStatuses",
        "userSpecialEpisodeStatuses",
        "userEventEpisodeStatuses",
        "userArchiveEventEpisodeStatuses",
        "userCharacterProfileEpisodeStatuses",
        "userCostume3dStatuses",
        "userCostume3dShopItems",
        "userReleaseConditions",
        "userMissionStatuses",
        "userEventExchanges",
        "userInformations",
        "userCustomProfiles",
        "userCustomProfileCards",
        "userCustomProfileResources",
        "userCustomProfileResourceUsages",
        "userCustomProfileGachas",
    ]
    for key in remove_keys:
        if key in suite:
            suite[key] = []
    return suite


async def filter_headers(headers: dict[str, str]) -> dict[str, str]:
    return {key: value for key, value in headers.items() if key.lower() in allowed_headers}


async def sekai_proxy_call_api(
    headers: dict[str, str],
    method: str = "GET",
    server: SupportedSuiteUploadServer = SupportedSuiteUploadServer.jp,
    data_type: UploadDataType = UploadDataType.suite,
    policy: UploadPolicy = UploadPolicy.public,
    data: bytes = None,
    params: dict[str, str] = None,
    proxy: str = None,
    user_id: int = None,
) -> SekaiDataRetrieverResponse:
    filtered_headers = await filter_headers(headers)
    url, host = api_endpoint.get(server)
    filtered_headers["Host"] = host
    options = {
        "method": method,
        "url": url + acquire_path.get(data_type).format(user_id=user_id),
        "params": params,
        "data": data if data else None,
        "headers": filtered_headers,
    }
    async with ClientSession() as session:
        async with session.request(**options, proxy=proxy) as response:
            raw_response = await response.read()
            unpacked = await asyncio.to_thread(unpack, raw_response, server)
            if response.status == 200:
                unpacked = await pre_handle_data(unpacked, user_id, policy, server)
                if data_type == UploadDataType.suite:
                    unpacked = await clean_suite(unpacked)
            return SekaiDataRetrieverResponse(
                raw_body=raw_response,
                decrypted_body=unpacked,
                status_code=response.status,
                new_headers=response.headers,
            )


async def handle_proxy_upload(
    request: Request,
    server: Union[SupportedSuiteUploadServer, SupportedMysekaiUploadServer],
    policy: UploadPolicy,
    user_id: int,
    data_type: UploadDataType,
    proxy: str,
    manager: MongoDBManager,
) -> Response:
    params = dict(request.query_params) or None
    headers = dict(request.headers)
    data = await request.body() if request.method == "POST" else None
    server = SupportedSuiteUploadServer(str(server))
    _response = await sekai_proxy_call_api(
        headers=headers,
        method=request.method,
        server=server,
        data_type=data_type,
        policy=policy,
        data=data,
        params=params,
        proxy=proxy,
        user_id=user_id,
    )
    await manager.update_data(user_id, _response.decrypted_body, data_type)
    if policy == UploadPolicy.public:
        asyncio.create_task(call_webhook(user_id, str(server), data_type, manager))
    return Response(
        content=_response.raw_body,
        status_code=_response.status_code,
        headers=_response.new_headers,
        media_type="application/octet-stream",
    )

import time
import asyncio
import traceback
from aiohttp import ClientSession
from typing import TypeVar, Dict, Union, Optional

from ..logger import AsyncLogger
from ..mongo import MongoDBManager
from ..schemas import HandleDataResult
from ..sekai_client.cryptor import unpack
from ..enums import (
    UploadPolicy,
    SupportedSuiteUploadServer,
    SupportedMysekaiUploadServer,
    SupportedInheritUploadServer,
    UploadDataType,
)

T = TypeVar("T")


async def pre_handle_data(
    data: Dict[str, T],
    user_id: int,
    policy: UploadPolicy,
    server: Union[SupportedSuiteUploadServer, SupportedMysekaiUploadServer],
) -> Dict[str, T]:
    data["upload_time"] = int(time.time())
    data["policy"] = str(policy)
    data["_id"] = user_id
    data["server"] = str(server)
    return data


async def handle_and_update_data(
    data: bytes,
    server: Union[SupportedSuiteUploadServer, SupportedMysekaiUploadServer, SupportedInheritUploadServer],
    policy: UploadPolicy,
    manager: MongoDBManager,
    data_type: UploadDataType,
    user_id: int = None,
) -> Optional[HandleDataResult]:
    try:
        data = await asyncio.to_thread(unpack, data, SupportedSuiteUploadServer(str(server)))
        if "httpStatus" in data:
            return HandleDataResult(status=data.get("httpStatus", 403), error_message=data.get("errorCode", "error"))
        if not user_id:
            user_id = data.get("userGamedata", {}).get("userId", None)
        data = await pre_handle_data(data, user_id, policy, server)
        await manager.update_data(user_id, data, data_type)
        if policy == UploadPolicy.public:
            asyncio.create_task(call_webhook(user_id, str(server), data_type, manager))
        return HandleDataResult(user_id=user_id)
    except:
        traceback.print_exc()
        return None


async def callback_webhook_api(url: str, bearer: str = None) -> None:
    logger = AsyncLogger("WebHook Callback Client", level="DEBUG")
    await logger.start()
    await logger.info(f"Calling back WebHook API: {url}")
    headers = {"User-Agent": "Haruki-Suite-DB/v2.2.0"}
    if bearer:
        headers["Authorization"] = f"Bearer {bearer}"
    async with ClientSession() as session:
        async with session.post(url, headers=headers) as response:
            if response.status == 200:
                await logger.info(f"Called back WebHook API {url} successfully.")
            else:
                await logger.error(f"Called back WebHook API {url} failed, status code: {response.status}")
    await logger.stop()
    return None


async def call_webhook(user_id: int, server: str, data_type: UploadDataType, manager: MongoDBManager) -> None:
    webhook_callback_list = await manager.get_webhook_push_api(user_id=user_id, server=server, data_type=data_type)
    if not webhook_callback_list:
        return None
    tasks = []
    for callback in webhook_callback_list:
        url = callback.get("callback_url").format(user_id=user_id, server=server, data_type=data_type)
        bearer = callback.get("bearer")
        tasks.append(callback_webhook_api(url=url, bearer=bearer))
    await asyncio.gather(*tasks)
    return None

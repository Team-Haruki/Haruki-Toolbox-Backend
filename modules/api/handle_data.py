import time
import asyncio
import traceback
from typing import TypeVar, Dict, Union, Optional
from pymongo.asynchronous.collection import AsyncCollection

from ..mongo import update_data
from ..sekai_client.cryptor import unpack
from ..enums import UploadPolicy, SupportedSuiteUploadServer, SupportedMysekaiUploadServer, SupportedInheritUploadServer

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
    collection: AsyncCollection,
    user_id: int = None,
) -> Optional[int]:
    try:
        data = await asyncio.to_thread(unpack, data, SupportedSuiteUploadServer(str(server)))
        if not user_id:
            user_id = data.get("userGamedata", {}).get("userId", None)
        data = await pre_handle_data(data, user_id, policy, server)
        await update_data(user_id, data, collection)
        return user_id
    except:
        traceback.print_exc()
        return None

import re
from typing import Optional, Tuple, Union

from modules.api.exception import APIException
from modules.cache_helpers import clear_cache_by_path
from modules.api.handle_data import handle_and_update_data
from modules.mongo import MongoDBManager
from modules.schemas import HandleDataResult
from modules.enums import (
    SupportedInheritUploadServer,
    SupportedMysekaiUploadServer,
    SupportedSuiteUploadServer,
    UploadDataType,
    UploadPolicy,
)


def get_clear_cache_paths(
    server: Union[SupportedSuiteUploadServer, SupportedMysekaiUploadServer, SupportedInheritUploadServer],
    data_type: UploadDataType,
    user_id: int,
) -> list:
    return [
        {"namespace": "public_access", "path": f"/public/{server}/{data_type}/{user_id}"},
        {
            "namespace": "public_access",
            "path": f"/public/{server}/{data_type}/{user_id}",
            "query_string": "key=upload_time",
        },
    ]


async def handle_upload(
    data: bytes,
    server: Union[SupportedSuiteUploadServer, SupportedMysekaiUploadServer, SupportedInheritUploadServer],
    policy: UploadPolicy,
    manager: MongoDBManager,
    data_type: UploadDataType,
    user_id: int = None,
) -> Optional[HandleDataResult]:
    result = await handle_and_update_data(data, server, policy, manager, data_type, user_id=user_id)
    if not user_id:
        user_id = result.user_id
    if result.status != 200:
        raise APIException(result.status, result.error_message or "Unknown Error")
    paths = get_clear_cache_paths(server, data_type, user_id)
    for path_info in paths:
        await clear_cache_by_path(**path_info)
    return result


def extract_upload_type_and_user_id(original_url: str) -> Optional[Tuple[UploadDataType, int]]:
    if UploadDataType.suite.value in original_url:
        upload_type = UploadDataType.suite
        match = re.search(r"user/(\d+)", original_url)
        if not match:
            return None
        user_id = match.group(1)
        return upload_type, int(user_id)
    elif UploadDataType.mysekai.value in original_url:
        upload_type = UploadDataType.mysekai
        match = re.search(r"user/(\d+)", original_url)
        if not match:
            return None
        user_id = match.group(1)
        return upload_type, int(user_id)
    else:
        return None

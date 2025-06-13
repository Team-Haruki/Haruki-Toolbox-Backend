from typing import Dict, List
from fastapi import APIRouter, Depends

from modules.schemas import APIResponse
from modules.api.depends import validate_webhook_user
from modules.enums import SupportedSuiteUploadServer, UploadDataType

from configs import WEBHOOK_JWT_SECRET
from utils import mongo

webhook = APIRouter(prefix="/webhook", tags=["RegisterWebhook"])


@webhook.get(
    "/subscribers",
    response_model=None,
    summary="获取已注册WebHook用户",
    description="获取指定WebHookID已注册用户",
)
async def get_webhook_subscribers(
    webhook_id: str = Depends(validate_webhook_user(WEBHOOK_JWT_SECRET, mongo)),
) -> List[Dict[str, str]]:
    return await mongo.get_webhook_subscribers(webhook_id)


@webhook.put(
    "/{server}/{data_type}/{user_id}",
    response_model=APIResponse,
    summary="注册WebHook",
    description="向服务器注册指定服务器指定用户指定数据的WebHook",
)
async def register_webhook(
    server: SupportedSuiteUploadServer,
    data_type: UploadDataType,
    user_id: int,
    webhook_id: str = Depends(validate_webhook_user(WEBHOOK_JWT_SECRET, mongo)),
) -> APIResponse:
    await mongo.add_webhook_push_user(user_id=user_id, server=server, data_type=data_type, webhook_id=webhook_id)
    return APIResponse(message="Registered webhook push user successfully.")


@webhook.delete(
    "/{server}/{data_type}/{user_id}",
    response_model=APIResponse,
    summary="反注册WebHook",
    description="向服务器反注册指定服务器指定用户指定数据的WebHook",
)
async def unregister_webhook(
    server: SupportedSuiteUploadServer,
    data_type: UploadDataType,
    user_id: int,
    webhook_id: str = Depends(validate_webhook_user(WEBHOOK_JWT_SECRET, mongo)),
) -> APIResponse:
    await mongo.remove_webhook_push_user(user_id=user_id, server=server, data_type=data_type, webhook_id=webhook_id)
    return APIResponse(message="Unregistered webhook push user successfully.")

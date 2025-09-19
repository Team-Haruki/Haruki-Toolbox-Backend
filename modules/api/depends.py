from typing import Type, TypeVar
from pydantic import ValidationError
from jwt import decode, InvalidTokenError
from fastapi import Depends, HTTPException, Path, Request

from ..mongo import MongoDBManager
from ..enums import UploadDataType, SupportedInheritUploadServer

T = TypeVar("T")


def require_upload_type(expected_type: UploadDataType):
    def checker(upload_type: UploadDataType = Path(...)) -> None:
        if upload_type != expected_type:
            raise HTTPException(status_code=400, detail=f"Invalid upload_type: expected {expected_type}")

    return Depends(checker)


def reject_en_mysekai_inherit(
    server: SupportedInheritUploadServer = Path(...),
    upload_type: UploadDataType = Path(...),
):
    if server == SupportedInheritUploadServer.en and upload_type == UploadDataType.mysekai:
        raise HTTPException(
            status_code=403,
            detail="Haruki Inherit can not accept EN server's mysekai data upload request at this time.",
        )


def parse_json_body(model: Type[T]):
    async def dependency(request: Request) -> T:
        try:
            body = await request.json()
            obj = model(**body)
        except ValidationError as ve:
            raise HTTPException(status_code=422, detail=f"Validation error: {ve.errors()}")
        return obj

    return dependency


def validate_webhook_user(secret_key: str, manager: MongoDBManager):
    async def dependency(request: Request) -> str:
        jwt_token = request.headers.get("X-Haruki-Suite-Webhook-Token")
        if not jwt_token:
            raise HTTPException(status_code=401, detail="Missing X-Haruki-Suite-Webhook-Token header")
        try:
            payload = decode(jwt_token, secret_key, algorithms=["HS256"])
            _id = payload.get("_id")
            credential = payload.get("credential")
            if not _id or not credential:
                raise HTTPException(status_code=403, detail="Invalid token payload")
        except InvalidTokenError:
            raise HTTPException(status_code=403, detail="Invalid or expired JWT")

        user = await manager.get_webhook_user(_id, credential)
        if not user:
            raise HTTPException(status_code=403, detail="Webhook user not found or credential mismatch")

        return _id

    return dependency

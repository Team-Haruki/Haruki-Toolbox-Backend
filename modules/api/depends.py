from typing import Type, TypeVar
from pydantic import ValidationError
from fastapi import Depends, HTTPException, Path, Request

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

from bson import ObjectId
from typing import Dict, Any, Optional
from pymongo import AsyncMongoClient
from pymongo.results import UpdateResult

from .enums import UploadDataType


class MongoDBManager:
    def __init__(
        self, db_url: str, db: str, suite: str, mysekai: str, webhook_user: str, webhook_user_user: str
    ) -> None:
        self.client = AsyncMongoClient(db_url)
        self.suite_collection = self.client[db][suite]
        self.mysekai_collection = self.client[db][mysekai]
        self.webhook_collection = self.client[db][webhook_user]
        self.webhook_user_collection = self.client[db][webhook_user_user]

    async def update_data(self, user_id: int, data: Dict[str, Any], data_type: UploadDataType) -> UpdateResult:
        collection = self.suite_collection if data_type == UploadDataType.suite else self.mysekai_collection
        return await collection.update_one(
            filter={"_id": user_id},
            update={"$set": data},
            upsert=True,
        )

    async def get_data(self, user_id: int, server: str, data_type: UploadDataType) -> Optional[Dict[str, Any]]:
        collection = self.suite_collection if data_type == UploadDataType.suite else self.mysekai_collection
        return await collection.find_one(filter={"_id": user_id, "server": server})

    async def get_webhook_user(self, _id: str, credential: str) -> Optional[Dict[str, str]]:
        return await self.webhook_collection.find_one(
            filter={"_id": ObjectId(_id), "credential": credential},
            projection={"callback_url": 1, "credential": 1, "_id": 0},
        )

    async def get_webhook_push_api(self, user_id: int, server: str, data_type: str) -> list[Dict[str, str]]:
        binding = await self.webhook_user_collection.find_one(
            {"uid": str(user_id), "server": server, "type": data_type}
        )
        if not binding or "webhook_user_ids" not in binding:
            return []

        webhook_ids = binding["webhook_user_ids"]
        cursor = self.webhook_collection.find(
            {"_id": {"$in": [ObjectId(wid) for wid in webhook_ids]}},
            projection={"callback_url": 1, "bearer": 1, "_id": 0},
        )
        return [doc async for doc in cursor]

    async def add_webhook_push_user(self, user_id: int, server: str, data_type: str, webhook_id: str) -> None:
        await self.webhook_user_collection.update_one(
            {"uid": str(user_id), "server": server, "type": data_type},
            {"$addToSet": {"webhook_user_ids": webhook_id}},
            upsert=True,
        )

    async def remove_webhook_push_user(self, user_id: int, server: str, data_type: str, webhook_id: str) -> None:
        await self.webhook_user_collection.update_one(
            {"uid": str(user_id), "server": server, "type": data_type},
            {"$pull": {"webhook_user_ids": webhook_id}},
        )

    async def get_webhook_subscribers(self, webhook_id: str) -> list[Dict[str, str]]:
        cursor = self.webhook_user_collection.find(
            {"webhook_user_ids": webhook_id},
            projection={"uid": 1, "server": 1, "type": 1, "_id": 0},
        )
        return [doc async for doc in cursor]

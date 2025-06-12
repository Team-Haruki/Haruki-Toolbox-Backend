from typing import Dict, Any, Optional
from pymongo.results import UpdateResult
from pymongo.asynchronous.collection import AsyncCollection


async def update_data(user_id: int, data: Dict[str, Any], collection: AsyncCollection) -> UpdateResult:
    result = await collection.update_one(
        filter={"_id": user_id},
        update={"$set": data},
        upsert=True,
    )
    return result


async def get_data(user_id: int, server: str, collection: AsyncCollection) -> Optional[Dict[str, Any]]:
    return await collection.find_one(filter={"_id": user_id, "server": server})

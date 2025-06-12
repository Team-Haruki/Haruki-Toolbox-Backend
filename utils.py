from pymongo import AsyncMongoClient

from configs import MONGODB_DB, MONGODB_DB_URL, MYSEKAI_DB, SUITE_DB

mongo_client = AsyncMongoClient(MONGODB_DB_URL)
_db = mongo_client[MONGODB_DB]
suite_collections = _db[SUITE_DB]
mysekai_collections = _db[MYSEKAI_DB]

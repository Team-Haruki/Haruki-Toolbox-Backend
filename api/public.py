from typing import Dict, Any
from fastapi import APIRouter, Request
from fastapi_cache.decorator import cache

from modules.mongo import get_data
from modules.api.exception import APIException
from modules.cache_helpers import ORJsonCoder, cache_key_builder
from modules.enums import SupportedSuiteUploadServer, UploadDataType, UploadPolicy
from utils import suite_collections, mysekai_collections

public_api = APIRouter(prefix="/public/{server}/{data_type}")


@public_api.get(
    "/{user_id}", response_model=None, summary="获取玩家数据", description="根据传入的游戏服务器、玩家uid与获取数据的类型获取玩家的数据"
)
@cache(expire=300, namespace="public_access", coder=ORJsonCoder, key_builder=cache_key_builder)  # type: ignore
async def get_user(
    server: SupportedSuiteUploadServer, data_type: UploadDataType, user_id: int, request: Request
) -> Dict[str, Any]:
    result = await get_data(
        user_id, server, collection=mysekai_collections if data_type == UploadDataType.mysekai else suite_collections
    )
    if not result:
        raise APIException(status=404, message="Player data not found.")
    elif result["policy"] == UploadPolicy.private:
        raise APIException(status=403, message="This player's data is not public accessible.")
    if data_type == UploadDataType.suite:
        suite = {
            "userGamedata": {
                key: value
                for key, value in result["userGamedata"].items()
                if key in ["userId", "name", "deck", "exp", "totalExp"]
            },
        }
        allowed_keys = [
            "userDecks",
            "userCards",
            "userAreas",
            "userHonors",
            "userMusics",
            "userEvents",
            "upload_time",
            "userProfile",
            "userCharacters",
            "userBondsHonors",
            "userMusicResults",
            "userMysekaiGates",
            "userProfileHonors",
            "userMysekaiCanvases",
            "userRankMatchSeasons",
            "userMysekaiMaterials",
            "userMysekaiCharacterTalks",
            "userChallengeLiveSoloStages",
            "userChallengeLiveSoloResults",
            "userMysekaiFixtureGameCharacterPerformanceBonuses",
        ]
        request_key = request.query_params.get("key")
        if request_key:
            request_keys = request_key.split(",")
            if len(request_keys) == 1:
                if request_keys[0] in allowed_keys:
                    return result.get(request_keys[0], {})
                else:
                    raise APIException(status=403, message="Invalid request key")
            else:
                for key in request_keys:
                    if key in allowed_keys:
                        suite[key] = result.get(key)
                return suite
        elif request_key and request_key not in allowed_keys:
            raise APIException(status=403, message="Invalid request key")
        else:
            suite.update({key: result.get(key, []) for key in allowed_keys})
            return suite
    elif data_type == UploadDataType.mysekai:
        request_key = request.query_params.get("key")
        if request_key:
            request_keys = request_key.split(",")
            mysekai_data = {key: result.get(key, []) for key in request_keys if key not in ["_id", "policy"]}
        else:
            mysekai_data = {key: value for key, value in result.items() if key not in ["_id", "policy"]}
        return mysekai_data
    raise APIException(status=500, message="Unknown error.")

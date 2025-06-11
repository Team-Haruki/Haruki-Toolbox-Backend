from fastapi import APIRouter, Request
from fastapi_cache.decorator import cache
from fastapi.responses import ORJSONResponse

from modules.mongo import get_data
from modules.cache_helpers import ORJsonCoder, cache_key_builder
from modules.enums import SupportedSuiteUploadServer, UploadDataType, UploadPolicy
from utils import suite_collections, mysekai_collections

public_api = APIRouter(prefix="/public/{server}/{data_type}")


@public_api.get(
    "/{user_id}", summary="获取玩家数据", description="根据传入的游戏服务器、玩家uid与获取数据的类型获取玩家的数据"
)
@cache(expire=300, namespace="public_access", coder=ORJsonCoder, key_builder=cache_key_builder) # type: ignore
async def get_user(
    server: SupportedSuiteUploadServer, data_type: UploadDataType, user_id: int, request: Request
) -> ORJSONResponse:
    result = await get_data(
        user_id, server, collection=mysekai_collections if data_type == UploadDataType.mysekai else suite_collections
    )
    if not result:
        return ORJSONResponse(content={"error": "Player data not found."}, status_code=404)
    elif result["policy"] == UploadPolicy.private:
        return ORJSONResponse(content={"error": "This player's data is not public accessible."}, status_code=403)
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
                return (
                    (ORJSONResponse(content=result.get(request_keys[0], {}), status_code=200))
                    if request_keys[0] in allowed_keys
                    else (ORJSONResponse(content={"error": "Invalid request key"}, status_code=403))
                )
            else:
                for key in request_keys:
                    if key in allowed_keys:
                        suite[key] = result.get(key)
                return ORJSONResponse(content=suite)
        elif request_key and request_key not in allowed_keys:
            return ORJSONResponse(content={"error": "Invalid request key"}, status_code=403)
        else:
            suite.update({key: result.get(key, []) for key in allowed_keys})
            return ORJSONResponse(content=suite, status_code=200)
    elif data_type == "mysekai":
        request_key = request.query_params.get("key")
        if request_key:
            request_keys = request_key.split(",")
            mysekai_data = {key: result.get(key, []) for key in request_keys if key not in ["_id", "policy"]}
        else:
            mysekai_data = {key: value for key, value in result.items() if key not in ["_id", "policy"]}
        return ORJSONResponse(content=mysekai_data)
    return ORJSONResponse(content={"error": "Unknown error."}, status_code=500)

from pymongo import MongoClient
from quart import Blueprint, jsonify, request, Response

from Configs.configs import MONGODB_SERVER, SUITE_DB, MONGODB_DB, MYSEKAI_DB

public_api = Blueprint('public_api', __name__, url_prefix='/public_api')
mongo_client = MongoClient(MONGODB_SERVER)
db = mongo_client[MONGODB_DB]
db_collections = {
    'suite': db[SUITE_DB],
    'mysekai': db[MYSEKAI_DB]
}


async def _get_user_data(user_id, data_type):
    collection = db_collections[data_type]
    result = collection.find_one({"_id": user_id})
    if result:
        policy = result.get('policy')
        return policy, result
    else:
        return None, None


@public_api.route('/<int:user_id>/get_data/<data_type>', methods=['GET'])
async def _get_pjsk_data(user_id, data_type):
    if data_type not in ['suite', 'mysekai']:
        return jsonify({'error': 'Invalid data type'}), 400

    policy, result = await _get_user_data(user_id, data_type)
    if not policy and not result:
        return jsonify({'error': 'No such user.'}), 404
    elif policy == 'private':
        return jsonify({'error': 'This user data cannot be retrieved due to user policy.'}), 403
    elif policy == 'public':
        if data_type == 'suite':
            suite = {
                'userGamedata': {
                    key: value for key, value in result['userGamedata'].items()
                    if key in ['userId', 'name', 'deck', 'exp', 'totalExp']
                },
            }
            allowed_keys = [
                'userDecks',
                'userCards',
                'userAreas',
                'userHonors',
                'userMusics',
                'userEvents',
                'upload_time',
                'userProfile',
                'userCharacters',
                'userBondsHonors',
                'userMusicResults',
                'userMysekaiGates',
                'userProfileHonors',
                'userMysekaiCanvases',
                'userRankMatchSeasons',
                'userMysekaiMaterials',
                'userMysekaiCharacterTalks',
                'userChallengeLiveSoloStages',
                'userChallengeLiveSoloResults',
                'userMysekaiFixtureGameCharacterPerformanceBonuses'
            ]
            request_key = request.args.get('key')
            if request_key:
                request_keys = request_key.split(',')
                if len(request_keys) == 1:
                    return (jsonify(result.get(request_keys[0], {})), 200) if request_keys[0] in allowed_keys else (
                    jsonify({'error': 'Invalid request key'}), 403)
                else:
                    suite.update({key: result.get(key, []) for key in request_keys if key in allowed_keys})
                    return jsonify(suite), 200
            elif request_key and request_key not in allowed_keys:
                return jsonify({'error': 'Invalid request key'}), 403
            else:
                suite.update({key: result.get(key, []) for key in allowed_keys})
                return jsonify(suite), 200
        elif data_type == 'mysekai':
            request_key = request.args.get('key')
            if request_key:
                request_keys = request_key.split(',')
                mysekai_data = {key: result.get(key, []) for key in request_keys if key not in ['_id', 'policy']}
            else:
                mysekai_data = {key: value for key, value in result.items() if key not in ['_id', 'policy']}
            return jsonify(mysekai_data), 200

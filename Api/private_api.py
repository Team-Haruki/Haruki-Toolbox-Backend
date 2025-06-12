from pymongo import MongoClient
from quart import Blueprint, jsonify, request, Response

from Configs.configs import MONGODB_SERVER, SUITE_DB, MONGODB_DB, MYSEKAI_DB

private_api = Blueprint('private_api', __name__, url_prefix='/p2x')
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
        return result
    else:
        return None


@private_api.route('/<int:user_id>/get_data/<data_type>', methods=['GET'])
async def _get_pjsk_data(user_id, data_type):
    if request.headers.get('Authorization') == '':
        result = await _get_user_data(user_id, data_type)
        if not result:
            return jsonify({'error': 'No such user.'}), 404
        else:
            return jsonify(result), 200
    else:
        return jsonify({'error': 'Unauthorized.'}), 401

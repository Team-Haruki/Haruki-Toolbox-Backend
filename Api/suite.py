import time
from pymongo import MongoClient
from quart import Blueprint, jsonify, request, Response

from Configs.configs import MONGODB_SERVER, SUITE_DB, MONGODB_DB
from Utils.api_unpacker import unpack
from Utils.sekai_proxy import sekai_proxy_call_api
from Utils.clean_data import clean_suite

suite_api = Blueprint('suite_api', __name__, url_prefix='/suite')
suite_db_client = MongoClient(MONGODB_SERVER)
suite_db = suite_db_client[MONGODB_DB]
suite_collection = suite_db[SUITE_DB]


@suite_api.route('/<policy>/upload', methods=['POST'])
async def _upload_suite_data(policy):
    try:
        if policy not in ['private', 'public']:
            response = {
                'message': 'policy must be either private or public.'
            }
            return jsonify(response), 400
        body = await request.get_data()
        body = await unpack(body)

        user_id = body.get('userGamedata', {}).get('userId', None)
        if not user_id:
            response = {
                'message': 'Invalid suite.'
            }
            return jsonify(response), 400

        current_time = int(time.time())
        body['upload_time'] = current_time
        body['policy'] = policy
        body['_id'] = user_id
        body = await clean_suite(body)

        suite_collection.update_one(
            {"_id": user_id},
            {"$set": body},
            upsert=True
        )

        response = {
            'message': f'{user_id} successfully uploaded suite data.'
        }
        return jsonify(response), 200
    except Exception as e:
        return jsonify({'message': 'Internal server error'}), 500


@suite_api.route('/<policy>/<path:path>', methods=['GET'])
async def _proxy_suite_data(policy, path):
    headers = request.headers
    if request.args:
        params = request.args.to_dict()
    else:
        params = None

    response, new_headers, status = await sekai_proxy_call_api(path, headers, method='GET', params=params)
    if status == 200:
        unpacked_response = await unpack(response)
        user_id = unpacked_response.get('userGamedata', {}).get('userId', None)

        current_time = int(time.time())
        unpacked_response['upload_time'] = current_time
        unpacked_response['policy'] = policy
        unpacked_response['_id'] = user_id
        unpacked_response = await clean_suite(unpacked_response)

        suite_collection.update_one(
            {"_id": user_id},
            {"$set": unpacked_response},
            upsert=True
        )

    packed_response = Response(response, status=status, headers=new_headers, mimetype='application/octet-stream')
    return packed_response

import time
import asyncio
import hashlib
import ujson as json
from pymongo import MongoClient
from aiohttp import ClientSession
from quart import Blueprint, jsonify, request, Response

from Configs.configs import MONGODB_SERVER, MYSEKAI_DB, MONGODB_DB
from Utils.api_unpacker import unpack
from Utils.sekai_proxy import sekai_proxy_call_api

mysekai_api = Blueprint('mysekai_api', __name__, url_prefix='/mysekai')
mysekai_db_client = MongoClient(MONGODB_SERVER)
mysekai_db = mysekai_db_client[MONGODB_DB]
mysekai_collection = mysekai_db[MYSEKAI_DB]


def sha256_uid(number: int) -> str:
    num_bytes = str(number).encode()
    first_hash = hashlib.sha256(num_bytes).digest()
    second_hash = hashlib.sha256(first_hash).hexdigest()
    return second_hash



@mysekai_api.route('/<policy>/<user_id>/upload', methods=['POST'])
async def _upload_mysekai_data(policy, user_id):
    try:
        if policy not in ['private', 'public']:
            response = {
                'message': 'policy must be either private or public.'
            }
            return jsonify(response), 400

        body = await request.get_data()
        body = await unpack(body)
        current_time = int(time.time())

        body['upload_time'] = current_time
        body['policy'] = policy
        body['_id'] = int(user_id)
        mysekai_collection.update_one(
            {"_id": int(user_id)},
            {"$set": body},
            upsert=True
        )

        response = {
            'message': 'successfully upload mysekai data.'
        }
        return jsonify(response), 200
    except Exception as e:
        return jsonify({'message': 'Internal server error'}), 500


@mysekai_api.route('/<policy>/user/<int:user_id>/mysekai', methods=['POST'])
async def _proxy_mysekai_data(policy, user_id):
    headers = request.headers
    body = await request.get_data()
    if request.args:
        params = request.args.to_dict()
    else:
        params = None

    path = f'user/{user_id}/mysekai'
    response, new_headers, status = await sekai_proxy_call_api(path, headers, method='POST', data=body, params=params)

    if status == 200:
        unpacked_response = await unpack(response)
        current_time = int(time.time())

        unpacked_response['upload_time'] = current_time
        unpacked_response['policy'] = policy
        unpacked_response['_id'] = user_id

        mysekai_collection.update_one(
            {"_id": user_id},
            {"$set": unpacked_response},
            upsert=True
        )

    packed_response = Response(response, status=status, headers=new_headers, mimetype='application/octet-stream')
    return packed_response

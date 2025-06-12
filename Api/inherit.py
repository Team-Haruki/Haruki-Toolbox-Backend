import traceback
from urllib.parse import parse_qs, urlparse
from quart import Blueprint, request, jsonify
from Modules.sekai_client.retriever import ProjectSekaiDataRetriever

inherit_api = Blueprint("inherit_api", __name__, url_prefix='/inherit')


async def parse_params():
    if request.method == 'GET':
        return request.args.to_dict()
    elif request.method == 'POST':
        body = await request.get_data()
        if request.content_type == "application/x-www-form-urlencoded":
            parsed_url = urlparse("?" + body.decode('utf-8'))
            query_params = parse_qs(parsed_url.query)
            return {k: v[0] if isinstance(v, list) else v for k, v in query_params.items()}
        else:
            return await request.json
    return {}


def response_builder(status_code, message, is_success=True):
    response = {
        'result': 'success' if is_success else 'failure',
        'message': message
    }
    return jsonify(response), status_code


async def main(inherit_id, inherit_password, user_policy, upload_type):
    retriever = ProjectSekaiDataRetriever(inherit_id, inherit_password, user_policy, upload_type)
    try:
        await retriever.run()
        if not retriever.is_error_exist:
            return response_builder(200, 'Your data has been uploaded successfully.')
        else:
            return response_builder(500, retriever.error_message, is_success=False)
    except Exception as e:
        traceback.print_exc()
    finally:
        retriever.client.close()



@inherit_api.route('/submit_inherit', methods=['POST', 'GET'])
async def submit_inherit():
    valid_policy = ['public', 'private']
    params = await parse_params()
    _id = params.get('id')
    _pwd = params.get('pwd')
    _policy = params.get('policy')
    _type = params.get('type')

    if not all([_id, _pwd, _policy, _type]):
        return response_builder(400, 'Incomplete parameters.', is_success=False)
    if _policy not in valid_policy:
        return response_builder(400, 'Policy error, policy can only be private or public.', is_success=False)

    return await main(_id, _pwd, _policy, _type)

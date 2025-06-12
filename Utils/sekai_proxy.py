from aiohttp import ClientSession
from Configs.configs import PROXY


async def filter_headers(headers):
    allowed_headers = [
        'User-Agent',
        'Cookie',
        'X-Forwarded-For',
        'Accept-Language',
        'Accept',
        'Accept-Encoding',
        'X-Devicemodel',
        'X-App-Hash',
        'X-Operatingsystem',
        'X-Kc',
        'X-Unity-Version',
        'X-App-Version',
        'X-Platform',
        'X-Session-Token',
        'X-Asset-Version',
        'X-Request-Id',
        'X-Data-Version',
        'Content-Type',
        'X-Install-Id'
    ]
    return {key: value for key, value in headers.items() if key in allowed_headers}


async def sekai_proxy_call_api(path, headers, method='GET', data=None, params=None):
    filtered_headers = await filter_headers(headers)
    filtered_headers['Host'] = ''
    options = {
        'method': method,
        'url': f'url/{path}',
        'params': params,
        'data': data if data else None,
        'headers': filtered_headers
    }
    async with ClientSession() as session:
        async with session.request(**options, proxy=PROXY) as response:
            return await response.read(), response.headers, response.status

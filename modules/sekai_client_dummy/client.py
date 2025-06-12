import copy
import json
import asyncio
from uuid import uuid4
from base64 import b64decode
from aiohttp import ClientSession
from jwt import encode as encode_inherit
from typing import Dict, Callable, Any, Union, List, Optional

from .model import RequestData
from ..logger import AsyncLogger
from ..enums import SupportedInheritUploadServer, SupportedSuiteUploadServer
from ..schemas import InheritInformation


class ProjectSekaiClient(object):
    def __init__(
        self,
        server: SupportedInheritUploadServer,
        api: str,
        version_info_url: str,
        inherit: InheritInformation,
        headers: Dict[str, str],
        pack_func: Callable[[Any, SupportedSuiteUploadServer], bytes],
        unpack_func: Callable[[bytes, SupportedSuiteUploadServer], Union[Dict[str, Any], List[Any]]],
        proxy: str = None,
    ) -> None:
        self.server = server
        self.api = api
        self.version_info_url = version_info_url
        self.is_error_exist = False
        self.error_message = None
        self.inherit = inherit
        self.user_id = None
        self.credential = None
        self.headers = headers
        self.login_bonus = False
        self.session = None
        self.pack = pack_func
        self.unpack = unpack_func
        self.proxy = proxy
        self.logger = AsyncLogger(__name__, level="DEBUG")

    async def pack_data(self, content: Union[Dict[str, Any], List[Any]]) -> bytes:
        return await asyncio.to_thread(self.pack, content, SupportedSuiteUploadServer(str(self.server)))

    async def unpack_data(self, encrypted: bytes) -> Union[Dict[str, Any], List[Any]]:
        return await asyncio.to_thread(self.unpack, encrypted, SupportedSuiteUploadServer(str(self.server)))

    async def generate_inherit_token(self):
        inherit_header = {}
        inherit_payload = {}
        jwt_token = "1" if self.server == SupportedSuiteUploadServer.jp else "2"
        return encode_inherit(headers=inherit_header, payload=inherit_payload, key=jwt_token)

    async def _get_cookies(self, retries: int = 5):
        if self.server == SupportedInheritUploadServer.jp:
            try:
                async with ClientSession() as session:
                    url = "jp.issue"
                    async with session.post(url, proxy=self.proxy) as response:
                        if response.status == 200:
                            self.headers["Cookie"] = response.headers["Set-Cookie"]
                            await self.logger.info("Cookies parsed.")
                        else:
                            await self.logger.error("Failed to parse cookies.")
            except Exception as e:
                await self.logger.warning(f"Aiohttp returned error while parsing cookies: {repr(e)}, retrying...")
                if retries > 0:
                    return await self._get_cookies(retries=retries - 1)
                else:
                    self.is_error_exist = True
                    await self.logger.error("Failed to parse cookies.")
                    self.error_message = f"Failed to parse cookies: {repr(e)}"
                    return None
        else:
            return None

    async def _parse_app_version(self, retries: int = 5) -> None:
        if self.is_error_exist:
            return None
        try:
            async with ClientSession() as session:
                async with session.get(self.version_info_url, timeout=5, proxy=self.proxy) as response:
                    if response.status == 200:
                        data = await response.text()
                        data = json.loads(data)
                        self.headers["X-App-Version"] = data["appVersion"]
                        self.headers["X-App-Hash"] = data["appHash"]
                        self.headers["X-Data-Version"] = data["dataVersion"]
                        self.headers["X-Asset-Version"] = data["assetVersion"]
                        return None
                    else:
                        self.is_error_exist = True
                        self.error_message = "Game version API returned error, status code: {response.status}"
                        await self.logger.error(f"{self.error_message}")
                        return None
        except asyncio.TimeoutError:
            self.is_error_exist = True
            self.error_message = f"Call game version API timed out."
            await self.logger.error(f"{self.error_message}")
            return None
        except Exception as e:
            await self.logger.error(f"Aiohttp returned error while parsing version: {repr(e)}, retrying...")
            if retries > 0:
                return await self._parse_app_version(retries=retries - 1)
            else:
                await self.logger.error(f"Failed to parse game version, {repr(e)}")
                self.is_error_exist = True
                self.error_message = f"Failed to parse game version."
                return None

    async def call_api(
        self,
        path: str,
        method: str = "GET",
        params: Dict[str, str] = None,
        data: bytes = None,
        custom_headers: Dict[str, str] = None,
    ) -> Optional[bytes]:
        if custom_headers:
            headers = copy.deepcopy(self.headers)
            headers.update(custom_headers)
        else:
            headers = self.headers

        headers["X-Request-Id"] = str(uuid4())
        try:
            options = {"method": method, "url": f"{self.api}{path}", "params": params, "data": data, "headers": headers}

            async with self.session.request(**options, proxy=self.proxy, timeout=8) as response:
                if response.status == 200:
                    if "X-Session-Token" in response.headers:
                        self.headers["X-Session-Token"] = response.headers["X-Session-Token"]
                    if (
                        "X-Login-Bonus-Status" in response.headers
                        and response.headers["X-Login-Bonus-Status"] == "true"
                    ):
                        self.login_bonus = True
                    return await response.read()
                else:
                    # await self.logger.debug(f'Calling api options: {options}')
                    await self.logger.error(
                        f"Error occurred while calling api, status: {response.status}",
                    )
                    await self.logger.error(await self.unpack_data(await response.read()))
                    return await response.read()
        except asyncio.TimeoutError:
            self.is_error_exist = True
            self.error_message = "Game API request timed out."
            await self.logger.error(f"{self.error_message}")
            return None

    async def inherit_account(self, return_user_id: bool = False) -> None:
        if self.is_error_exist:
            return None
        base_path = ""
        if return_user_id:
            path = base_path + "False"
            start_message = "Getting userId..."
            error_message = (
                "Failed to get user infomation: auth failed, it may be because your account or password is incorrect."
            )
        else:
            path = base_path + "True"
            start_message = "Inheriting account..."
            error_message = "The unknown error occurred while inheriting the account, please contact the developer."
        try:
            token = await self.generate_inherit_token()
            inherit_token = {"": token}
            await self.logger.info(start_message)
            if self.server == SupportedInheritUploadServer.en:
                path += "&isAdult=True&tAge=16"
            data = await self.call_api(
                path, method="POST", data=b64decode(RequestData.General), custom_headers=inherit_token
            )
            await asyncio.sleep(1)
            data = await self.unpack_data(data)
            if "afterUserGamedata" in data and return_user_id:
                self.user_id = data["afterUserGamedata"]["userId"]
                await self.logger.info("Retrieved user_id successfully.")
                return None
            elif "afterUserGamedata" in data and not return_user_id:
                self.credential = data["credential"]
                await self.logger.info("Inherited account successfully.")
                return None
            else:
                self.is_error_exist = True
                self.error_message = error_message
                return None
        except Exception as e:
            await self.logger.error(f"Error occurred while inheriting the account, reason: {repr(e)}")
            self.is_error_exist = True
            self.error_message = (
                "The unknown error occurred while inheriting the account, please contact the developer."
            )
            return None

    async def login(self) -> None:
        if self.is_error_exist:
            return None
        await self.logger.info("Authing user...")
        body = {"credential": self.credential, "deviceId": None}
        body = await self.pack_data(body)
        path = f"/"
        result = await self.call_api(path=path, method="PUT", data=body)
        response = await self.unpack_data(result)
        await asyncio.sleep(1)
        await self.call_api(path="/system")
        try:
            self.headers["X-Session-Token"] = response["sessionToken"]
            return None
        except Exception as e:
            self.is_error_exist = True
            self.error_message = "The unknown error occurred while authenticating user, please contact the developer."
            await self.logger.error("Failed to authenticate user, response: {}".format(repr(e)))
            return None

    async def init(self) -> None:
        await self.logger.start()
        await self._get_cookies()
        await self._parse_app_version()
        self.session = ClientSession()
        await self.inherit_account(return_user_id=True)
        await self.inherit_account()
        await self.login()

    async def close(self) -> None:
        await self.logger.stop()
        await self.session.close()

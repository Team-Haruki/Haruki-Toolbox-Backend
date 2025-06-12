import asyncio
from typing import Optional
from base64 import b64decode

from ..logger import AsyncLogger
from ..schemas import InheritInformation, SekaiInheritDataRetrieverResponse
from ..enums import SupportedInheritUploadServer, UploadPolicy, UploadDataType
from .model import RequestData
from .client import ProjectSekaiClient
from .cryptor import pack, unpack

api = {SupportedInheritUploadServer.jp: "", SupportedInheritUploadServer.en: ""}
headers = {SupportedInheritUploadServer.jp: {}, SupportedInheritUploadServer.en: {}}
version = {SupportedInheritUploadServer.jp: "", SupportedInheritUploadServer.en: ""}


class ProjectSekaiDataRetriever:
    def __init__(
        self,
        server: SupportedInheritUploadServer,
        inherit: InheritInformation,
        policy: UploadPolicy,
        upload_type: UploadDataType,
        proxy: str = None,
    ) -> None:
        self.client = ProjectSekaiClient(
            server=server,
            api=api.get(server),
            version_info_url=version.get(server),
            inherit=inherit,
            headers=headers.get(server),
            pack_func=pack,
            unpack_func=unpack,
            proxy=proxy,
        )
        self.policy = policy
        self.upload_type = upload_type
        self.logger = AsyncLogger(__name__, level="DEBUG")
        self.is_error_exist = False
        self.error_message = None

    async def retrieve_suite(self) -> Optional[bytes]:
        if self.is_error_exist:
            return None
        await self.logger.info("Getting suite...")
        base_path = f""
        suite = await self.client.call_api(path=base_path)
        if suite:
            await asyncio.sleep(1)
            await self.logger.info("Calling suite...")
            path = base_path + ""
            await self.client.call_api(path=path)
            await asyncio.sleep(1)
            await self.client.call_api(path="/system")
            await asyncio.sleep(1)
            unpacked_suite = await self.client.unpack_data(suite)
            friend = bool(unpacked_suite["userFriends"])
            if self.client.login_bonus:
                if friend:
                    await asyncio.create_task(self.refresh_home(friends=True, login=True))
                else:
                    await asyncio.create_task(self.refresh_home(login=True))
            else:
                if friend:
                    await asyncio.create_task(self.refresh_home(friends=True))
                else:
                    await asyncio.create_task(self.refresh_home())
            return suite
        else:
            self.is_error_exist = True
            self.error_message = "Failed to retrieve suite, it may be due to API response timeout."
            return None

    async def refresh_home(self, friends: bool = False, login: bool = False) -> None:
        if self.is_error_exist:
            return None
        await self.logger.info("Refreshing home...")
        if friends:
            await self.client.call_api(path=f"")
            await self.client.call_api(path="/system")
            await self.client.call_api(path="/information")
        else:
            await self.client.call_api(path="/system")
            await self.client.call_api(path="/information")
        refresh_path = f""
        if login:
            return await self.client.call_api(
                path=refresh_path, method="PUT", data=await self.client.pack_data(RequestData.RefreshLogin)
            )
        else:
            return await self.client.call_api(
                path=refresh_path, method="PUT", data=await self.client.pack_data(RequestData.Refresh)
            )

    async def retrieve_mysekai(self) -> Optional[bytes]:
        if self.is_error_exist:
            return None
        response = await self.client.call_api(path="/module-maintenance/MYSEKAI")
        response = await self.client.unpack_data(response)
        if response["isOngoing"] is True:
            return None
        response = await self.client.call_api(path="/module-maintenance/MYSEKAI_ROOM")
        response = await self.client.unpack_data(response)
        if response["isOngoing"] is True:
            return None
        mysekai_data = await self.client.call_api(path=f"", method="POST", data=b64decode(RequestData.General))
        await self.client.call_api(path=f"", method="POST", data=await self.client.pack_data(RequestData.MySekaiRoom))
        await self.client.call_api(path=f"")
        return mysekai_data

    async def run(self) -> Optional[SekaiInheritDataRetrieverResponse]:
        await self.logger.start()
        await self.client.init()
        if self.client.is_error_exist:
            self.is_error_exist = True
            self.error_message = self.client.error_message
            await self.client.close()
            return None
        suite = await self.retrieve_suite()
        await self.refresh_home()
        if self.upload_type == UploadDataType.mysekai:
            mysekai = await self.retrieve_mysekai()
        else:
            mysekai = None
        await self.client.close()
        await self.logger.stop()
        return SekaiInheritDataRetrieverResponse(
            server=str(self.client.server),
            user_id=self.client.user_id,
            suite=suite,
            mysekai=mysekai,
            policy=self.policy,
        )

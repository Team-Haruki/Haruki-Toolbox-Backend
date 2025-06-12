from enum import Enum


class UploadDataType(str, Enum):
    suite = "suite"
    mysekai = "mysekai"

    def __str__(self):
        return self.value


class SupportedSuiteUploadServer(str, Enum):
    jp = "jp"
    en = "en"
    tw = "tw"
    kr = "kr"
    cn = "cn"

    def __str__(self):
        return self.value


class SupportedMysekaiUploadServer(str, Enum):
    jp = "jp"

    def __str__(self):
        return self.value


class SupportedInheritUploadServer(str, Enum):
    jp = "jp"
    en = "en"

    def __str__(self):
        return self.value


class UploadPolicy(str, Enum):
    public = "public"
    private = "private"

    def __str__(self):
        return self.value

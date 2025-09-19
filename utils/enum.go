package utils

type UploadDataType string

const (
	UploadDataTypeSuite   UploadDataType = "suite"
	UploadDataTypeMysekai UploadDataType = "mysekai"
)

type SupportedDataUploadServer string

const (
	SupportedSuiteUploadServerJP SupportedDataUploadServer = "jp"
	SupportedSuiteUploadServerEN SupportedDataUploadServer = "en"
	SupportedSuiteUploadServerTW SupportedDataUploadServer = "tw"
	SupportedSuiteUploadServerKR SupportedDataUploadServer = "kr"
	SupportedSuiteUploadServerCN SupportedDataUploadServer = "cn"
)

type SupportedInheritUploadServer string

const (
	SupportedInheritUploadServerJP SupportedInheritUploadServer = "jp"
	SupportedInheritUploadServerEN SupportedInheritUploadServer = "en"
)

type UploadPolicy string

const (
	UploadPolicyPublic  UploadPolicy = "public"
	UploadPolicyPrivate UploadPolicy = "private"
)

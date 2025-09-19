package utils

type UploadDataType string

const (
	UploadDataTypeSuite   UploadDataType = "suite"
	UploadDataTypeMysekai UploadDataType = "mysekai"
)

type SupportedDataUploadServer string

const (
	SupportedDataUploadServerJP SupportedDataUploadServer = "jp"
	SupportedDataUploadServerEN SupportedDataUploadServer = "en"
	SupportedDataUploadServerTW SupportedDataUploadServer = "tw"
	SupportedDataUploadServerKR SupportedDataUploadServer = "kr"
	SupportedDataUploadServerCN SupportedDataUploadServer = "cn"
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

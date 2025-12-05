package utils

import "fmt"

type UploadDataType string

const (
	UploadDataTypeSuite                UploadDataType = "suite"
	UploadDataTypeMysekai              UploadDataType = "mysekai"
	UploadDataTypeMysekaiBirthdayParty UploadDataType = "mysekai_birthday_party"
)

func ParseUploadDataType(s string) (UploadDataType, error) {
	switch UploadDataType(s) {
	case UploadDataTypeSuite, UploadDataTypeMysekai, UploadDataTypeMysekaiBirthdayParty:
		return UploadDataType(s), nil
	default:
		return "", fmt.Errorf("invalid data_type: %s", s)
	}
}

type SupportedDataUploadServer string

const (
	SupportedDataUploadServerJP SupportedDataUploadServer = "jp"
	SupportedDataUploadServerEN SupportedDataUploadServer = "en"
	SupportedDataUploadServerTW SupportedDataUploadServer = "tw"
	SupportedDataUploadServerKR SupportedDataUploadServer = "kr"
	SupportedDataUploadServerCN SupportedDataUploadServer = "cn"
)

func ParseSupportedDataUploadServer(s string) (SupportedDataUploadServer, error) {
	switch SupportedDataUploadServer(s) {
	case SupportedDataUploadServerJP,
		SupportedDataUploadServerEN,
		SupportedDataUploadServerTW,
		SupportedDataUploadServerKR,
		SupportedDataUploadServerCN:
		return SupportedDataUploadServer(s), nil
	default:
		return "", fmt.Errorf("invalid server: %s", s)
	}
}

type SupportedInheritUploadServer string

const (
	SupportedInheritUploadServerJP SupportedInheritUploadServer = "jp"
	SupportedInheritUploadServerEN SupportedInheritUploadServer = "en"
)

func ParseSupportedInheritUploadServer(s string) (SupportedInheritUploadServer, error) {
	switch SupportedInheritUploadServer(s) {
	case SupportedInheritUploadServerJP, SupportedInheritUploadServerEN:
		return SupportedInheritUploadServer(s), nil
	default:
		return "", fmt.Errorf("invalid server: %s", s)
	}
}

package utils

import (
	"fmt"
	"time"
)

type UploadDataType string

const (
	UploadDataTypeSuite                UploadDataType = "suite"
	UploadDataTypeMysekai              UploadDataType = "mysekai"
	UploadDataTypeMysekaiBirthdayParty UploadDataType = "mysekai_birthday_party"
)

func ParseUploadDataType(s string) (UploadDataType, error) {
	switch UploadDataType(s) {
	case UploadDataTypeSuite:
		return UploadDataTypeSuite, nil
	case UploadDataTypeMysekai:
		return UploadDataTypeMysekai, nil
	case UploadDataTypeMysekaiBirthdayParty:
		return UploadDataTypeMysekaiBirthdayParty, nil
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
	case SupportedDataUploadServerJP:
		return SupportedDataUploadServerJP, nil
	case SupportedDataUploadServerEN:
		return SupportedDataUploadServerEN, nil
	case SupportedDataUploadServerTW:
		return SupportedDataUploadServerTW, nil
	case SupportedDataUploadServerKR:
		return SupportedDataUploadServerKR, nil
	case SupportedDataUploadServerCN:
		return SupportedDataUploadServerCN, nil
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
	case SupportedInheritUploadServerJP:
		return SupportedInheritUploadServerJP, nil
	case SupportedInheritUploadServerEN:
		return SupportedInheritUploadServerEN, nil
	default:
		return "", fmt.Errorf("invalid server: %s", s)
	}
}

type UploadMethod string

const (
	UploadMethodManual      UploadMethod = "manual"
	UploadMethodIOSProxy    UploadMethod = "ios_proxy"
	UploadMethodIOSScript   UploadMethod = "ios_script"
	UploadMethodHarukiProxy UploadMethod = "haruki_proxy"
	UploadMethodInherit     UploadMethod = "inherit"
)

const (
	SessionTTL       = 7 * 24 * time.Hour
	OTPTTL           = 5 * time.Minute
	MaxOTPAttempts   = 5
	ResetPasswordTTL = 30 * time.Minute
)

const (
	MaxBodySize = 30 * 1024 * 1024
)

var AllowedAvatarMIMETypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

const (
	HarukiDataSyncerDataFormatRaw      = "raw"
	HarukiDataSyncerDataFormatJsonZstd = "json-zstd"
)

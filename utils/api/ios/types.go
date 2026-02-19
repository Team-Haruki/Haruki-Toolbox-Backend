package ios

import (
	harukiUtils "haruki-suite/utils"
	"strings"
)

type ProxyApp string

const (
	ProxyAppSurge       ProxyApp = "sgmodule"
	ProxyAppLoon        ProxyApp = "lnplugin"
	ProxyAppQuantumultX ProxyApp = "conf"
	ProxyAppStash       ProxyApp = "stoverride"
)

func ParseProxyApp(ext string) (ProxyApp, bool) {
	switch ext {
	case "sgmodule":
		return ProxyAppSurge, true
	case "lnplugin":
		return ProxyAppLoon, true
	case "conf":
		return ProxyAppQuantumultX, true
	case "stoverride":
		return ProxyAppStash, true
	default:
		return "", false
	}
}

type UploadMode string

const (
	UploadModeProxy  UploadMode = "proxy"
	UploadModeScript UploadMode = "script"
)

type EndpointType string

const (
	EndpointTypeDirect EndpointType = "direct"
	EndpointTypeCDN    EndpointType = "cdn"
)

func ParseEndpointType(s string) (EndpointType, bool) {
	switch s {
	case "direct":
		return EndpointTypeDirect, true
	case "cdn":
		return EndpointTypeCDN, true
	default:
		return "", false
	}
}

type DataType string

const (
	DataTypeSuite                DataType = "suite"
	DataTypeMysekai              DataType = "mysekai"
	DataTypeMysekaiForce         DataType = "mysekai_force"
	DataTypeMysekaiBirthdayParty DataType = "mysekai_birthday_party"
)

func ParseDataType(s string) (DataType, bool) {
	switch s {
	case "suite":
		return DataTypeSuite, true
	case "mysekai":
		return DataTypeMysekai, true
	case "mysekai_force":
		return DataTypeMysekaiForce, true
	case "mysekai_birthday_party":
		return DataTypeMysekaiBirthdayParty, true
	default:
		return "", false
	}
}

type ModuleRequest struct {
	UploadCode  string
	Regions     []harukiUtils.SupportedDataUploadServer
	DataTypes   []DataType
	App         ProxyApp
	Mode        UploadMode
	ChunkSizeMB int
}

func (app ProxyApp) ContentType() string {
	switch app {
	case ProxyAppSurge, ProxyAppLoon:
		return "text/plain; charset=utf-8"
	case ProxyAppQuantumultX:
		return "text/plain; charset=utf-8"
	case ProxyAppStash:
		return "text/yaml; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

func (req *ModuleRequest) FileName() string {
	var result strings.Builder
	for i, r := range req.Regions {
		if i > 0 {
			result.WriteString("-")
		}
		result.WriteString(string(r))
	}
	result.WriteString("-haruki-toolbox-")
	for i, dt := range req.DataTypes {
		if i > 0 {
			result.WriteString("-")
		}
		result.WriteString(string(dt))
	}
	result.WriteString("." + string(req.App))
	return result.String()
}

var regionNames = map[harukiUtils.SupportedDataUploadServer]string{
	harukiUtils.SupportedDataUploadServerJP: "日服",
	harukiUtils.SupportedDataUploadServerEN: "国际服",
	harukiUtils.SupportedDataUploadServerTW: "台服",
	harukiUtils.SupportedDataUploadServerKR: "韩服",
	harukiUtils.SupportedDataUploadServerCN: "国服",
}

var dataTypeNames = map[DataType]string{
	DataTypeSuite:                "Suite",
	DataTypeMysekai:              "MySekai",
	DataTypeMysekaiForce:         "MySekai强制刷新",
	DataTypeMysekaiBirthdayParty: "MySekai生日双叶",
}

package ios

// ProxyApp represents supported proxy applications
type ProxyApp string

const (
	ProxyAppSurge       ProxyApp = "sgmodule"
	ProxyAppLoon        ProxyApp = "lnplugin"
	ProxyAppQuantumultX ProxyApp = "conf"
	ProxyAppStash       ProxyApp = "stoverride"
)

// ParseProxyApp parses file extension to ProxyApp
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

// UploadMode represents how data is uploaded
type UploadMode string

const (
	UploadModeProxy  UploadMode = "proxy"  // 307 redirect
	UploadModeScript UploadMode = "script" // JavaScript upload
)

// EndpointType represents which endpoint to use
type EndpointType string

const (
	EndpointTypeDirect EndpointType = "direct" // Use direct backend URL
	EndpointTypeCDN    EndpointType = "cdn"    // Use CDN URL
)

// ParseEndpointType parses string to EndpointType
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

// DataType represents the type of game data to capture
type DataType string

const (
	DataTypeSuite           DataType = "suite"
	DataTypeMysekai         DataType = "mysekai"
	DataTypeMysekaiForce    DataType = "mysekai_force"
	DataTypeMysekaiBirthday DataType = "mysekai-birthday"
)

// ParseDataType parses string to DataType
func ParseDataType(s string) (DataType, bool) {
	switch s {
	case "suite":
		return DataTypeSuite, true
	case "mysekai":
		return DataTypeMysekai, true
	case "mysekai_force":
		return DataTypeMysekaiForce, true
	case "mysekai-birthday":
		return DataTypeMysekaiBirthday, true
	default:
		return "", false
	}
}

// Region represents game server region
type Region string

const (
	RegionJP Region = "jp"
	RegionEN Region = "en"
	RegionTW Region = "tw"
	RegionKR Region = "kr"
	RegionCN Region = "cn"
)

// ParseRegion parses string to Region
func ParseRegion(s string) (Region, bool) {
	switch s {
	case "jp":
		return RegionJP, true
	case "en":
		return RegionEN, true
	case "tw":
		return RegionTW, true
	case "kr":
		return RegionKR, true
	case "cn":
		return RegionCN, true
	default:
		return "", false
	}
}

// ModuleRequest represents a request to generate an iOS module
type ModuleRequest struct {
	UploadCode  string
	Regions     []Region
	DataTypes   []DataType
	App         ProxyApp
	Mode        UploadMode
	ChunkSizeMB int // Chunk size in MB for JavaScript upload (default: 1)
}

// ContentType returns the HTTP content type for the module
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

// FileName generates the download filename
func (req *ModuleRequest) FileName() string {
	// Format: jp-en-haruki-toolbox-suite-mysekai.sgmodule
	result := ""
	for i, r := range req.Regions {
		if i > 0 {
			result += "-"
		}
		result += string(r)
	}
	result += "-haruki-toolbox-"
	for i, dt := range req.DataTypes {
		if i > 0 {
			result += "-"
		}
		result += string(dt)
	}
	result += "." + string(req.App)
	return result
}

package upload

import (
	"fmt"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"strconv"
	"strings"
)

type uploadContext struct {
	Server               harukiUtils.SupportedDataUploadServer
	DataType             harukiUtils.UploadDataType
	ExpectedGameUserID   int64
	ToolboxUserID        string
	UploadMethod         harukiUtils.UploadMethod
	Settings             harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings
	AllowPublicAPI       bool
	ParsedGameUserID     *int64
	ParsedGameUserIDType string
	FailureStage         string
}

func (uc *uploadContext) expectedGameUserIDString() string {
	if uc == nil {
		return ""
	}
	return strconv.FormatInt(uc.ExpectedGameUserID, 10)
}

func (uc *uploadContext) parsedGameUserIDString() string {
	if uc == nil || uc.ParsedGameUserID == nil {
		return ""
	}
	return strconv.FormatInt(*uc.ParsedGameUserID, 10)
}

func buildUploadContext(
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	gameUserID *int64,
	userID *string,
	uploadMethod harukiUtils.UploadMethod,
) (*uploadContext, error) {
	if gameUserID == nil {
		return nil, fmt.Errorf("missing game user ID")
	}
	if _, err := harukiUtils.ParseSupportedDataUploadServer(string(server)); err != nil {
		return nil, fmt.Errorf("invalid server in HandleUpload: %w", err)
	}
	if _, err := harukiUtils.ParseUploadDataType(string(dataType)); err != nil {
		return nil, fmt.Errorf("invalid data_type in HandleUpload: %w", err)
	}
	toolboxUserID := ""
	if userID != nil {
		toolboxUserID = strings.TrimSpace(*userID)
	}
	return &uploadContext{
		Server:             server,
		DataType:           dataType,
		ExpectedGameUserID: *gameUserID,
		ToolboxUserID:      toolboxUserID,
		UploadMethod:       uploadMethod,
	}, nil
}

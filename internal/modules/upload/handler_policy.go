package upload

import (
	"context"
	"errors"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"strings"
)

var (
	errUploadOwnershipMismatch = errors.New("upload game account ownership mismatch")
	errUploadOwnerBanned       = errors.New("upload game account owner banned")
	errUploadCNMysekaiDenied   = errors.New("upload cn mysekai denied")
)

func ParseGameAccountSetting(ctx context.Context, db *postgresql.Client, server string, gameUserID string, uploadMethod harukiUtils.UploadMethod, userID *string) (bool, *bool, harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings, *bool, *bool, *string, error) {
	var settings harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings
	record, err := db.GameAccountBinding.
		Query().
		Where(
			gameaccountbinding.ServerEQ(server),
			gameaccountbinding.GameUserIDEQ(gameUserID),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			return false, nil, settings, nil, nil, nil, nil
		}
		return false, nil, settings, nil, nil, nil, err
	}
	var belongs *bool
	var allowCNMysekai *bool
	var userBanned *bool
	var banReason *string
	if record.Edges.User != nil {
		ownerID := strings.TrimSpace(record.Edges.User.ID)
		a := record.Edges.User.AllowCnMysekai
		allowCNMysekai = &a
		banned := record.Edges.User.Banned
		userBanned = &banned
		banReason = record.Edges.User.BanReason
		belongs = deriveUploadOwnership(ownerID, userID, uploadMethod)
	}
	settings = harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings{
		Suite:   record.Suite,
		Mysekai: record.Mysekai,
	}
	return true, belongs, settings, allowCNMysekai, userBanned, banReason, nil
}

func validateGameAccountBelonging(belongs *bool) error {
	if belongs != nil && !*belongs {
		return errUploadOwnershipMismatch
	}
	return nil
}

func determinePublicAPIPermission(exists bool, dataType harukiUtils.UploadDataType, settings harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings) bool {
	if !exists {
		return false
	}
	if dataType == harukiUtils.UploadDataTypeMysekai {
		if settings.Mysekai != nil {
			return settings.Mysekai.AllowPublicApi
		}
		return false
	}
	if settings.Suite != nil {
		return settings.Suite.AllowPublicApi
	}
	return false
}

func validateCNMysekaiAccess(dataType harukiUtils.UploadDataType, server harukiUtils.SupportedDataUploadServer, allowCNMySekai *bool) error {
	if server == harukiUtils.SupportedDataUploadServerCN &&
		(dataType == harukiUtils.UploadDataTypeMysekai || dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty) {
		if allowCNMySekai != nil && !*allowCNMySekai {
			return errUploadCNMysekaiDenied
		}
	}
	return nil
}

func deriveUploadOwnership(ownerUserID string, currentUserID *string, uploadMethod harukiUtils.UploadMethod) *bool {
	ownerUserID = strings.TrimSpace(ownerUserID)
	if ownerUserID == "" {
		return nil
	}
	if currentUserID == nil {
		if allowAnonymousBoundAccountUpload(uploadMethod) {
			return nil
		}
		owned := false
		return &owned
	}
	owned := strings.TrimSpace(*currentUserID) == ownerUserID
	return &owned
}

func allowAnonymousBoundAccountUpload(uploadMethod harukiUtils.UploadMethod) bool {
	switch uploadMethod {
	case harukiUtils.UploadMethodIOSProxy, harukiUtils.UploadMethodInherit, harukiUtils.UploadMethodHarukiProxy:
		return true
	default:
		return false
	}
}

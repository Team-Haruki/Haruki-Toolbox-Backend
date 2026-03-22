package adminusers

import adminCoreModule "haruki-suite/internal/modules/admincore"

const (
	roleUser       = adminCoreModule.RoleUser
	roleAdmin      = adminCoreModule.RoleAdmin
	roleSuperAdmin = adminCoreModule.RoleSuperAdmin
)

const (
	defaultAdminUsersPage     = 1
	defaultAdminUsersPageSize = 50
	maxAdminUsersPageSize     = 200
	defaultAdminUsersSort     = "id_desc"
	maxBanReasonLength        = 500

	adminUsersSortIDDesc        = "id_desc"
	adminUsersSortIDAsc         = "id_asc"
	adminUsersSortNameDesc      = "name_desc"
	adminUsersSortNameAsc       = "name_asc"
	adminUsersSortCreatedAtDesc = "created_at_desc"
	adminUsersSortCreatedAtAsc  = "created_at_asc"
)

const maxBatchUserOperationCount = 200

const (
	defaultAdminUserActivitySystemLogLimit = 50
	defaultAdminUserActivityUploadLogLimit = 50
	maxAdminUserActivityItemLimit          = 200
)

const defaultAdminUserDetailActivityWindowHours = 24

const (
	softDeleteBanReasonPrefix   = "[soft_deleted]"
	temporaryPasswordPrefix     = "Tmp-"
	temporaryPasswordBytes      = 12
	adminPasswordMinLengthChars = 8
	adminPasswordMaxLengthBytes = 72
)

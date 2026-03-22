package usercore

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func CurrentUserID(c fiber.Ctx) (string, error) {
	userID, ok := c.Locals("userID").(string)
	userID = strings.TrimSpace(userID)
	if !ok || userID == "" {
		return "", fiber.NewError(fiber.StatusUnauthorized, "user not authenticated")
	}
	return userID, nil
}

func CurrentKratosIdentityID(c fiber.Ctx) (string, error) {
	identityID, ok := c.Locals("identityID").(string)
	identityID = strings.TrimSpace(identityID)
	if !ok || identityID == "" {
		return "", fiber.NewError(fiber.StatusUnauthorized, "kratos identity not authenticated")
	}
	return identityID, nil
}

func CheckUserNotBanned(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		ctx := c.Context()
		user, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Select(userSchema.FieldBanned, userSchema.FieldBanReason).
			Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to validate user status")
		}
		if user.Banned {
			banMessage := "Your account has been banned"
			if user.BanReason != nil && *user.BanReason != "" {
				banMessage = "Your account has been banned: " + *user.BanReason
			}
			return harukiAPIHelper.ErrorForbidden(c, banMessage)
		}
		return c.Next()
	}
}

func RequireSelfUserParam(paramName string) fiber.Handler {
	return func(c fiber.Ctx) error {
		authUserID, err := CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		targetUserID := strings.TrimSpace(c.Params(paramName))
		if targetUserID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, paramName+" is required")
		}
		if targetUserID != authUserID {
			return harukiAPIHelper.ErrorForbidden(c, "you can only access your own resources")
		}

		return c.Next()
	}
}

func IsCurrentUserEmailVerified(c fiber.Ctx) bool {
	switch value := c.Locals("emailVerified").(type) {
	case bool:
		return value
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return err == nil && parsed
	default:
		return false
	}
}

func RequireVerifiedEmail() fiber.Handler {
	return func(c fiber.Ctx) error {
		if IsCurrentUserEmailVerified(c) {
			return c.Next()
		}
		return harukiAPIHelper.ErrorForbidden(c, "email must be verified before binding social platform")
	}
}

func WriteUserAuditLog(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string, result string, targetUserID string, metadata map[string]any) {
	if strings.TrimSpace(targetUserID) == "" {
		if localUserID, ok := c.Locals("userID").(string); ok {
			targetUserID = strings.TrimSpace(localUserID)
		}
	}

	targetType := "user"
	var targetIDPtr *string
	if targetUserID != "" {
		targetID := targetUserID
		targetIDPtr = &targetID
	}

	entry := harukiAPIHelper.BuildSystemLogEntryFromFiber(c, action, result, &targetType, targetIDPtr, metadata)
	if targetUserID != "" && (entry.ActorUserID == nil || strings.TrimSpace(*entry.ActorUserID) == "") {
		entry.ActorUserID = &targetUserID
		role := "user"
		if localRole, ok := c.Locals("userRole").(string); ok {
			trimmedRole := strings.ToLower(strings.TrimSpace(localRole))
			if trimmedRole != "" {
				role = trimmedRole
			}
		}
		entry.ActorRole = &role
		if role == "admin" || role == "super_admin" {
			entry.ActorType = harukiAPIHelper.SystemLogActorTypeAdmin
		} else {
			entry.ActorType = harukiAPIHelper.SystemLogActorTypeUser
		}
	}

	_ = harukiAPIHelper.WriteSystemLog(c.Context(), apiHelper, entry)
}

package userauth

import (
	"context"
	"crypto/rand"
	"fmt"
	userModule "haruki-suite/internal/modules/user"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"math/big"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"

	userSchema "haruki-suite/utils/database/postgresql/user"
)

const (
	registerAuditAction         = "user.register"
	registerAuditTargetTypeUser = "user"
	registerAuditActorRoleUser  = "user"

	registerReasonInvalidPayload                = "invalid_payload"
	registerReasonInvalidChallenge              = "invalid_challenge"
	registerReasonInvalidEmailOTP               = "invalid_email_otp"
	registerReasonEmailUnavailable              = "email_unavailable"
	registerReasonPasswordTooShort              = "password_too_short"
	registerReasonPasswordTooLong               = "password_too_long"
	registerReasonPasswordHashFailed            = "password_hash_failed"
	registerReasonGenerateUIDFailed             = "generate_uid_failed"
	registerReasonStartTransactionFailed        = "start_transaction_failed"
	registerReasonCreateUserFailed              = "create_user_failed"
	registerReasonGenerateUploadCode            = "generate_upload_code_failed"
	registerReasonCreateIOSCodeFailed           = "create_ios_code_failed"
	registerReasonCommitTransactionFailed       = "commit_transaction_failed"
	registerReasonIssueSessionFailed            = "issue_session_failed"
	registerReasonOK                            = "ok"
	registerUIDGenerateMaxAttempts              = 3
	registerUIDTimestampSuffixModulo            = 10000
	registerUIDRandomRangeExclusive       int64 = 1000000
	registerUIDFormat                           = "%04d%06d"
	registerOTPAttemptLimit                     = 5
	registerOTPAttemptTTL                       = 5 * time.Minute
)

type registerCreateUserFailureDecision int

const (
	registerCreateUserFailureDecisionFail registerCreateUserFailureDecision = iota
	registerCreateUserFailureDecisionRetryUID
	registerCreateUserFailureDecisionEmailConflict
)

var registerRandInt = rand.Int

func handleRegister(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		logRegister := func(result string, targetUserID string, reason string) {
			targetType := registerAuditTargetTypeUser
			var targetIDPtr *string
			if targetUserID != "" {
				targetID := targetUserID
				targetIDPtr = &targetID
			}
			entry := harukiAPIHelper.BuildSystemLogEntryFromFiber(c, registerAuditAction, result, &targetType, targetIDPtr, map[string]any{
				"reason": reason,
			})
			if targetUserID != "" {
				entry.ActorUserID = &targetUserID
				role := registerAuditActorRoleUser
				entry.ActorRole = &role
				entry.ActorType = harukiAPIHelper.SystemLogActorTypeUser
			}
			_ = harukiAPIHelper.WriteSystemLog(ctx, apiHelper, entry)
		}

		var req harukiAPIHelper.RegisterPayload
		if err := c.Bind().Body(&req); err != nil {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonInvalidPayload)
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		req.Email = platformIdentity.NormalizeEmail(req.Email)
		if req.Email == "" {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonEmailUnavailable)
			return harukiAPIHelper.ErrorBadRequest(c, "email is required")
		}
		vresp, err := cloudflare.ValidateTurnstile(req.ChallengeToken, c.IP())
		if err != nil || vresp == nil || !vresp.Success {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonInvalidChallenge)
			return harukiAPIHelper.ErrorBadRequest(c, "invalid challenge token")
		}
		verified, err := verifyEmailOTP(c, apiHelper, req.Email, req.OneTimePassword)
		if err != nil {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonInvalidEmailOTP)
			return harukiAPIHelper.ErrorInternal(c, "redis error")
		}
		if !verified {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonInvalidEmailOTP)
			return harukiAPIHelper.ErrorBadRequest(c, "invalid or expired verification code")
		}
		if err := checkEmailAvailability(c, apiHelper, req.Email); err != nil {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonEmailUnavailable)
			return err
		}
		if userModule.IsPasswordTooShort(req.Password) {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonPasswordTooShort)
			return harukiAPIHelper.ErrorBadRequest(c, userModule.PasswordTooShortMessage)
		}
		if userModule.IsPasswordTooLong(req.Password) {
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonPasswordTooLong)
			return harukiAPIHelper.ErrorBadRequest(c, userModule.PasswordTooLongMessage)
		}
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() {
			return handleRegisterViaKratos(c, apiHelper, req, logRegister)
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			harukiLogger.Errorf("Failed to hash password: %v", err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonPasswordHashFailed)
			return harukiAPIHelper.ErrorInternal(c, "failed to hash password")
		}

		uploadCode, err := userModule.GenerateUploadCode()
		if err != nil {
			harukiLogger.Errorf("Failed to generate upload code: %v", err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonGenerateUploadCode)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate upload code")
		}

		var uid string
		var newUser *postgresql.User
		var tx *postgresql.Tx
		for attempt := range registerUIDGenerateMaxAttempts {
			tx, err = apiHelper.DBManager.DB.Tx(ctx)
			if err != nil {
				harukiLogger.Errorf("Failed to start register transaction: %v", err)
				logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonStartTransactionFailed)
				return harukiAPIHelper.ErrorInternal(c, "failed to create user")
			}

			uid, err = generateRegisterUID(time.Now())
			if err != nil {
				_ = tx.Rollback()
				tx = nil
				harukiLogger.Errorf("Failed to generate register uid: %v", err)
				logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonGenerateUIDFailed)
				return harukiAPIHelper.ErrorInternal(c, "failed to create user")
			}
			newUser, err = tx.User.Create().
				SetID(uid).
				SetName(req.Name).
				SetEmail(req.Email).
				SetPasswordHash(string(passwordHash)).
				SetNillableAvatarPath(nil).
				SetCreatedAt(time.Now().UTC()).
				Save(ctx)
			if err == nil {
				break
			}
			_ = tx.Rollback()
			tx = nil

			emailTaken, emailCheckErr := queryRegisterEmailConflict(ctx, apiHelper, req.Email, err)
			if emailCheckErr != nil {
				harukiLogger.Errorf("Failed to query email conflict during register: %v", emailCheckErr)
				logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonCreateUserFailed)
				return harukiAPIHelper.ErrorInternal(c, "failed to create user")
			}

			switch decideRegisterCreateUserFailure(err, emailTaken) {
			case registerCreateUserFailureDecisionRetryUID:
				harukiLogger.Warnf("UID collision on attempt %d, retrying...", attempt+1)
				continue
			case registerCreateUserFailureDecisionEmailConflict:
				logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonEmailUnavailable)
				return harukiAPIHelper.ErrorBadRequest(c, "email already in use")
			default:
				break
			}
		}
		if err != nil {
			if tx != nil {
				_ = tx.Rollback()
			}
			harukiLogger.Errorf("Failed to create user after retries: %v", err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonCreateUserFailed)
			return harukiAPIHelper.ErrorInternal(c, "failed to create user")
		}
		_, err = tx.IOSScriptCode.Create().
			SetUserID(uid).
			SetUploadCode(uploadCode).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			harukiLogger.Errorf("Failed to create iOS script code: %v", err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, uid, registerReasonCreateIOSCodeFailed)
			return harukiAPIHelper.ErrorInternal(c, "failed to create upload code")
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			harukiLogger.Errorf("Failed to commit register transaction: %v", err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, uid, registerReasonCommitTransactionFailed)
			return harukiAPIHelper.ErrorInternal(c, "failed to create user")
		}

		signedToken, err := apiHelper.SessionHandler.IssueSession(uid)
		if err != nil {
			harukiLogger.Errorf("Failed to issue session for user %s: %v", uid, err)
			cleanupRegisteredUserAfterSessionIssueFailure(ctx, apiHelper, uid)
			logRegister(harukiAPIHelper.SystemLogResultFailure, uid, registerReasonIssueSessionFailed)
			return harukiAPIHelper.ErrorInternal(c, "Failed to create session")
		}
		role := string(newUser.Role)
		ud := harukiAPIHelper.HarukiToolboxUserData{
			Name:                        &newUser.Name,
			UserID:                      &uid,
			Role:                        &role,
			AvatarPath:                  nil,
			AllowCNMysekai:              &newUser.AllowCnMysekai,
			IOSUploadCode:               &uploadCode,
			EmailInfo:                   &harukiAPIHelper.EmailInfo{Email: req.Email, Verified: true},
			SocialPlatformInfo:          nil,
			AuthorizeSocialPlatformInfo: nil,
			GameAccountBindings:         nil,
			SessionToken:                &signedToken,
		}
		logRegister(harukiAPIHelper.SystemLogResultSuccess, uid, registerReasonOK)
		resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "register success", UserData: ud}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
	}
}

func verifyEmailOTP(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email, otp string) (bool, error) {
	ctx := c.Context()
	email = platformIdentity.NormalizeEmail(email)
	attemptKey := harukiRedis.BuildOTPAttemptKey(email)
	var attemptCount int
	found, err := apiHelper.DBManager.Redis.GetCache(ctx, attemptKey, &attemptCount)
	if err != nil {
		harukiLogger.Errorf("Failed to get OTP attempt count: %v", err)
		return false, err
	}
	if found && attemptCount >= registerOTPAttemptLimit {
		return false, nil
	}

	redisKey := harukiRedis.BuildEmailVerifyKey(email)
	var storedOTP string
	found, err = apiHelper.DBManager.Redis.GetCache(ctx, redisKey, &storedOTP)
	if err != nil {
		harukiLogger.Errorf("Failed to get OTP from redis: %v", err)
		return false, err
	}
	if !found || storedOTP != otp {
		newCount := attemptCount + 1
		if setErr := apiHelper.DBManager.Redis.SetCache(ctx, attemptKey, newCount, registerOTPAttemptTTL); setErr != nil {
			harukiLogger.Errorf("Failed to update OTP attempt count: %v", setErr)
			return false, setErr
		}
		return false, nil
	}

	consumed, err := apiHelper.DBManager.Redis.DeleteCacheIfValueMatches(ctx, redisKey, storedOTP)
	if err != nil {
		harukiLogger.Errorf("Failed to consume OTP code: %v", err)
		return false, err
	}
	if !consumed {
		return false, nil
	}
	if delErr := apiHelper.DBManager.Redis.DeleteCache(ctx, attemptKey); delErr != nil {
		harukiLogger.Warnf("Failed to clear OTP attempt key for %s: %v", email, delErr)
	}
	return true, nil
}

func checkEmailAvailability(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email string) error {
	ctx := c.Context()
	normalizedEmail := platformIdentity.NormalizeEmail(email)
	userExists, err := apiHelper.DBManager.DB.User.Query().Where(userSchema.EmailEqualFold(normalizedEmail)).Exist(ctx)
	if err != nil {
		harukiLogger.Errorf("Failed to query user: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "database error")
	}
	if userExists {
		return harukiAPIHelper.ErrorBadRequest(c, "email already in use")
	}
	return nil
}

func formatRegisterUID(tsSuffix int64, randNum int64) string {
	return fmt.Sprintf(registerUIDFormat, tsSuffix, randNum)
}

func generateRegisterUID(now time.Time) (string, error) {
	tsSuffix := now.UnixMicro() % registerUIDTimestampSuffixModulo
	randNum, err := registerRandInt(rand.Reader, big.NewInt(registerUIDRandomRangeExclusive))
	if err != nil {
		return "", fmt.Errorf("generate uid random number: %w", err)
	}
	return formatRegisterUID(tsSuffix, randNum.Int64()), nil
}

func cleanupRegisteredUserAfterSessionIssueFailure(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) {
	if strings.TrimSpace(userID) == "" {
		return
	}
	if _, err := apiHelper.DBManager.DB.IOSScriptCode.Delete().
		Where(iosscriptcode.UserIDEQ(userID)).
		Exec(ctx); err != nil && !postgresql.IsNotFound(err) {
		harukiLogger.Errorf("Failed to cleanup iOS script code for user %s: %v", userID, err)
	}
	if err := apiHelper.DBManager.DB.User.DeleteOneID(userID).Exec(ctx); err != nil && !postgresql.IsNotFound(err) {
		harukiLogger.Errorf("Failed to cleanup user %s after session issue failure: %v", userID, err)
	}
}

func queryRegisterEmailConflict(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email string, createErr error) (bool, error) {
	if !postgresql.IsConstraintError(createErr) {
		return false, nil
	}
	normalizedEmail := platformIdentity.NormalizeEmail(email)
	return apiHelper.DBManager.DB.User.Query().Where(userSchema.EmailEqualFold(normalizedEmail)).Exist(ctx)
}

func decideRegisterCreateUserFailure(createErr error, emailTaken bool) registerCreateUserFailureDecision {
	if !postgresql.IsConstraintError(createErr) {
		return registerCreateUserFailureDecisionFail
	}
	if emailTaken {
		return registerCreateUserFailureDecisionEmailConflict
	}
	return registerCreateUserFailureDecisionRetryUID
}

func handleRegisterViaKratos(
	c fiber.Ctx,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	req harukiAPIHelper.RegisterPayload,
	logRegister func(result string, targetUserID string, reason string),
) error {
	ctx := c.Context()
	traits := map[string]any{}
	if name := strings.TrimSpace(req.Name); name != "" {
		traits["name"] = name
	}

	sessionToken, err := apiHelper.SessionHandler.RegisterWithKratosPassword(ctx, req.Email, req.Password, traits)
	if err != nil {
		switch {
		case harukiAPIHelper.IsKratosIdentityConflictError(err):
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonEmailUnavailable)
			return harukiAPIHelper.ErrorBadRequest(c, "email already in use")
		case harukiAPIHelper.IsKratosInvalidInputError(err), harukiAPIHelper.IsKratosInvalidCredentialsError(err):
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonInvalidPayload)
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		case harukiAPIHelper.IsIdentityProviderUnavailableError(err):
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", "identity_provider_unavailable")
			return harukiAPIHelper.ErrorInternal(c, "failed to create user")
		default:
			harukiLogger.Errorf("Kratos registration failed for email %s: %v", req.Email, err)
			logRegister(harukiAPIHelper.SystemLogResultFailure, "", registerReasonCreateUserFailed)
			return harukiAPIHelper.ErrorInternal(c, "failed to create user")
		}
	}

	userID, err := apiHelper.SessionHandler.ResolveUserIDFromKratosSession(ctx, sessionToken, "")
	if err != nil {
		harukiLogger.Errorf("Kratos registration succeeded but resolve identity failed for email %s: %v", req.Email, err)
		logRegister(harukiAPIHelper.SystemLogResultFailure, "", "resolve_identity_failed")
		return harukiAPIHelper.ErrorInternal(c, "failed to create user")
	}

	if name := strings.TrimSpace(req.Name); name != "" {
		if _, err := apiHelper.DBManager.DB.User.Update().
			Where(userSchema.IDEQ(userID)).
			SetName(name).
			Save(ctx); err != nil {
			harukiLogger.Warnf("Failed to update provisioned user name for %s: %v", userID, err)
		}
	}

	newUser, err := apiHelper.DBManager.DB.User.
		Query().
		Where(userSchema.IDEQ(userID)).
		WithSocialPlatformInfo().
		WithAuthorizedSocialPlatforms().
		WithGameAccountBindings().
		WithIosScriptCode().
		Only(ctx)
	if err != nil {
		harukiLogger.Errorf("Failed to load provisioned user %s after Kratos registration: %v", userID, err)
		logRegister(harukiAPIHelper.SystemLogResultFailure, userID, registerReasonCreateUserFailed)
		return harukiAPIHelper.ErrorInternal(c, "failed to create user")
	}

	role := string(newUser.Role)
	ud := harukiAPIHelper.BuildUserDataFromDBUser(newUser, &sessionToken)
	if ud.Role == nil {
		ud.Role = &role
	}
	logRegister(harukiAPIHelper.SystemLogResultSuccess, userID, registerReasonOK)
	resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "register success", UserData: ud}
	return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
}

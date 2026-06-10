package userprivateapi

import (
	harukiApiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/authorizesocialplatforminfo"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"sync"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/sync/errgroup"
)

func handleGetGameBindings(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		platform := c.Query("platform")
		platformUserID := c.Query("platform_user_id")
		if platform == "" || platformUserID == "" {
			return harukiApiHelper.ErrorBadRequest(c, "both platform and platform_user_id are required")
		}
		var (
			mu          sync.Mutex
			authEntries []*postgresql.AuthorizeSocialPlatformInfo
			directUser  *postgresql.User
		)
		g, gCtx := errgroup.WithContext(ctx)
		g.Go(func() error {
			entries, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
				Where(
					authorizesocialplatforminfo.PlatformEQ(platform),
					authorizesocialplatforminfo.PlatformUserIDEQ(platformUserID),
					authorizesocialplatforminfo.AllowFastVerificationEQ(true),
				).
				WithUser(func(query *postgresql.UserQuery) {
					query.WithGameAccountBindings(func(bQuery *postgresql.GameAccountBindingQuery) {
						bQuery.Where(gameaccountbinding.VerifiedEQ(true))
					})
				}).
				All(gCtx)
			if err != nil {
				return err
			}
			mu.Lock()
			authEntries = entries
			mu.Unlock()
			return nil
		})
		g.Go(func() error {
			directInfo, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
				Where(
					socialplatforminfo.PlatformEQ(platform),
					socialplatforminfo.PlatformUserIDEQ(platformUserID),
				).
				WithUser(func(query *postgresql.UserQuery) {
					query.WithGameAccountBindings(func(bQuery *postgresql.GameAccountBindingQuery) {
						bQuery.Where(gameaccountbinding.VerifiedEQ(true))
					})
				}).
				Only(gCtx)
			if err == nil && directInfo != nil {
				mu.Lock()
				directUser = directInfo.Edges.User
				mu.Unlock()
			} else if !postgresql.IsNotFound(err) {
				return err
			}
			return nil
		})

		if err := g.Wait(); err != nil {
			harukiLogger.Errorf("Failed to query social platforms concurrently (platform=%s,platform_user_id=%s): %v", platform, platformUserID, err)
			return harukiApiHelper.ErrorInternal(c, "failed to query social platforms")
		}

		if len(authEntries) == 0 && directUser == nil {
			return c.JSON([]any{})
		}
		type bindingEntry struct {
			Server     string `json:"server"`
			GameUserID string `json:"gameUserId"`
		}
		type bindingKey struct {
			Server     string
			GameUserID string
		}
		seen := make(map[bindingKey]struct{})
		var result []bindingEntry
		processUserBindings := func(u *postgresql.User) error {
			if u == nil {
				return nil
			}
			if u.Banned {
				return harukiApiHelper.ErrorForbidden(c, "forbidden: account owner is banned")
			}
			for _, b := range u.Edges.GameAccountBindings {
				key := bindingKey{Server: b.Server, GameUserID: b.GameUserID}
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				result = append(result, bindingEntry{
					Server:     b.Server,
					GameUserID: b.GameUserID,
				})
			}
			return nil
		}

		if directUser != nil {
			if err := processUserBindings(directUser); err != nil {
				return err
			}
		}

		for _, entry := range authEntries {
			if err := processUserBindings(entry.Edges.User); err != nil {
				return err
			}
		}

		if result == nil {
			result = []bindingEntry{}
		}
		return c.JSON(result)
	}
}

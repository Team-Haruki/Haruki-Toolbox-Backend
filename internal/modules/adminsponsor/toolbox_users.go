package adminsponsor

import (
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/gofiber/fiber/v3"
)

const toolboxSponsorAction = "admin.sponsor.toolbox_access.update"

type toolboxSponsorItem struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Banned bool   `json:"banned"`
	sponsor.ToolboxAccess
	Note      string     `json:"note"`
	UpdatedBy string     `json:"updatedBy"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

type toolboxSponsorList struct {
	Items    []toolboxSponsorItem `json:"items"`
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"pageSize"`
}

func toolboxSponsorView(u *postgresql.User, access sponsor.ToolboxAccess) toolboxSponsorItem {
	return toolboxSponsorItem{UserID: u.ID, Name: u.Name, Email: u.Email, Banned: u.Banned, ToolboxAccess: access,
		Note: stringPtrValue(u.SponsorAccessNote), UpdatedBy: stringPtrValue(u.SponsorAccessUpdatedBy), UpdatedAt: u.SponsorAccessUpdatedAt}
}

func ensureCanManageToolboxSponsor(actorID, actorRole, targetID, targetRole string) error {
	// Sponsorship changes only cosmetic access. A super admin may manage their
	// own sponsorship; the shared account/role management guard stays unchanged.
	if actorID == targetID && admincore.NormalizeRole(actorRole) == admincore.RoleSuperAdmin && admincore.NormalizeRole(targetRole) == admincore.RoleSuperAdmin {
		return nil
	}
	return admincore.EnsureAdminCanManageTargetUser(actorID, actorRole, targetID, targetRole)
}

func handleAdminListToolboxSponsors(helper *api.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorID, role, err := admincore.CurrentAdminActor(c)
		if err != nil {
			return admincore.RespondFiberOrUnauthorized(c, err, "missing admin session")
		}
		page, size, err := pagination.ParsePageAndPageSize(c, 1, 20, 100)
		if err != nil {
			return admincore.RespondFiberOrBadRequest(c, err, "invalid pagination")
		}
		if page > 1000000 {
			return api.ErrorBadRequest(c, "page exceeds max allowed value")
		}
		keyword, scope := strings.TrimSpace(c.Query("q")), c.Query("scope", "certified")
		if len(keyword) > 128 || (scope != "certified" && scope != "all") || (scope == "all" && keyword == "") {
			return api.ErrorBadRequest(c, "invalid search; all-user searches require a keyword")
		}
		db := helper.DBManager.DB
		query := db.User.Query()
		if role != admincore.RoleSuperAdmin {
			query.Where(user.IDNEQ(actorID), user.RoleNEQ(user.RoleSuperAdmin))
		}
		if scope == "certified" {
			query.Where(user.Or(user.SponsorAccessOverrideNotNil(), func(s *sql.Selector) {
				t := sql.Table(sponsorSchema.Table)
				s.Where(sql.In(s.C(user.FieldAfdianUserID), sql.Select(t.C(sponsorSchema.FieldAfdianUserID)).From(t)))
			}))
		}
		if keyword != "" {
			query.Where(user.Or(user.IDContainsFold(keyword), user.NameContainsFold(keyword), user.EmailContainsFold(keyword), user.AfdianUserIDContainsFold(keyword)))
		}
		total, err := query.Clone().Count(c.Context())
		if err != nil {
			return api.ErrorInternal(c, "failed to count sponsor users")
		}
		users, err := query.Order(user.ByID()).Offset((page - 1) * size).Limit(size).All(c.Context())
		if err != nil {
			return api.ErrorInternal(c, "failed to query sponsor users")
		}
		ids := make([]string, 0, len(users))
		for _, u := range users {
			if u.AfdianUserID != nil {
				ids = append(ids, *u.AfdianUserID)
			}
		}
		var rows []*postgresql.Sponsor
		if len(ids) > 0 {
			rows, err = db.Sponsor.Query().Where(sponsorSchema.AfdianUserIDIn(ids...)).All(c.Context())
			if err != nil {
				return api.ErrorInternal(c, "failed to query sponsorships")
			}
		}
		result := toolboxSponsorList{Items: make([]toolboxSponsorItem, 0, len(users)), Total: total, Page: page, PageSize: size}
		for _, u := range users {
			if err := ensureCanManageToolboxSponsor(actorID, role, u.ID, string(u.Role)); err != nil {
				return admincore.RespondFiberOrForbidden(c, err, "cannot manage target user")
			}
			result.Items = append(result.Items, toolboxSponsorView(u, sponsor.ResolveToolboxAccess(u, rows, time.Now().UTC())))
		}
		admincore.WriteAdminAuditLog(c, helper, "admin.sponsor.toolbox_access.list", "user", "all", api.SystemLogResultSuccess, map[string]any{"total": total, "scope": scope})
		return api.Responses.SuccessResponse(c, "success", &result)
	}
}

func handleAdminUpdateToolboxSponsor(helper *api.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorID, role, err := admincore.CurrentAdminActor(c)
		if err != nil {
			return admincore.RespondFiberOrUnauthorized(c, err, "missing admin session")
		}
		id := c.Params("toolbox_user_id")
		fail := func(reason string) {
			admincore.WriteAdminAuditLog(c, helper, toolboxSponsorAction, "user", id, api.SystemLogResultFailure, map[string]any{"reason": reason})
		}
		var payload struct {
			Access string `json:"access"`
			Note   string `json:"note"`
		}
		if c.Bind().Body(&payload) != nil {
			fail("invalid_payload")
			return api.ErrorBadRequest(c, "invalid request payload")
		}
		payload.Note = strings.TrimSpace(payload.Note)
		if (payload.Access != "granted" && payload.Access != "blocked" && payload.Access != "automatic") || payload.Note == "" || len(payload.Note) > 1000 {
			fail("invalid_access_or_note")
			return api.ErrorBadRequest(c, "valid access and a note of at most 1000 bytes are required")
		}
		db := helper.DBManager.DB
		u, err := db.User.Get(c.Context(), id)
		if postgresql.IsNotFound(err) {
			fail("user_not_found")
			return api.ErrorNotFound(c, "user not found")
		}
		if err != nil {
			fail("query_failed")
			return api.ErrorInternal(c, "failed to load user")
		}
		if err := ensureCanManageToolboxSponsor(actorID, role, u.ID, string(u.Role)); err != nil {
			fail("target_not_manageable")
			return admincore.RespondFiberOrForbidden(c, err, "cannot manage target user")
		}
		if u.Banned && payload.Access == "granted" {
			fail("target_banned")
			return fiber.NewError(fiber.StatusConflict, "cannot grant sponsorship to a banned user")
		}
		update := db.User.Update().Where(user.IDEQ(id)).SetSponsorAccessNote(payload.Note).SetSponsorAccessUpdatedBy(actorID).SetSponsorAccessUpdatedAt(time.Now().UTC())
		// Repeat permission predicates in the write to avoid a concurrent role or ban change.
		if actorID == id {
			update.Where(user.RoleEQ(user.RoleSuperAdmin))
		}
		if role != admincore.RoleSuperAdmin {
			update.Where(user.RoleNEQ(user.RoleSuperAdmin))
		}
		if payload.Access == "granted" {
			update.Where(user.BannedEQ(false))
		}
		if payload.Access == "automatic" {
			update.ClearSponsorAccessOverride()
		} else {
			update.SetSponsorAccessOverride(user.SponsorAccessOverride(payload.Access))
		}
		n, err := update.Save(c.Context())
		if err != nil {
			fail("save_failed")
			return api.ErrorInternal(c, "failed to save sponsor access")
		}
		if n != 1 {
			fail("target_changed")
			return fiber.NewError(fiber.StatusConflict, "target user changed; refresh and retry")
		}
		admincore.WriteAdminAuditLog(c, helper, toolboxSponsorAction, "user", id, api.SystemLogResultSuccess, map[string]any{"access": payload.Access, "previousAccess": u.SponsorAccessOverride, "note": payload.Note})
		return api.Responses.SuccessResponse[string](c, "sponsor access updated", nil)
	}
}

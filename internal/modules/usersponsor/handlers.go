package usersponsor

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/gofiber/fiber/v3"
)

type handlers struct {
	api    *api.HarukiToolboxRouterHelpers
	oauth  OAuthConfig
	client *http.Client
}

type sponsorStatus struct {
	OAuthEnabled bool       `json:"oauthEnabled"`
	AfdianUserID *string    `json:"afdianUserId"`
	BoundAt      *time.Time `json:"boundAt"`
	Eligible     bool       `json:"eligible"`
	Category     string     `json:"category"`
	ExpiresAt    *time.Time `json:"expiresAt"`
}

func (h *handlers) statusFor(ctx context.Context, u *postgresql.User) (sponsorStatus, error) {
	s := sponsorStatus{OAuthEnabled: h.oauth.Enabled(), AfdianUserID: u.AfdianUserID, BoundAt: u.AfdianBoundAt, Category: "none"}
	if u.Banned || u.AfdianUserID == nil {
		return s, nil
	}
	rows, err := h.api.DBManager.DB.Sponsor.Query().Where(sponsorSchema.AfdianUserIDEQ(*u.AfdianUserID)).All(ctx)
	if err != nil {
		return s, err
	}
	// A previously sponsored account remains eligible after expiry. The
	// sponsor module remains the only authority for current/former status.
	s.Eligible = len(rows) > 0
	for _, row := range rows {
		category := string(sponsor.SponsorCategory(row, time.Now().UTC()))
		if s.Category == "none" || category == string(sponsor.CategoryCurrent) {
			s.Category = category
		}
		if row.PlanExpiresAt != nil && (s.ExpiresAt == nil || row.PlanExpiresAt.After(*s.ExpiresAt)) {
			s.ExpiresAt = row.PlanExpiresAt
		}
	}
	return s, nil
}

func (h *handlers) currentStatus(c fiber.Ctx) (sponsorStatus, error) {
	uid, err := usercore.CurrentUserID(c)
	if err != nil {
		return sponsorStatus{}, err
	}
	u, err := h.api.DBManager.DB.User.Get(c.Context(), uid)
	if err != nil {
		return sponsorStatus{}, err
	}
	return h.statusFor(c.Context(), u)
}

func (h *handlers) status(c fiber.Ctx) error {
	s, err := h.currentStatus(c)
	if err != nil {
		return api.ErrorInternal(c, "failed to load sponsor status")
	}
	return api.Responses.SuccessResponse(c, "success", &s)
}

type frameAccount struct {
	Server     string  `json:"server"`
	GameUserID string  `json:"gameUserId"`
	Verified   bool    `json:"verified"`
	FrameID    *string `json:"frameId"`
}

func (h *handlers) frames(c fiber.Ctx) error {
	s, err := h.currentStatus(c)
	if err != nil {
		return api.ErrorInternal(c, "failed to load sponsor status")
	}
	uid, _ := usercore.CurrentUserID(c)
	bindings, err := h.api.DBManager.DB.GameAccountBinding.Query().Where(gameaccountbinding.HasUserWith(user.IDEQ(uid))).All(c.Context())
	if err != nil {
		return api.ErrorInternal(c, "failed to load game accounts")
	}
	accounts := make([]frameAccount, 0, len(bindings))
	for _, b := range bindings {
		accounts = append(accounts, frameAccount{b.Server, b.GameUserID, b.Verified, b.SponsorFrameID})
	}
	return api.Responses.SuccessResponse(c, "success", &struct {
		Status   sponsorStatus  `json:"status"`
		Frames   []Frame        `json:"frames"`
		Accounts []frameAccount `json:"accounts"`
	}{s, frameCatalog(), accounts})
}

func (h *handlers) saveFrame(c fiber.Ctx) error {
	var payload struct {
		FrameID *string `json:"frameId"`
	}
	if c.Bind().Body(&payload) != nil || payload.FrameID == nil {
		return api.ErrorBadRequest(c, "frameId is required; use an empty string to restore the game frame")
	}
	id := strings.TrimSpace(*payload.FrameID)
	if id != "" && findFrame(id) == nil {
		return api.ErrorBadRequest(c, "unknown frame")
	}
	uid, _ := usercore.CurrentUserID(c)
	ctx := c.Context()
	// Scope the object before checking eligibility. An unrelated or unverified
	// binding has the same response, and can never be decorated by the caller.
	b, err := h.api.DBManager.DB.GameAccountBinding.Query().Where(
		gameaccountbinding.ServerEQ(c.Params("server")), gameaccountbinding.GameUserIDEQ(c.Params("game_user_id")),
		gameaccountbinding.HasUserWith(user.IDEQ(uid)), gameaccountbinding.VerifiedEQ(true)).Only(ctx)
	if postgresql.IsNotFound(err) {
		return api.ErrorNotFound(c, "verified game account not found")
	}
	if err != nil {
		return api.ErrorInternal(c, "failed to load game account")
	}
	if id != "" {
		s, err := h.currentStatus(c)
		if err != nil {
			return api.ErrorInternal(c, "failed to load sponsor status")
		}
		if !s.Eligible {
			return api.ErrorForbidden(c, "a linked sponsor account is required")
		}
	}
	update := h.api.DBManager.DB.GameAccountBinding.Update().Where(gameaccountbinding.IDEQ(b.ID),
		gameaccountbinding.HasUserWith(user.IDEQ(uid)), gameaccountbinding.VerifiedEQ(true))
	if id == "" {
		update.ClearSponsorFrameID()
	} else {
		update.SetSponsorFrameID(id)
	}
	n, err := update.Save(ctx)
	if err != nil {
		return api.ErrorInternal(c, "failed to save frame")
	}
	if n != 1 {
		return api.ErrorNotFound(c, "verified game account not found")
	}
	usercore.WriteUserAuditLog(c, h.api, "user.sponsor_frame.update", api.SystemLogResultSuccess, uid, map[string]any{"server": b.Server, "gameUserId": b.GameUserID, "frameId": id})
	return h.frames(c)
}

// The private bot API returns decoration only. It discloses neither the
// Toolbox owner nor Afdian identity, sponsorship amount or expiry.
func (h *handlers) gameFrame(c fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancel()
	b, err := h.api.DBManager.DB.GameAccountBinding.Query().Where(
		gameaccountbinding.ServerEQ(c.Params("server")), gameaccountbinding.GameUserIDEQ(c.Params("game_user_id")),
		gameaccountbinding.VerifiedEQ(true), gameaccountbinding.SponsorFrameIDNotNil(),
		gameaccountbinding.HasUserWith(user.BannedEQ(false))).WithUser().Only(ctx)
	var frame *Frame
	if err != nil && !postgresql.IsNotFound(err) {
		return api.ErrorInternal(c, "failed to load frame")
	}
	if err == nil && b.Edges.User != nil {
		s, err := h.statusFor(ctx, b.Edges.User)
		if err != nil {
			return api.ErrorInternal(c, "failed to load frame")
		}
		if s.Eligible && b.SponsorFrameID != nil {
			frame = findFrame(*b.SponsorFrameID)
		}
	}
	type selectedFrame struct {
		ID         string     `json:"id"`
		Horizontal FrameParts `json:"horizontal"`
		Vertical   FrameParts `json:"vertical"`
	}
	var selection *selectedFrame
	if frame != nil {
		selection = &selectedFrame{frame.ID, frame.Horizontal, frame.Vertical}
	}
	return c.JSON(struct {
		Frame *selectedFrame `json:"frame"`
	}{selection})
}

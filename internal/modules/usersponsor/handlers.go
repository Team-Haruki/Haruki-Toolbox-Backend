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
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/gofiber/fiber/v3"
)

type handlers struct {
	catalog *Catalog
	api     *api.HarukiToolboxRouterHelpers
	oauth   OAuthConfig
	orders  sponsor.AfdianConfig
	client  *http.Client
}

type sponsorStatus struct {
	OAuthEnabled             bool `json:"oauthEnabled"`
	OrderVerificationEnabled bool `json:"orderVerificationEnabled"`
	sponsor.ToolboxAccess
}

func (h *handlers) statusFor(ctx context.Context, u *postgresql.User) (sponsorStatus, error) {
	access, err := sponsor.QueryToolboxAccess(ctx, h.api.DBManager.DB, u)
	return sponsorStatus{OAuthEnabled: h.oauth.Enabled(), OrderVerificationEnabled: h.orders.CredentialsConfigured(), ToolboxAccess: access}, err
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
	frames := h.catalog.snapshot()
	accounts := make([]frameAccount, 0, len(bindings))
	for _, b := range bindings {
		frameID := b.SponsorFrameID
		if frameID != nil && lookupFrame(frames, *frameID) == nil {
			frameID = nil
		}
		accounts = append(accounts, frameAccount{b.Server, b.GameUserID, b.Verified, frameID})
	}
	return api.Responses.SuccessResponse(c, "success", &struct {
		Status   sponsorStatus  `json:"status"`
		Frames   []Frame        `json:"frames"`
		Accounts []frameAccount `json:"accounts"`
	}{s, frames, accounts})
}

func (h *handlers) saveFrame(c fiber.Ctx) error {
	var payload struct {
		FrameID *string `json:"frameId"`
	}
	if c.Bind().Body(&payload) != nil || payload.FrameID == nil {
		return api.ErrorBadRequest(c, "frameId is required; use an empty string to restore the game frame")
	}
	id := strings.TrimSpace(*payload.FrameID)
	if id != "" && lookupFrame(h.catalog.snapshot(), id) == nil {
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
			return api.ErrorForbidden(c, "sponsor access is required")
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
			frame = lookupFrame(h.catalog.snapshot(), *b.SponsorFrameID)
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

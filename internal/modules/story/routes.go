package story

import (
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/storytranslation"

	"github.com/gofiber/fiber/v3"
)

// Public routes sit under /api/misc/, which Oathkeeper already forwards
// unauthenticated for GET (rule haruki-public-misc).
const routePrefix = "/api/misc/story"

// TranslationItem is one entry of the translated-episode list.
type TranslationItem struct {
	ScenarioID string    `json:"scenarioId"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// TranslationBody is a finished translation: Lines aligned with TalkData,
// Effects keyed by SpecialEffectData index.
type TranslationBody struct {
	ScenarioID string            `json:"scenarioId"`
	Lang       string            `json:"lang"`
	Model      string            `json:"model"`
	Names      map[string]string `json:"names"`
	Lines      []string          `json:"lines"`
	// Effects maps SpecialEffectData indexes to translated text effects.
	Effects   map[string]string `json:"effects"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// RegisterRoutes mounts the public story API; s may be nil (feature off).
func RegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, s *Service) {
	if s == nil {
		return
	}
	g := apiHelper.Router.Group(routePrefix)
	g.Get("/translations", s.handleList)
	g.Get("/translations/:scenario_id", s.handleTranslation)
}

// RegisterAdminRoutes mounts the admin re-translate action.
func RegisterAdminRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router, s *Service) {
	if s == nil {
		return
	}
	group := adminGroup.Group("/story", adminCoreModule.RequireAdmin(apiHelper))
	group.Post("/translations/:scenario_id/retranslate", s.handleRetranslate)
}

func (s *Service) handleList(c fiber.Ctx) error {
	rows, err := s.db.StoryTranslation.Query().
		Where(storytranslation.Lang(TranslationLang), storytranslation.StatusEQ(storytranslation.StatusDone)).
		Select(storytranslation.FieldScenarioID, storytranslation.FieldUpdatedAt).
		All(c.Context())
	if err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to list translations")
	}
	items := make([]TranslationItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, TranslationItem{ScenarioID: r.ScenarioID, UpdatedAt: r.UpdatedAt})
	}
	c.Set("Cache-Control", "public, max-age=60")
	return c.JSON(fiber.Map{"lang": TranslationLang, "items": items})
}

func (s *Service) handleTranslation(c fiber.Ctx) error {
	id := c.Params("scenario_id")
	if lang := c.Query("lang", TranslationLang); lang != TranslationLang {
		return harukiAPIHelper.ErrorNotFound(c, "unsupported language")
	}
	row, err := s.db.StoryTranslation.Query().
		Where(storytranslation.ScenarioID(id), storytranslation.Lang(TranslationLang), storytranslation.StatusEQ(storytranslation.StatusDone)).
		Only(c.Context())
	if err != nil {
		if !postgresql.IsNotFound(err) {
			return harukiAPIHelper.ErrorInternal(c, "failed to read translation")
		}
		// pending: a JP-only episode the worker will get to
		status := "unavailable"
		if cat := s.currentCatalog(); s.tr != nil && cat != nil && cat.JPOnly(id) {
			status = "pending"
		}
		c.Set("Cache-Control", "public, max-age=60")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"status": status})
	}
	c.Set("Cache-Control", "public, max-age=300")
	return c.JSON(TranslationBody{
		ScenarioID: row.ScenarioID, Lang: row.Lang, Model: row.Model,
		Names: row.Names, Lines: row.Lines, Effects: row.Effects, UpdatedAt: row.UpdatedAt,
	})
}

func (s *Service) handleRetranslate(c fiber.Ctx) error {
	id := c.Params("scenario_id")
	if _, err := s.db.StoryTranslation.Delete().
		Where(storytranslation.ScenarioID(id), storytranslation.Lang(TranslationLang)).
		Exec(c.Context()); err != nil {
		return harukiAPIHelper.ErrorInternal(c, "failed to reset translation")
	}
	s.Kick()
	return harukiAPIHelper.Responses.SuccessResponse[string](c, "queued", nil)
}

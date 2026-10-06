package upload

import (
	"strings"

	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	utils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/gofiber/fiber/v3"
)

func browserUploadAttempt(c fiber.Ctx) *platform.Attempt {
	a := newAttempt(utils.UploadMethodManual, len(c.Body()))
	// Only coarse, known browser platform tokens; never persist the raw UA.
	ua := c.Get("User-Agent")
	a.Client.Platform = "unknown"
	switch {
	case strings.Contains(ua, "Android"):
		a.Client.Platform = "android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		a.Client.Platform = "ios"
	case strings.Contains(ua, "Windows NT"):
		a.Client.Platform = "windows"
	case strings.Contains(ua, "Macintosh"):
		a.Client.Platform = "macos"
	case strings.Contains(ua, "Linux"):
		a.Client.Platform = "linux"
	}
	return a
}
func oauthUploadAttempt(c fiber.Ctx) *platform.Attempt {
	a := newAttempt(utils.UploadMethodOAuth2, len(c.Body()))
	id, _ := c.Locals("oauth2ClientID").(string)
	if len(id) <= 255 {
		a.Client.OAuthClientID = strings.Clone(id)
	}
	return a
}

package userauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type loginRateLimitProbe struct {
	Limited bool   `json:"limited"`
	Key     string `json:"key"`
	Message string `json:"message"`
}

func TestCheckLoginRateLimitByIP(t *testing.T) {
	t.Parallel()

	apiHelper, _ := newRegisterOTPHelper(t)
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		switch c.Query("op") {
		case "check":
			limited, key, message, err := checkLoginRateLimit(c, apiHelper, c.Query("ip"), c.Query("email"))
			if err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			return c.JSON(loginRateLimitProbe{Limited: limited, Key: key, Message: message})
		case "release":
			if err := releaseLoginRateLimitReservation(c, apiHelper, c.Query("ip"), c.Query("email")); err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			return c.SendStatus(fiber.StatusNoContent)
		default:
			return fiber.NewError(fiber.StatusBadRequest, "invalid op")
		}
	})

	for i := 0; i < loginRateLimitIPLimit; i++ {
		email := fmt.Sprintf("ip-probe-%d@example.com", i)
		req := httptest.NewRequest(http.MethodGet, "/?op=check&ip=1.2.3.4&email="+email, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request %d error: %v", i+1, err)
		}
		var probe loginRateLimitProbe
		if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil {
			_ = resp.Body.Close()
			t.Fatalf("decode request %d error: %v", i+1, err)
		}
		_ = resp.Body.Close()
		if probe.Limited {
			t.Fatalf("request %d should not be limited", i+1)
		}

	}

	req := httptest.NewRequest(http.MethodGet, "/?op=check&ip=1.2.3.4&email=ip-probe-limit@example.com", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("limit request error: %v", err)
	}

	var probe loginRateLimitProbe
	if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode limit request error: %v", err)
	}
	_ = resp.Body.Close()
	if !probe.Limited {
		t.Fatalf("request %d should be limited", loginRateLimitIPLimit+1)
	}
	if probe.Key == "" {
		t.Fatalf("limit response key should not be empty")
	}
}

func TestCheckLoginRateLimitByTarget(t *testing.T) {
	t.Parallel()

	apiHelper, _ := newRegisterOTPHelper(t)
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		switch c.Query("op") {
		case "check":
			limited, key, message, err := checkLoginRateLimit(c, apiHelper, c.Query("ip"), c.Query("email"))
			if err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			return c.JSON(loginRateLimitProbe{Limited: limited, Key: key, Message: message})
		case "release":
			if err := releaseLoginRateLimitReservation(c, apiHelper, c.Query("ip"), c.Query("email")); err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			return c.SendStatus(fiber.StatusNoContent)
		default:
			return fiber.NewError(fiber.StatusBadRequest, "invalid op")
		}
	})

	const email = "test@example.com"
	for i := 0; i < loginRateLimitTargetLimit; i++ {
		req := httptest.NewRequest(http.MethodGet, "/?op=check&ip=1.2.3."+strconv.Itoa(10+i)+"&email="+email, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request %d error: %v", i+1, err)
		}
		var probe loginRateLimitProbe
		if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil {
			_ = resp.Body.Close()
			t.Fatalf("decode request %d error: %v", i+1, err)
		}
		_ = resp.Body.Close()
		if probe.Limited {
			t.Fatalf("request %d should not be limited", i+1)
		}

	}

	req := httptest.NewRequest(http.MethodGet, "/?op=check&ip=1.2.3.200&email="+email, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("limit request error: %v", err)
	}

	var probe loginRateLimitProbe
	if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode limit request error: %v", err)
	}
	_ = resp.Body.Close()
	if !probe.Limited {
		t.Fatalf("request %d should be limited", loginRateLimitTargetLimit+1)
	}
	if probe.Key == "" {
		t.Fatalf("limit response key should not be empty")
	}

}

func TestReleaseLoginRateLimitReservation(t *testing.T) {
	t.Parallel()

	apiHelper, _ := newRegisterOTPHelper(t)
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		switch c.Query("op") {
		case "check":
			limited, key, message, err := checkLoginRateLimit(c, apiHelper, c.Query("ip"), c.Query("email"))
			if err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			return c.JSON(loginRateLimitProbe{Limited: limited, Key: key, Message: message})
		case "release":
			if err := releaseLoginRateLimitReservation(c, apiHelper, c.Query("ip"), c.Query("email")); err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			return c.SendStatus(fiber.StatusNoContent)
		default:
			return fiber.NewError(fiber.StatusBadRequest, "invalid op")
		}
	})

	const email = "release@example.com"
	req := httptest.NewRequest(http.MethodGet, "/?op=check&ip=9.9.9.9&email="+email, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("initial check error: %v", err)
	}
	var probe loginRateLimitProbe
	if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode initial check error: %v", err)
	}
	_ = resp.Body.Close()
	if probe.Limited {
		t.Fatalf("initial check should not be limited")
	}

	req = httptest.NewRequest(http.MethodGet, "/?op=release&ip=9.9.9.9&email="+email, nil)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("release request error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("release request status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}

	for i := 0; i < loginRateLimitTargetLimit; i++ {
		req = httptest.NewRequest(http.MethodGet, "/?op=check&ip=9.9.8."+strconv.Itoa(10+i)+"&email="+email, nil)
		resp, err = app.Test(req)
		if err != nil {
			t.Fatalf("loop check %d error: %v", i+1, err)
		}
		if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil {
			_ = resp.Body.Close()
			t.Fatalf("decode loop check %d error: %v", i+1, err)
		}
		_ = resp.Body.Close()
		if probe.Limited {
			t.Fatalf("loop check %d should not be limited", i+1)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/?op=check&ip=9.9.8.250&email="+email, nil)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("limit check error: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode limit check error: %v", err)
	}
	_ = resp.Body.Close()
	if !probe.Limited {
		t.Fatalf("limit check should be limited")
	}
}

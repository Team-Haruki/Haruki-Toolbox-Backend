package userprofile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	json "encoding/json/v2"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiDatabase "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func newProfileTestDB(t *testing.T) *postgresql.Client {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "-"))
	db := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createProfileTestUser(t *testing.T, db *postgresql.Client, id string, avatarPath string, kratosID string) {
	t.Helper()
	create := db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com")
	if avatarPath != "" {
		create = create.SetAvatarPath(avatarPath)
	}
	if kratosID != "" {
		create = create.SetKratosIdentityID(kratosID)
	}
	if _, err := create.Save(context.Background()); err != nil {
		t.Fatalf("create user %s: %v", id, err)
	}
}

type profileTestResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData *struct {
		AvatarPath *string `json:"avatarPath"`
	} `json:"updatedData"`
}

// runAsUser sends body to handler with userID set the way the session
// middleware sets it.
func runAsUser(t *testing.T, handler fiber.Handler, userID string, body string) (int, profileTestResponse) {
	t.Helper()
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		if userID != "" {
			c.Locals("userID", userID)
		}
		return handler(c)
	})
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var decoded profileTestResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return resp.StatusCode, decoded
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pngHeaderWithSize forges a PNG whose IHDR claims w x h; DecodeConfig only
// reads the header, so no large image needs to be built.
func pngHeaderWithSize(t *testing.T, w, h uint32) []byte {
	t.Helper()
	data := encodePNG(t, 1, 1)
	// IHDR: type at 12..16, width/height at 16..24, CRC over 12..29 at 29..33.
	binary.BigEndian.PutUint32(data[16:20], w)
	binary.BigEndian.PutUint32(data[20:24], h)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}

func avatarPayload(data []byte) string {
	return `{"avatarBase64":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(data) + `"}`
}

func TestHandleUpdateProfileReplacesAvatar(t *testing.T) {
	db := newProfileTestDB(t)
	dir := t.TempDir()
	oldAvatar := filepath.Join(dir, "old.png")
	if err := os.WriteFile(oldAvatar, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	createProfileTestUser(t, db, "u-1", "old.png", "")
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &harukiDatabase.HarukiToolboxDBManager{DB: db}}
	cfg := NewConfig(ConfigOptions{AvatarSaveDir: dir, AvatarBaseURL: "https://cdn.example/"})

	status, resp := runAsUser(t, handleUpdateProfile(helper, cfg), "u-1", avatarPayload(encodePNG(t, 2, 2)))
	if status != fiber.StatusOK || resp.Message != "profile updated" {
		t.Fatalf("status = %d, response = %+v", status, resp)
	}
	if resp.UpdatedData == nil || resp.UpdatedData.AvatarPath == nil ||
		!strings.HasPrefix(*resp.UpdatedData.AvatarPath, "https://cdn.example/avatars/") ||
		!strings.HasSuffix(*resp.UpdatedData.AvatarPath, ".png") {
		t.Fatalf("avatar URL = %+v", resp.UpdatedData)
	}
	user, err := db.User.Get(context.Background(), "u-1")
	if err != nil || user.AvatarPath == nil {
		t.Fatalf("user after update: %+v, %v", user, err)
	}
	if !strings.HasSuffix(*resp.UpdatedData.AvatarPath, "/"+*user.AvatarPath) {
		t.Fatalf("stored avatar %q does not match URL %q", *user.AvatarPath, *resp.UpdatedData.AvatarPath)
	}
	if _, err := os.Stat(filepath.Join(dir, *user.AvatarPath)); err != nil {
		t.Fatalf("new avatar not saved: %v", err)
	}
	if _, err := os.Stat(oldAvatar); !os.IsNotExist(err) {
		t.Fatalf("old avatar still present: %v", err)
	}
}

func TestHandleUpdateProfileRejectsBadInput(t *testing.T) {
	db := newProfileTestDB(t)
	createProfileTestUser(t, db, "u-1", "", "")
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &harukiDatabase.HarukiToolboxDBManager{DB: db}}
	dir := t.TempDir()
	cfg := NewConfig(ConfigOptions{AvatarSaveDir: dir})
	handler := handleUpdateProfile(helper, cfg)

	tests := []struct {
		name, userID, body string
		wantStatus         int
		wantMessage        string
	}{
		{"no session", "", `{}`, fiber.StatusUnauthorized, "user not authenticated"},
		{"malformed json", "u-1", `{`, fiber.StatusBadRequest, "Invalid request payload"},
		{"no fields", "u-1", `{}`, fiber.StatusBadRequest, "No profile fields to update"},
		{"unknown user", "u-missing", avatarPayload(encodePNG(t, 1, 1)), fiber.StatusUnauthorized, "invalid user session"},
		{"bad base64", "u-1", `{"avatarBase64":"%%%"}`, fiber.StatusBadRequest, "Invalid base64 avatar data"},
		{"too many bytes", "u-1", avatarPayload(make([]byte, 2*1024*1024+1)), fiber.StatusBadRequest, "Avatar image is too large (max 2MB)"},
		{"not an image", "u-1", avatarPayload([]byte("plain text, not an image")), fiber.StatusBadRequest, "Unsupported image format. Allowed: PNG, JPEG, GIF, WebP"},
		{"corrupt png", "u-1", avatarPayload(append([]byte("\x89PNG\r\n\x1a\n"), 0, 0)), fiber.StatusBadRequest, "Invalid or corrupted image data"},
		{"too many pixels", "u-1", avatarPayload(pngHeaderWithSize(t, 4097, 4096)), fiber.StatusBadRequest, "Avatar image dimensions are too large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := runAsUser(t, handler, tc.userID, tc.body)
			if status != tc.wantStatus || resp.Message != tc.wantMessage {
				t.Fatalf("got %d %q, want %d %q", status, resp.Message, tc.wantStatus, tc.wantMessage)
			}
		})
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected uploads left files behind: %v, %v", entries, err)
	}
	user, err := db.User.Get(context.Background(), "u-1")
	if err != nil || user.AvatarPath != nil {
		t.Fatalf("rejected uploads changed the avatar: %+v, %v", user.AvatarPath, err)
	}
}

func TestHandleUpdateProfileStorageFailures(t *testing.T) {
	png := avatarPayload(encodePNG(t, 1, 1))

	t.Run("no avatar dir", func(t *testing.T) {
		db := newProfileTestDB(t)
		createProfileTestUser(t, db, "u-1", "", "")
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &harukiDatabase.HarukiToolboxDBManager{DB: db}}
		status, resp := runAsUser(t, handleUpdateProfile(helper, NewConfig(ConfigOptions{})), "u-1", png)
		if status != fiber.StatusInternalServerError || resp.Message != "Failed to save avatar" {
			t.Fatalf("got %d %q", status, resp.Message)
		}
	})

	t.Run("avatar dir is a file", func(t *testing.T) {
		db := newProfileTestDB(t)
		createProfileTestUser(t, db, "u-1", "", "")
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &harukiDatabase.HarukiToolboxDBManager{DB: db}}
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		status, resp := runAsUser(t, handleUpdateProfile(helper, NewConfig(ConfigOptions{AvatarSaveDir: file})), "u-1", png)
		if status != fiber.StatusInternalServerError || resp.Message != "Failed to save avatar" {
			t.Fatalf("got %d %q", status, resp.Message)
		}
	})

	t.Run("database closed", func(t *testing.T) {
		db := newProfileTestDB(t)
		createProfileTestUser(t, db, "u-1", "", "")
		_ = db.Close()
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &harukiDatabase.HarukiToolboxDBManager{DB: db}}
		dir := t.TempDir()
		status, resp := runAsUser(t, handleUpdateProfile(helper, NewConfig(ConfigOptions{AvatarSaveDir: dir})), "u-1", png)
		if status != fiber.StatusInternalServerError || resp.Message != "Failed to update profile" {
			t.Fatalf("got %d %q", status, resp.Message)
		}
	})
}

func TestHandleChangePasswordWithoutKratos(t *testing.T) {
	db := newProfileTestDB(t)
	createProfileTestUser(t, db, "u-1", "", "")
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager:      &harukiDatabase.HarukiToolboxDBManager{DB: db},
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, ""),
	}
	handler := handleChangePassword(helper)

	tests := []struct {
		name, userID, body string
		wantStatus         int
		wantMessage        string
	}{
		{"no session", "", `{}`, fiber.StatusUnauthorized, "user not authenticated"},
		{"malformed json", "u-1", `{`, fiber.StatusBadRequest, "Invalid request payload"},
		{"unknown user", "u-missing", `{"oldPassword":"a","newPassword":"b"}`, fiber.StatusUnauthorized, "invalid user session"},
		// Without Kratos there is no local password store to fall back to.
		{"no identity provider", "u-1", `{"oldPassword":"old-password","newPassword":"new-password"}`, fiber.StatusGone, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := runAsUser(t, handler, tc.userID, tc.body)
			if status != tc.wantStatus || (tc.wantMessage != "" && resp.Message != tc.wantMessage) {
				t.Fatalf("got %d %q, want %d %q", status, resp.Message, tc.wantStatus, tc.wantMessage)
			}
		})
	}

	t.Run("database closed", func(t *testing.T) {
		db := newProfileTestDB(t)
		_ = db.Close()
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &harukiDatabase.HarukiToolboxDBManager{DB: db}}
		status, resp := runAsUser(t, handleChangePassword(helper), "u-1", `{}`)
		if status != fiber.StatusInternalServerError || resp.Message != "Failed to verify user" {
			t.Fatalf("got %d %q", status, resp.Message)
		}
	})
}

// The Kratos password change validates locally before calling Kratos, and maps
// each Kratos admin failure to a response that does not leak its cause.
func TestHandleChangePasswordViaKratosErrors(t *testing.T) {
	adminStatus := map[string]int{
		"kid-missing":  http.StatusNotFound,
		"kid-down":     http.StatusBadGateway,
		"kid-rejected": http.StatusBadRequest,
	}
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/admin/identities/")
		if status, ok := adminStatus[id]; ok {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"x"}}`))
			return
		}
		// A known identity without an email cannot be verified by password.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + id + `","traits":{}}`))
	}))
	t.Cleanup(admin.Close)

	sessionHandler := harukiAPIHelper.NewSessionHandler(nil, "")
	sessionHandler.ConfigureIdentityProvider("kratos", admin.URL, admin.URL, "", "", true, true, 2*time.Second, nil)
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{SessionHandler: sessionHandler}

	ptr := func(s string) *string { return &s }
	goodPassword := harukiAPIHelper.ChangePasswordPayload{OldPassword: "old-password", NewPassword: "new-password"}
	tests := []struct {
		name        string
		user        *postgresql.User
		payload     harukiAPIHelper.ChangePasswordPayload
		wantStatus  int
		wantMessage string
		wantReason  string
	}{
		{"nil user", nil, goodPassword, fiber.StatusUnauthorized, "invalid user session", "invalid_user"},
		{"too short", &postgresql.User{ID: "u"}, harukiAPIHelper.ChangePasswordPayload{NewPassword: "short"}, fiber.StatusBadRequest, "password must be at least 8 characters", "new_password_too_short"},
		{"too long", &postgresql.User{ID: "u"}, harukiAPIHelper.ChangePasswordPayload{NewPassword: strings.Repeat("x", 73)}, fiber.StatusBadRequest, "password is too long (max 72 bytes)", "new_password_too_long"},
		{"not linked", &postgresql.User{ID: "u"}, goodPassword, fiber.StatusUnauthorized, "invalid user session", "identity_not_linked"},
		{"blank link", &postgresql.User{ID: "u", KratosIdentityID: ptr("  ")}, goodPassword, fiber.StatusUnauthorized, "invalid user session", "identity_not_linked"},
		{"identity gone", &postgresql.User{ID: "u", KratosIdentityID: ptr("kid-missing")}, goodPassword, fiber.StatusUnauthorized, "invalid user session", "identity_not_found"},
		{"identity without email", &postgresql.User{ID: "u", KratosIdentityID: ptr("kid-noemail")}, goodPassword, fiber.StatusUnauthorized, "invalid user session", "identity_not_found"},
		{"kratos down", &postgresql.User{ID: "u", KratosIdentityID: ptr("kid-down")}, goodPassword, fiber.StatusInternalServerError, "Failed to process request", "identity_provider_unavailable"},
		{"kratos rejects", &postgresql.User{ID: "u", KratosIdentityID: ptr("kid-rejected")}, goodPassword, fiber.StatusBadRequest, "Old password is incorrect", "old_password_invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := harukiAPIHelper.SystemLogResultFailure
			reason := "unknown"
			sessionClearFailed := false
			app := fiber.New()
			app.Put("/", func(c fiber.Ctx) error {
				return handleChangePasswordViaKratos(c, helper, tc.user, tc.payload, &result, &reason, &sessionClearFailed)
			})
			resp, err := app.Test(httptest.NewRequest(http.MethodPut, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var decoded profileTestResponse
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("decode %q: %v", raw, err)
			}
			if resp.StatusCode != tc.wantStatus || decoded.Message != tc.wantMessage || reason != tc.wantReason {
				t.Fatalf("got %d %q reason %q, want %d %q reason %q", resp.StatusCode, decoded.Message, reason, tc.wantStatus, tc.wantMessage, tc.wantReason)
			}
			if result != harukiAPIHelper.SystemLogResultFailure || sessionClearFailed {
				t.Fatalf("failure recorded as result=%q sessionClearFailed=%v", result, sessionClearFailed)
			}
		})
	}
}

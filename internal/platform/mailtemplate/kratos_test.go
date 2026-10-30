package mailtemplate

import (
	"bytes"
	htmltemplate "html/template"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	texttemplate "text/template"
)

func kratosTemplateRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "external", "kratos", "courier-templates")
}

// TestKratosCourierTemplatesAreGenerated keeps the committed Kratos templates
// in sync with the shared layout. Set UPDATE_KRATOS_TEMPLATES=1 to rewrite
// them.
func TestKratosCourierTemplatesAreGenerated(t *testing.T) {
	templates, err := KratosTemplates()
	if err != nil {
		t.Fatal(err)
	}
	root := kratosTemplateRoot(t)
	update := os.Getenv("UPDATE_KRATOS_TEMPLATES") == "1"
	want := map[string]bool{}
	for _, tpl := range templates {
		for name, content := range tpl.Files() {
			path := filepath.Join(root, filepath.FromSlash(tpl.Dir), name)
			want[path] = true
			if update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (regenerate with UPDATE_KRATOS_TEMPLATES=1)", err)
			}
			if string(got) != content {
				t.Errorf("%s is out of date (regenerate with UPDATE_KRATOS_TEMPLATES=1)", path)
			}
		}
	}
	// No stale files from a removed template.
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !want[path] {
			t.Errorf("unexpected file %s", path)
		}
		return nil
	})
}

// The models mirror the Kratos v25.4 courier template models
// (courier/template/email/*.go); a field the templates use but Kratos does
// not provide fails execution.
type kratosVerificationCodeValid struct {
	To, VerificationURL, VerificationCode, RequestURL string
	Identity, TransientPayload                        map[string]any
	ExpiresInMinutes                                  int
}

type kratosRecoveryCodeValid struct {
	To, RecoveryCode, RequestURL string
	Identity, TransientPayload   map[string]any
	ExpiresInMinutes             int
}

type kratosLoginCodeValid struct {
	To, LoginCode, RequestURL  string
	Identity, TransientPayload map[string]any
	ExpiresInMinutes           int
}

type kratosRegistrationCodeValid struct {
	To, RegistrationCode, RequestURL string
	Traits, TransientPayload         map[string]any
	ExpiresInMinutes                 int
}

type kratosInvalid struct {
	To, RequestURL   string
	TransientPayload map[string]any
}

type kratosSample struct {
	dir     string
	caption string
	model   any
	want    []string
}

const sampleVerificationURL = "https://auth.example.com/self-service/verification?flow=6c1a3b0e-2f4d-4d8e-9b1a-0d5e7f9c2a41&code=294715"

func kratosSamples() []kratosSample {
	return []kratosSample{
		{"verification_code/valid", "Kratos：邮箱验证码（注册后 / 更换邮箱）", kratosVerificationCodeValid{To: "user@example.com", VerificationCode: "294715", VerificationURL: sampleVerificationURL, ExpiresInMinutes: 15}, []string{"294715", "15 分钟内有效", "flow=6c1a3b0e-2f4d-4d8e-9b1a-0d5e7f9c2a41"}},
		{"verification_code/invalid", "Kratos：验证请求的邮箱未注册", kratosInvalid{To: "user@example.com"}, []string{"没有关联任何 Haruki 工具箱账号"}},
		{"recovery_code/valid", "Kratos：找回账号验证码", kratosRecoveryCodeValid{To: "user@example.com", RecoveryCode: "830164", ExpiresInMinutes: 15}, []string{"830164", "15 分钟内有效"}},
		{"recovery_code/invalid", "Kratos：找回请求的邮箱未注册", kratosInvalid{To: "user@example.com"}, []string{"此邮箱没有关联任何账号"}},
		{"login_code/valid", "Kratos：登录验证码（当前未启用）", kratosLoginCodeValid{To: "user@example.com", LoginCode: "507391", ExpiresInMinutes: 15}, []string{"507391"}},
		{"registration_code/valid", "Kratos：注册验证码（当前未启用）", kratosRegistrationCodeValid{To: "user@example.com", RegistrationCode: "662048", ExpiresInMinutes: 15}, []string{"662048"}},
	}
}

type kratosRendered struct {
	subject, html, text string
}

func executeKratosTemplate(t *testing.T, tpl KratosTemplate, model any) kratosRendered {
	t.Helper()
	exec := func(name, src string, html bool) string {
		var buf bytes.Buffer
		var err error
		if html {
			var parsed *htmltemplate.Template
			if parsed, err = htmltemplate.New(name).Option("missingkey=error").Parse(src); err == nil {
				err = parsed.Execute(&buf, model)
			}
		} else {
			var parsed *texttemplate.Template
			if parsed, err = texttemplate.New(name).Option("missingkey=error").Parse(src); err == nil {
				err = parsed.Execute(&buf, model)
			}
		}
		if err != nil {
			t.Fatalf("%s/%s: %v", tpl.Dir, name, err)
		}
		return buf.String()
	}
	return kratosRendered{
		subject: strings.TrimSpace(exec("subject", tpl.Subject, false)),
		html:    exec("html", tpl.HTMLBody, true),
		text:    exec("text", tpl.TextBody, false),
	}
}

func TestKratosTemplatesExecuteWithKratosModels(t *testing.T) {
	templates, err := KratosTemplates()
	if err != nil {
		t.Fatal(err)
	}
	byDir := map[string]KratosTemplate{}
	for _, tpl := range templates {
		byDir[tpl.Dir] = tpl
	}
	// Every code-based email family Kratos v25.4 sends
	// (selfservice/strategy/code/code_sender.go). The link-based
	// recovery/valid and verification/valid families are unused because the
	// recovery and verification flows are configured with `use: code`.
	wantDirs := []string{
		"login_code/valid",
		"recovery_code/invalid",
		"recovery_code/valid",
		"registration_code/valid",
		"verification_code/invalid",
		"verification_code/valid",
	}
	var gotDirs []string
	for _, tpl := range templates {
		gotDirs = append(gotDirs, tpl.Dir)
	}
	if strings.Join(gotDirs, ",") != strings.Join(wantDirs, ",") {
		t.Fatalf("generated families = %v, want %v", gotDirs, wantDirs)
	}
	samples := kratosSamples()
	if len(samples) != len(templates) {
		t.Fatalf("%d samples for %d templates", len(samples), len(templates))
	}
	for _, sample := range samples {
		tpl, ok := byDir[sample.dir]
		if !ok {
			t.Fatalf("no template for %s", sample.dir)
		}
		out := executeKratosTemplate(t, tpl, sample.model)
		if !strings.HasSuffix(out.subject, " | "+Brand) {
			t.Errorf("%s subject = %q", sample.dir, out.subject)
		}
		for part, body := range map[string]string{"html": out.html, "text": out.text} {
			for _, want := range append([]string{Brand, SiteURL, footer}, sample.want...) {
				if !strings.Contains(body, want) {
					t.Errorf("%s %s part lacks %q", sample.dir, part, want)
				}
			}
			if strings.Contains(body, "KRATOSVAR") || strings.Contains(body, "{{") {
				t.Errorf("%s %s part has an unresolved placeholder", sample.dir, part)
			}
		}
	}
	// The verification code is shown in the code box on its own, not only
	// inside the link, and the link is a real link in the HTML part.
	out := executeKratosTemplate(t, byDir["verification_code/valid"], kratosSamples()[0].model)
	if !strings.Contains(out.html, ">294715</div>") || !strings.Contains(out.text, "验证码：294715") {
		t.Errorf("verification code box missing")
	}
	if !strings.Contains(out.html, `href="https://auth.example.com/self-service/verification?flow=6c1a3b0e-2f4d-4d8e-9b1a-0d5e7f9c2a41&amp;code=294715"`) {
		t.Errorf("verification link missing from html: %s", out.html)
	}
}

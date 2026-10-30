package mailtemplate

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Kratos courier templates are generated from the shared layout. Each one is
// rendered with placeholder tokens, which are then replaced by the Kratos
// template actions ({{ .VerificationCode }} and so on); Kratos executes the
// result with Go's html/template and text/template.
//
// The generated files live in external/kratos/courier-templates and are
// checked by TestKratosCourierTemplatesAreGenerated. After changing the
// layout or the content below, regenerate them with:
//
//	UPDATE_KRATOS_TEMPLATES=1 go test ./internal/platform/mailtemplate -run TestKratosCourierTemplatesAreGenerated

// KratosTemplate is one Kratos courier template set (subject, HTML body and
// plain-text body) for the directory Dir under template_override_path.
type KratosTemplate struct {
	Dir      string
	Subject  string
	HTMLBody string
	TextBody string
}

// Files maps file names inside Dir to their contents.
func (t KratosTemplate) Files() map[string]string {
	return map[string]string{
		"email.subject.gotmpl":        t.Subject + "\n",
		"email.body.gotmpl":           t.HTMLBody,
		"email.body.plaintext.gotmpl": t.TextBody,
	}
}

var kratosPlaceholder = regexp.MustCompile(`KRATOSVAR_([A-Za-z]+)`)

// kratosVar is the placeholder for a field of the Kratos template model.
func kratosVar(field string) string { return "KRATOSVAR_" + field }

func kratosExpiry() string {
	return kratosVar("ExpiresInMinutes") + " 分钟内有效"
}

const (
	kratosIgnore       = "如果这不是您本人的操作，请忽略此邮件，您的账号不会发生任何变化。请勿将验证码告诉任何人。"
	kratosIgnoreNoCode = "如果这不是您本人的操作，请忽略此邮件。"
)

func kratosEmails() map[string]Email {
	return map[string]Email{
		"verification_code/valid": {
			Subject:   subject("验证您的邮箱"),
			Preheader: "邮箱验证码，" + kratosExpiry() + "。",
			Title:     "验证您的邮箱",
			Intro:     []string{"请在页面中输入下方验证码，或点击按钮，完成 Haruki 工具箱的邮箱验证。"},
			Code:      &Code{Label: "验证码", Value: kratosVar("VerificationCode"), Note: kratosExpiry()},
			Action:    &Action{Label: "验证邮箱", URL: kratosVar("VerificationURL")},
			Notice:    kratosIgnore,
		},
		"verification_code/invalid": {
			Subject: subject("邮箱验证请求"),
			Title:   "无法验证此邮箱",
			Intro:   []string{"有人（可能是您）请求验证此邮箱地址，但它没有关联任何 Haruki 工具箱账号。"},
			Outro:   []string{"如果您正在使用 Haruki 工具箱，请确认填写的是注册时使用的邮箱。"},
			Notice:  kratosIgnoreNoCode,
		},
		"recovery_code/valid": {
			Subject:   subject("找回账号验证码"),
			Preheader: "找回账号验证码，" + kratosExpiry() + "。",
			Title:     "找回您的账号",
			Intro:     []string{"我们收到了找回 Haruki 工具箱账号的请求。请在找回页面输入下方验证码，之后即可设置新密码。"},
			Code:      &Code{Label: "验证码", Value: kratosVar("RecoveryCode"), Note: kratosExpiry()},
			Notice:    "如果这不是您本人的操作，请忽略此邮件，您的密码不会改变。请勿将验证码告诉任何人。",
		},
		"recovery_code/invalid": {
			Subject: subject("账号找回请求"),
			Title:   "无法找回账号",
			Intro:   []string{"有人（可能是您）尝试用此邮箱找回 Haruki 工具箱账号，但此邮箱没有关联任何账号。"},
			Outro:   []string{"如果您有 Haruki 工具箱账号，请改用注册时使用的邮箱再试一次。"},
			Notice:  kratosIgnoreNoCode,
		},
		"login_code/valid": {
			Subject:   subject("登录验证码"),
			Preheader: "登录验证码，" + kratosExpiry() + "。",
			Title:     "登录验证码",
			Intro:     []string{"请在登录页面输入下方验证码，登录 Haruki 工具箱。"},
			Code:      &Code{Label: "验证码", Value: kratosVar("LoginCode"), Note: kratosExpiry()},
			Notice:    "如果这不是您本人的操作，请忽略此邮件；没有验证码，其他人无法登录您的账号。请勿将验证码告诉任何人。",
		},
		"registration_code/valid": {
			Subject:   subject("注册验证码"),
			Preheader: "注册验证码，" + kratosExpiry() + "。",
			Title:     "完成注册",
			Intro:     []string{"请在注册页面输入下方验证码，完成 Haruki 工具箱账号注册。"},
			Code:      &Code{Label: "验证码", Value: kratosVar("RegistrationCode"), Note: kratosExpiry()},
			Notice:    kratosIgnoreNoCode,
		},
	}
}

// KratosTemplates renders every Kratos courier template, sorted by Dir.
func KratosTemplates() ([]KratosTemplate, error) {
	emails := kratosEmails()
	dirs := make([]string, 0, len(emails))
	for dir := range emails {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)

	out := make([]KratosTemplate, 0, len(dirs))
	for _, dir := range dirs {
		e := emails[dir]
		if err := e.validate(true); err != nil {
			return nil, err
		}
		msg, err := render(e)
		if err != nil {
			return nil, fmt.Errorf("kratos template %s: %w", dir, err)
		}
		t := KratosTemplate{
			Dir:      dir,
			Subject:  toKratos(msg.Subject),
			HTMLBody: toKratos(msg.HTML),
			TextBody: toKratos(msg.Text),
		}
		if strings.Contains(t.HTMLBody, "KRATOSVAR") || strings.Contains(t.TextBody, "KRATOSVAR") {
			return nil, fmt.Errorf("kratos template %s: a placeholder was escaped during rendering", dir)
		}
		out = append(out, t)
	}
	return out, nil
}

func toKratos(s string) string {
	return kratosPlaceholder.ReplaceAllString(s, "{{ .${1} }}")
}

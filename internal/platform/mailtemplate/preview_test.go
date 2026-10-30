package mailtemplate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWritePreviews renders every email with sample data into
// $MAIL_PREVIEW_DIR (HTML, plain text and a manifest.json) for visual review.
// It is skipped unless the variable is set.
func TestWritePreviews(t *testing.T) {
	dir := os.Getenv("MAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set MAIL_PREVIEW_DIR to write previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Name    string `json:"name"`
		Caption string `json:"caption"`
		Subject string `json:"subject"`
	}
	var manifest []entry
	write := func(name, caption, subject, html, text string) {
		for ext, body := range map[string]string{".html": html, ".txt": text} {
			if err := os.WriteFile(filepath.Join(dir, name+ext), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		manifest = append(manifest, entry{Name: name, Caption: caption, Subject: subject})
	}
	for _, sample := range sampleEmails() {
		msg, err := Render(sample.email)
		if err != nil {
			t.Fatal(err)
		}
		write(sample.name, sample.caption, msg.Subject, msg.HTML, msg.Text)
	}
	templates, err := KratosTemplates()
	if err != nil {
		t.Fatal(err)
	}
	byDir := map[string]KratosTemplate{}
	for _, tpl := range templates {
		byDir[tpl.Dir] = tpl
	}
	for _, sample := range kratosSamples() {
		out := executeKratosTemplate(t, byDir[sample.dir], sample.model)
		write("kratos-"+strings.ReplaceAll(sample.dir, "/", "-"), sample.caption, out.subject, out.html, out.text)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

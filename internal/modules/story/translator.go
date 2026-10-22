package story

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// translation is the result for one episode.
type translation struct {
	Names   map[string]string
	Lines   []string
	Summary string
	Usage   llmUsage
}

// sourceHash fingerprints a scenario's dialogue.
func sourceHash(s *Scenario) string {
	h := sha256.New()
	for _, t := range s.TalkData {
		h.Write([]byte(t.WindowDisplayName))
		h.Write([]byte{0})
		h.Write([]byte(t.Body))
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// symbolsOnly reports a line with no letters (♪, dashes, "……！"), which is
// kept verbatim instead of being sent to the model.
func symbolsOnly(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

type translator struct {
	llm        *llmClient
	chunkLines int
	tailLines  int
}

// translate translates every TalkData body of one scenario. pc carries the
// series context; Tail is filled per chunk here.
func (t *translator) translate(ctx context.Context, scn *Scenario, pc promptContext) (*translation, error) {
	n := len(scn.TalkData)
	res := &translation{Names: map[string]string{}, Lines: make([]string, n)}
	var todo []promptLine
	var speakers []string
	var text strings.Builder
	for i, td := range scn.TalkData {
		speakers = append(speakers, td.WindowDisplayName)
		text.WriteString(td.WindowDisplayName)
		text.WriteString(td.Body)
		if symbolsOnly(td.Body) {
			res.Lines[i] = td.Body
			continue
		}
		todo = append(todo, promptLine{I: i, Speaker: td.WindowDisplayName, Text: td.Body})
	}
	if pc.Glossary != nil {
		pc.Terms, pc.Names = pc.Glossary.hitsFor(text.String(), speakers)
	}
	chunk := t.chunkLines
	if chunk <= 0 {
		chunk = 120
	}
	var summaries []string
	run := func(lines []promptLine, tail []string) error {
		pc.Tail = tail
		reply, usage, err := t.llm.complete(ctx, systemPrompt, buildUserPrompt(pc, lines))
		res.Usage.PromptTokens += usage.PromptTokens
		res.Usage.CompletionTokens += usage.CompletionTokens
		if err != nil {
			return err
		}
		out, err := parseModelOutput(reply)
		if err != nil {
			return fmt.Errorf("parse model reply: %w", err)
		}
		want := map[int]bool{}
		for _, l := range lines {
			want[l.I] = true
		}
		for _, l := range out.Lines {
			if want[l.I] && strings.TrimSpace(l.Text) != "" {
				res.Lines[l.I] = l.Text
			}
		}
		for k, v := range out.Names {
			if k != "" && v != "" {
				res.Names[k] = v
			}
		}
		if s := strings.TrimSpace(out.Summary); s != "" {
			summaries = append(summaries, s)
		}
		return nil
	}
	for k := 0; k < len(todo); k += chunk {
		part := todo[k:min(k+chunk, len(todo))]
		if err := run(part, t.tail(scn, res, part[0].I)); err != nil {
			return nil, err
		}
	}
	// one more pass for anything the model skipped
	var missing []promptLine
	for _, l := range todo {
		if res.Lines[l.I] == "" {
			missing = append(missing, l)
		}
	}
	if len(missing) > 0 {
		if err := run(missing, t.tail(scn, res, missing[0].I)); err != nil {
			return nil, err
		}
		for _, l := range missing {
			if res.Lines[l.I] == "" {
				return nil, fmt.Errorf("model left %d of %d lines untranslated", countEmpty(res.Lines), n)
			}
		}
	}
	// official speaker labels win over whatever the model chose
	if pc.Glossary != nil {
		for _, sp := range speakers {
			if cn, ok := pc.Glossary.Speakers[sp]; ok {
				res.Names[sp] = cn
			}
		}
	}
	res.Summary = strings.Join(summaries, "")
	return res, nil
}

// tail is the translated dialogue just before line `before`, for continuity.
func (t *translator) tail(scn *Scenario, res *translation, before int) []string {
	keep := t.tailLines
	if keep <= 0 {
		keep = 12
	}
	var out []string
	for i := before - 1; i >= 0 && len(out) < keep; i-- {
		if res.Lines[i] == "" {
			continue
		}
		name := scn.TalkData[i].WindowDisplayName
		if cn, ok := res.Names[name]; ok {
			name = cn
		}
		out = append([]string{name + "：" + res.Lines[i]}, out...)
	}
	return out
}

func countEmpty(lines []string) int {
	n := 0
	for _, l := range lines {
		if l == "" {
			n++
		}
	}
	return n
}

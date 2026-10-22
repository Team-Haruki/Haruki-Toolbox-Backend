package story

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// translation is the result for one episode.
type translation struct {
	Names map[string]string
	Lines []string
	// Effects maps SpecialEffectData indexes (decimal) to translated text.
	Effects map[string]string
	Summary string
	Usage   llmUsage
}

// sourceHash fingerprints a scenario's text.
func sourceHash(s *Scenario) string {
	h := sha256.New()
	for _, t := range s.TalkData {
		h.Write([]byte(t.WindowDisplayName))
		h.Write([]byte{0})
		h.Write([]byte(t.Body))
		h.Write([]byte{1})
	}
	for _, e := range s.SpecialEffectData {
		if effectLabels[e.EffectType] != "" {
			h.Write([]byte(e.StringVal))
			h.Write([]byte{2})
		}
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

// Snippet actions that carry text.
const (
	actionTalk          = 1
	actionSpecialEffect = 6
)

// effectLabels marks the SpecialEffectType members whose StringVal is shown
// to the player; the label stands in for the speaker in the prompt.
var effectLabels = map[int]string{
	8:  "（字幕）",
	18: "（地点标签）",
	23: "（选项，以/分隔）",
	24: "（全屏旁白）",
}

type translator struct {
	llm        *llmClient
	chunkLines int
	tailLines  int
}

// translate translates the dialogue and the text effects of one scenario,
// in timeline order so the model reads them in context. pc carries the
// series context; Tail is filled per chunk here.
func (t *translator) translate(ctx context.Context, scn *Scenario, pc promptContext) (*translation, error) {
	n := len(scn.TalkData)
	res := &translation{Names: map[string]string{}, Lines: make([]string, n), Effects: map[string]string{}}
	var todo []promptLine
	var speakers []string
	var text strings.Builder
	talkSeen := make([]bool, n)
	effectSeen := map[int]bool{}
	addTalk := func(i int) {
		if i < 0 || i >= n || talkSeen[i] {
			return
		}
		talkSeen[i] = true
		td := scn.TalkData[i]
		speakers = append(speakers, td.WindowDisplayName)
		text.WriteString(td.WindowDisplayName)
		text.WriteString(td.Body)
		if symbolsOnly(td.Body) {
			res.Lines[i] = td.Body
			return
		}
		todo = append(todo, promptLine{I: i, Speaker: td.WindowDisplayName, Text: td.Body})
	}
	addEffect := func(i int) {
		if i < 0 || i >= len(scn.SpecialEffectData) || effectSeen[i] {
			return
		}
		effectSeen[i] = true
		e := scn.SpecialEffectData[i]
		label := effectLabels[e.EffectType]
		if label == "" || strings.TrimSpace(e.StringVal) == "" {
			return
		}
		text.WriteString(e.StringVal)
		if symbolsOnly(e.StringVal) {
			res.Effects[strconv.Itoa(i)] = e.StringVal
			return
		}
		// effects are numbered after the dialogue lines
		todo = append(todo, promptLine{I: n + i, Speaker: label, Text: e.StringVal})
	}
	for _, sn := range scn.Snippets {
		switch sn.Action {
		case actionTalk:
			addTalk(sn.ReferenceIndex)
		case actionSpecialEffect:
			addEffect(sn.ReferenceIndex)
		}
	}
	for i := range n {
		addTalk(i) // lines no snippet points at
	}
	if pc.Glossary != nil {
		pc.Terms, pc.Names = pc.Glossary.hitsFor(text.String(), speakers)
	}
	chunk := t.chunkLines
	if chunk <= 0 {
		chunk = 120
	}
	result := func(i int) *string {
		if i < n {
			return &res.Lines[i]
		}
		v := res.Effects[strconv.Itoa(i-n)]
		return &v
	}
	done := func(i int) bool { return strings.TrimSpace(*result(i)) != "" }
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
			if !want[l.I] || strings.TrimSpace(l.Text) == "" {
				continue
			}
			if l.I < n {
				res.Lines[l.I] = l.Text
			} else {
				res.Effects[strconv.Itoa(l.I-n)] = l.Text
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
		if err := run(part, t.tail(res, todo[:k], done)); err != nil {
			return nil, err
		}
	}
	// one more pass for anything the model skipped
	var missing []promptLine
	for _, l := range todo {
		if !done(l.I) {
			missing = append(missing, l)
		}
	}
	if len(missing) > 0 {
		before := slices.IndexFunc(todo, func(l promptLine) bool { return l.I == missing[0].I })
		if err := run(missing, t.tail(res, todo[:before], done)); err != nil {
			return nil, err
		}
		left := 0
		for _, l := range missing {
			if !done(l.I) {
				left++
			}
		}
		if left > 0 {
			return nil, fmt.Errorf("model left %d of %d lines untranslated", left, len(todo))
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

// tail is the translated text just before the next chunk (the end of
// `before`, in timeline order), for continuity.
func (t *translator) tail(res *translation, before []promptLine, done func(int) bool) []string {
	keep := t.tailLines
	if keep <= 0 {
		keep = 12
	}
	n := len(res.Lines)
	var out []string
	for i := len(before) - 1; i >= 0 && len(out) < keep; i-- {
		l := before[i]
		if !done(l.I) {
			continue
		}
		name, line := l.Speaker, ""
		if l.I < n {
			line = res.Lines[l.I]
			if cn, ok := res.Names[name]; ok {
				name = cn
			}
		} else {
			line = res.Effects[strconv.Itoa(l.I-n)]
		}
		out = append([]string{name + "：" + line}, out...)
	}
	return out
}

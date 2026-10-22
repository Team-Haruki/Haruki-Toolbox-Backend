package story

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func scenarioOf(lines ...[2]string) *Scenario {
	s := &Scenario{}
	for _, l := range lines {
		s.TalkData = append(s.TalkData, struct {
			WindowDisplayName string `json:"WindowDisplayName"`
			Body              string `json:"Body"`
			TalkCharacters    []struct {
				Character2dID int `json:"Character2dId"`
			} `json:"TalkCharacters"`
		}{WindowDisplayName: l[0], Body: l[1]})
	}
	return s
}

// fakeLLM answers each request by "translating" every requested line to
// "zh:<text>", except that the first reply drops line skipFirst.
func fakeLLM(t *testing.T, skipFirst int) (*httptest.Server, *int32) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Stream   bool          `json:"stream"`
			Messages []chatMessage `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		if !req.Stream {
			t.Errorf("request not streamed")
		}
		user := req.Messages[1].Content
		start := strings.LastIndex(user, "【待翻译台词】\n")
		var lines []promptLine
		_ = json.Unmarshal([]byte(user[start+len("【待翻译台词】\n"):]), &lines)
		out := modelOutput{Names: map[string]string{"杏": "杏"}, Summary: "概括。"}
		for _, l := range lines {
			if n == 1 && l.I == skipFirst {
				continue
			}
			out.Lines = append(out.Lines, struct {
				I    int    `json:"i"`
				Text string `json:"text"`
			}{l.I, "zh:" + l.Text})
		}
		raw, _ := json.Marshal(out)
		w.Header().Set("Content-Type", "text/event-stream")
		// split the JSON across two deltas like a real stream
		runes := []rune(string(raw))
		half := len(runes) / 2
		for _, part := range []string{string(runes[:half]), string(runes[half:])} {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": part}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestTranslateChunksAndRetriesMissing(t *testing.T) {
	srv, calls := fakeLLM(t, 2)
	tr := &translator{
		llm:        &llmClient{baseURL: srv.URL, model: "m", timeout: 5 * time.Second, client: srv.Client(), backoff: time.Millisecond},
		chunkLines: 2,
	}
	scn := scenarioOf([2]string{"杏", "おはよう"}, [2]string{"杏", "♪　――！"}, [2]string{"こはね", "えっと……"},
		[2]string{"杏", "行こう！"}, [2]string{"こはね", "うん"})
	gloss := &Glossary{Speakers: map[string]string{"こはね": "心羽"}}
	got, err := tr.translate(context.Background(), scn, promptContext{Episode: &Episode{Kind: "event", SeriesTitle: "S", Label: "第1話", Title: "T"}, Glossary: gloss})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	want := []string{"zh:おはよう", "♪　――！", "zh:えっと……", "zh:行こう！", "zh:うん"}
	for i := range want {
		if got.Lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got.Lines[i], want[i])
		}
	}
	// 4 translatable lines in chunks of 2 = 2 calls, plus one for the line the first reply dropped
	if n := atomic.LoadInt32(calls); n != 3 {
		t.Fatalf("calls = %d, want 3", n)
	}
	if got.Usage.PromptTokens != 30 || got.Names["杏"] != "杏" || got.Names["こはね"] != "心羽" || got.Summary == "" {
		t.Fatalf("usage/names/summary = %+v %v %q", got.Usage, got.Names, got.Summary)
	}
}

func TestParseModelOutputStripsFence(t *testing.T) {
	out, err := parseModelOutput("```json\n{\"names\":{},\"lines\":[{\"i\":0,\"text\":\"好\"}],\"summary\":\"s\"}\n```")
	if err != nil || len(out.Lines) != 1 || out.Lines[0].Text != "好" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestSymbolsOnly(t *testing.T) {
	for s, want := range map[string]bool{"♪　――――！": true, "……！": true, "えっ": false, "３の２乗": false, "": true} {
		if got := symbolsOnly(s); got != want {
			t.Errorf("symbolsOnly(%q) = %v", s, got)
		}
	}
}

func TestPendingOrderNewestSeriesFirstInEpisodeOrder(t *testing.T) {
	c := &Catalog{ByID: map[string]*Episode{}, CNHas: map[string]bool{"e1_01": true}}
	for _, e := range []*Episode{
		{ScenarioID: "e1_01", SeriesKey: "event:1", No: 1, ReleasedAt: 100},
		{ScenarioID: "e2_02", SeriesKey: "event:2", No: 2, ReleasedAt: 300},
		{ScenarioID: "e2_01", SeriesKey: "event:2", No: 1, ReleasedAt: 300},
		{ScenarioID: "c9_01", SeriesKey: "card:9", No: 1, ReleasedAt: 200},
	} {
		c.Episodes = append(c.Episodes, e)
		c.ByID[e.ScenarioID] = e
	}
	got := c.pendingOrder(func(id string) bool { return id == "c9_01" })
	var ids []string
	for _, e := range got {
		ids = append(ids, e.ScenarioID)
	}
	if strings.Join(ids, ",") != "e2_01,e2_02" {
		t.Fatalf("order = %v", ids)
	}
}

func TestKanaRatioSeparatesPlaceholderText(t *testing.T) {
	jp := scenarioOf([2]string{"杏", "やっとこのセカイに来てもらえたのね。"})
	cn := scenarioOf([2]string{"杏", "你们终于来到这个「世界」了。"})
	if r := kanaRatio(jp); r < untranslatedKana {
		t.Fatalf("jp ratio %.2f", r)
	}
	if r := kanaRatio(cn); r >= untranslatedKana {
		t.Fatalf("cn ratio %.2f", r)
	}
}

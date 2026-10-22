package story

import (
	"encoding/json"
	"fmt"
	"strings"
)

// systemPrompt sets the translator's role and the house style learned from
// the official CN text (punctuation, brackets, names, line breaks).
const systemPrompt = `你是《世界计划 彩色舞台 feat. 初音未来》（Project SEKAI，简称 PJSK）简体中文官方剧情的资深译者。
你会收到一话日服剧情的台词（JSON 数组，每项是 i 编号、说话人 speaker、原文 text），把它翻译成国服官方风格的简体中文。

翻译要求：
1. 忠实原意，不增删剧情信息。结合前情提要、活动简介和上下文理解人物关系与伏笔，语气、口癖、自称和性格要贴合角色（例如：司说话夸张自信，类慢条斯理带点狡黠，绘名嘴硬，瑞希俏皮，宁宁傲娇，彰人冷淡直率，冬弥礼貌克制，杏开朗爽快，心羽怯生生但坚定，奏温柔低落，真冬表面乖巧内心空虚）。
2. 角色名、团体名、地名、歌名一律使用下面给出的国服官方译名；未列出的人名按国服惯例沿用汉字或音译，同一名字全篇保持一致。
3. 说话人（speaker）也要翻译：优先用给出的官方说话人译名；“中学生の杏”“男性教師”这类标签按“初中时的杏”“男老师”这样的中文说法；“？？？”保持不变；多人用“&”连接（“杏・瑞希”→“杏&瑞希”）。
4. 称呼：日语敬称（さん/くん/ちゃん/先輩）按中文习惯处理，一般省略，或译成“学姐/学长/前辈”等，不要保留日文敬称；昵称按中文自然说法。
5. 标点：用中文全角标点；『』（线上聊天、电话、屏幕文字、引用）和「」都译成“”；省略号用“……”；拖音“～”保留；删掉句间的全角空格；句末补全标点。
6. 保留原文中的换行（\n），译文的换行位置与原文的分句对应。
7. 只由符号、音符或拖长音组成的台词（如“♪　――――！”“……！”）原样保留，不改动符号数量。
8. 拟声词、语气词译成自然的中文（“えへへ”→“诶嘿嘿”，“うむ”→“嗯”），不要硬音译。
9. 片假名「セカイ」（由想法诞生的那个特殊世界）是官方固定译名，一律译作「世界」并保留直角引号（不受第 5 条影响，例如“我是你们「世界」的初音未来。”）；普通的“世界／世の中”照常翻译，不加引号。官方保持英文原样的名称（如 Vivid BAD SQUAD、RAD WEEKEND、WEEKEND GARAGE）保持原样。
10. 不要合并、拆分或跳过台词；每个 i 都必须有译文，不要输出原文。

只输出一个 JSON 对象，不要其他文字：
{"names": {"日文说话人": "中文说话人"}, "lines": [{"i": 编号, "text": "译文"}], "summary": "用 3～5 句简体中文概括本段剧情（人物、事件、情绪走向），供翻译后续话数参考"}`

// promptLine is one dialogue line as sent to the model.
type promptLine struct {
	I       int    `json:"i"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

// promptContext is everything around the lines being translated.
type promptContext struct {
	Episode   *Episode
	Siblings  []*Episode // the whole series, in order
	Summaries []string   // "第1話「…」：summary" of earlier translated episodes
	Glossary  *Glossary
	Terms     []string
	Names     []string
	// Tail is the end of the previous chunk of this episode (already translated).
	Tail []string
}

func buildUserPrompt(pc promptContext, lines []promptLine) string {
	e := pc.Episode
	var b strings.Builder
	section := func(title, body string) {
		if strings.TrimSpace(body) == "" {
			return
		}
		fmt.Fprintf(&b, "【%s】\n%s\n\n", title, body)
	}
	kind := map[string]string{"event": "活动剧情", "unit": "主线剧情", "card": "卡面剧情", "special": "特别剧情", "area": "区域对话"}[e.Kind]
	head := fmt.Sprintf("%s：%s", kind, e.SeriesTitle)
	if e.Label != "" {
		head += " " + e.Label
	}
	if e.Title != "" {
		head += "「" + e.Title + "」"
	}
	section("剧情", head)
	section("简介", e.Outline)
	if len(pc.Siblings) > 1 {
		var parts []string
		for _, s := range pc.Siblings {
			mark := ""
			if s.ScenarioID == e.ScenarioID {
				mark = "（本话）"
			}
			parts = append(parts, fmt.Sprintf("%s「%s」%s", s.Label, s.Title, mark))
		}
		section("本系列各话", strings.Join(parts, " / "))
	}
	if len(pc.Summaries) > 0 {
		section("前情提要", strings.Join(pc.Summaries, "\n"))
	} else {
		section("前情提要", "无（第一话，或前情没有可用的概括）")
	}
	if pc.Glossary != nil {
		section("角色官方译名", strings.Join(pc.Glossary.Characters, "\n"))
		section("团体官方译名", strings.Join(pc.Glossary.Units, "；"))
	}
	section("本话出现的地名/歌名官方译名", strings.Join(pc.Terms, "；"))
	section("本话说话人的官方译名", strings.Join(pc.Names, "；"))
	section("本话上一段已译台词（仅用于衔接语气，不要重复输出）", strings.Join(pc.Tail, "\n"))
	raw, _ := json.Marshal(lines)
	section("待翻译台词", string(raw))
	return strings.TrimSpace(b.String())
}

// modelOutput is the JSON the model answers with.
type modelOutput struct {
	Names map[string]string `json:"names"`
	Lines []struct {
		I    int    `json:"i"`
		Text string `json:"text"`
	} `json:"lines"`
	Summary string `json:"summary"`
}

// parseModelOutput takes the outermost JSON object out of the reply (models
// sometimes wrap it in a code fence).
func parseModelOutput(reply string) (*modelOutput, error) {
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in reply")
	}
	var out modelOutput
	if err := json.Unmarshal([]byte(reply[start:end+1]), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

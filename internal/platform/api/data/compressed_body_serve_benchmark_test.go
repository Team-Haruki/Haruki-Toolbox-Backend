package data

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/valyala/fasthttp"
)

// syntheticSuiteBody builds a deterministic suite-shaped JSON document of at
// least size bytes: the same kinds of arrays, key names and value ranges as a
// real suite snapshot (cards with episodes, music results per difficulty,
// events, items, honors), with no real user data.
func syntheticSuiteBody(size int) []byte {
	rng := rand.New(rand.NewPCG(20260930, uint64(size)))
	var b bytes.Buffer
	b.Grow(size + 4096)
	ts := func() int64 { return 1600000000000 + rng.Int64N(190000000000) }
	b.WriteString(`{"upload_time":1790000000,"userGamedata":{"userId":7400000000000000000,"name":"synthetic","deck":1,"rank":`)
	fmt.Fprintf(&b, "%d", rng.IntN(400)+1)
	b.WriteString(`,"exp":123456789,"totalExp":987654321,"coin":12345678,"virtualCoin":0,"lastLoginAt":`)
	fmt.Fprintf(&b, "%d", ts())
	b.WriteString(`},"userCards":[`)
	for i := 0; i < 1800; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"userId":7400000000000000000,"cardId":%d,"level":%d,"exp":%d,"totalExp":%d,"skillLevel":%d,"skillExp":0,"totalSkillExp":0,"masterRank":%d,"specialTrainingStatus":"%s","defaultImage":"%s","duplicateCount":%d,"createdAt":%d,"episodes":[{"cardEpisodeId":%d,"scenarioStatus":"already_read","scenarioStatusReasons":[],"isNotSkipped":false},{"cardEpisodeId":%d,"scenarioStatus":"unreleased","scenarioStatusReasons":["not_enough_level"],"isNotSkipped":false}]}`,
			i+1, rng.IntN(60)+1, rng.IntN(5000), rng.IntN(500000), rng.IntN(4)+1, rng.IntN(6),
			[]string{"done", "not_doing"}[rng.IntN(2)], []string{"original", "special_training"}[rng.IntN(2)],
			rng.IntN(5), ts(), 2*i+1, 2*i+2)
	}
	b.WriteString(`],"userMusics":[`)
	difficulties := []string{"easy", "normal", "hard", "expert", "master", "append"}
	for m := 0; m < 650; m++ {
		if m > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"userId":7400000000000000000,"musicId":%d,"userMusicDifficultyStatuses":[`, m+1)
		for d, difficulty := range difficulties {
			if d > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"musicId":%d,"musicDifficulty":"%s","musicDifficultyStatus":"available","userMusicResults":[`, m+1, difficulty)
			for r := 0; r < rng.IntN(3); r++ {
				if r > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, `{"userId":7400000000000000000,"musicId":%d,"musicDifficultyType":"%s","playType":"%s","playResult":"%s","highScore":%d,"fullComboFlg":%t,"fullPerfectFlg":%t,"mvpCount":%d,"superStarCount":%d,"createdAt":%d,"updatedAt":%d}`,
					m+1, difficulty, []string{"solo", "multi", "auto"}[rng.IntN(3)], []string{"clear", "full_combo", "full_perfect"}[rng.IntN(3)],
					rng.IntN(1500000), rng.IntN(2) == 0, rng.IntN(4) == 0, rng.IntN(20), rng.IntN(20), ts(), ts())
			}
			b.WriteString(`]}`)
		}
		b.WriteString(`]}`)
	}
	b.WriteString(`],"userEvents":[`)
	for e := 0; e < 180; e++ {
		if e > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"userId":7400000000000000000,"eventId":%d,"eventPoint":%d,"totalEventPoint":%d,"eventStatus":"%s","rank":%d}`,
			e+1, rng.IntN(50000000), rng.IntN(50000000), []string{"going", "end"}[rng.IntN(2)], rng.IntN(300000)+1)
	}
	b.WriteString(`],"userItems":[`)
	for i := 0; i < 900; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"userId":7400000000000000000,"itemId":%d,"quantity":%d}`, i+1, rng.IntN(100000))
	}
	b.WriteString(`],"userHonors":[`)
	for i := 0; i < 1500; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"userId":7400000000000000000,"honorId":%d,"level":%d,"obtainedAt":%d}`, i+1, rng.IntN(5)+1, ts())
	}
	b.WriteString(`],"userMissionStatuses":[`)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"userId":7400000000000000000,"missionType":"%s","missionId":%d,"progress":%d,"missionStatus":"%s","seq":%d}`,
			[]string{"normal", "beginner", "achievement", "character", "event"}[rng.IntN(5)], i+1, rng.IntN(10000),
			[]string{"achieved", "received", "progress"}[rng.IntN(3)], rng.IntN(50))
	}
	b.WriteString(`]}`)
	return b.Bytes()
}

// newGameDataBenchmarkApp mirrors the production middleware order that
// matters here: the compress middleware at BestSpeed wraps the handler.
func newGameDataBenchmarkApp(handler fiber.Handler) fasthttp.RequestHandler {
	app := fiber.New()
	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))
	app.Get("/", handler)
	return app.Handler()
}

// serveOnce runs one request through the app and returns the wire body size.
func serveOnce(b *testing.B, handler fasthttp.RequestHandler, ctx *fasthttp.RequestCtx, acceptEncoding string) int {
	ctx.Request.Reset()
	ctx.Response.Reset()
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI("/")
	if acceptEncoding != "" {
		ctx.Request.Header.Set(fasthttp.HeaderAcceptEncoding, acceptEncoding)
	}
	handler(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		b.Fatalf("status = %d", ctx.Response.StatusCode())
	}
	return len(ctx.Response.Body())
}

// BenchmarkGameDataBodyServe measures the private game-data handler's body
// work per request, end to end through the compress middleware:
//
//	hit:  stored entry -> response on the wire
//	miss: marshaled JSON -> compress for storage -> response on the wire
//
// "gzip1" is the previous storage format, "zstd" the new one. Reported
// metrics: stored-B (cache entry size) and wire-B (response body size).
func BenchmarkGameDataBodyServe(b *testing.B) {
	// 0 yields only the fixed arrays (~2.4 MB); 16 MiB matches a large JP suite.
	for _, size := range []int{0, 16 << 20} {
		encoded := syntheticSuiteBody(size)
		storedGzip, err := CompressGameDataBody(encoded)
		if err != nil {
			b.Fatal(err)
		}
		storedZstd, err := CompressGameDataBodyZstd(encoded)
		if err != nil {
			b.Fatal(err)
		}
		label := fmt.Sprintf("%.1fMB", float64(len(encoded))/1e6)
		for _, tc := range []struct {
			name           string
			stored         string
			acceptEncoding string
		}{
			{name: "hit/gzip1-stored/zstd-client", stored: storedGzip, acceptEncoding: "zstd"},
			{name: "hit/zstd-stored/zstd-client", stored: storedZstd, acceptEncoding: "zstd"},
			{name: "hit/gzip1-stored/gzip-client", stored: storedGzip, acceptEncoding: "gzip"},
			{name: "hit/zstd-stored/gzip-client", stored: storedZstd, acceptEncoding: "gzip"},
			{name: "hit/zstd-stored/identity-client", stored: storedZstd, acceptEncoding: ""},
		} {
			b.Run(label+"/"+tc.name, func(b *testing.B) {
				stored := tc.stored
				handler := newGameDataBenchmarkApp(func(c fiber.Ctx) error { return ServeGameDataBody(c, stored) })
				var ctx fasthttp.RequestCtx
				wire := 0
				b.ReportAllocs()
				b.SetBytes(int64(len(encoded)))
				for b.Loop() {
					wire = serveOnce(b, handler, &ctx, tc.acceptEncoding)
				}
				b.ReportMetric(float64(len(stored)), "stored-B")
				b.ReportMetric(float64(wire), "wire-B")
			})
		}
		for _, tc := range []struct {
			name           string
			compress       func([]byte) (string, error)
			acceptEncoding string
		}{
			{name: "miss/gzip1-stored/zstd-client", compress: CompressGameDataBody, acceptEncoding: "zstd"},
			{name: "miss/zstd-stored/zstd-client", compress: CompressGameDataBodyZstd, acceptEncoding: "zstd"},
		} {
			b.Run(label+"/"+tc.name, func(b *testing.B) {
				storedLen := 0
				handler := newGameDataBenchmarkApp(func(c fiber.Ctx) error {
					stored, err := tc.compress(encoded)
					if err != nil {
						return err
					}
					storedLen = len(stored)
					return ServeGameDataBody(c, stored)
				})
				var ctx fasthttp.RequestCtx
				wire := 0
				b.ReportAllocs()
				b.SetBytes(int64(len(encoded)))
				for b.Loop() {
					wire = serveOnce(b, handler, &ctx, tc.acceptEncoding)
				}
				b.ReportMetric(float64(storedLen), "stored-B")
				b.ReportMetric(float64(wire), "wire-B")
			})
		}
	}
}

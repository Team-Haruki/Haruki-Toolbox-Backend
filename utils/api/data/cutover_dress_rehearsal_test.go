package data

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"sort"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiDatabase "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiGameData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	harukiMongo "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/mongo"
)

// A dress rehearsal of the cutover's read flip.
//
// Everything else proves a piece: the response shaping is unit-tested against
// hand-built rows, and `gamedata-migrate verify` proves the two stores hold the
// same data. Neither proves that the SERVING path actually answers the same
// bytes once read_source flips — which is the step the maintenance window turns
// on and cannot be undone mid-request.
//
// Skipped unless both stores are pointed at:
//
//	GAMEDATA_REHEARSAL_MONGO=mongodb://127.0.0.1:27019
//	GAMEDATA_REHEARSAL_MONGO_DB=collections
//	GAMEDATA_REHEARSAL_PG=postgres://...
func TestCutoverReadFlipServesTheSameResponses(t *testing.T) {
	mongoURI := os.Getenv("GAMEDATA_REHEARSAL_MONGO")
	pgURL := os.Getenv("GAMEDATA_REHEARSAL_PG")
	if mongoURI == "" || pgURL == "" {
		t.Skip("set GAMEDATA_REHEARSAL_MONGO and GAMEDATA_REHEARSAL_PG to run the cutover dress rehearsal")
	}
	mongoDB := os.Getenv("GAMEDATA_REHEARSAL_MONGO_DB")
	if mongoDB == "" {
		mongoDB = "collections"
	}
	ctx := context.Background()

	cl, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Disconnect(context.Background()) }()

	mgr, err := harukiMongo.NewMongoDBManager(ctx, mongoURI, mongoDB, "suite", "mysekai")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := harukiGameData.NewPool(ctx, harukiGameData.PoolConfig{URL: pgURL, MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	mongoHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &harukiDatabase.HarukiToolboxDBManager{
			Mongo:    mgr,
			GameData: harukiGameData.NewService(pool, false),
		},
	}
	pgHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &harukiDatabase.HarukiToolboxDBManager{
			Mongo:    mgr,
			GameData: harukiGameData.NewService(pool, true),
		},
	}

	allowlist := []string{
		"userDecks", "userCards", "userAreas", "userHonors", "userMusics",
		"userEvents", "upload_time", "userProfile", "userCharacters",
		"userWorldBlooms", "userBondsHonors", "userMusicResults",
		"userMysekaiGates", "userProfileHonors", "userMysekaiCanvases",
		"userRankMatchSeasons", "userMysekaiMaterials", "userMusicAchievements",
		"userMysekaiCharacterTalks", "userWorldBloomSupportDecks",
		"userChallengeLiveSoloDecks", "userChallengeLiveSoloStages",
		"userChallengeLiveSoloResults", "userChallengeLiveSoloHighScoreRewards",
		"userMysekaiFixtureGameCharacterPerformanceBonuses",
	}
	allowSet := make(map[string]struct{}, len(allowlist))
	for _, k := range allowlist {
		allowSet[k] = struct{}{}
	}

	users := sampleUsers(ctx, t, cl.Database(mongoDB), "suite", 60)
	if len(users) == 0 {
		t.Fatal("no sample users")
	}

	shapes := []struct {
		name string
		key  string
	}{
		{"no key (allowlist)", ""},
		{"single key", "userCards"},
		{"single compact key", "userMusicResults"},
		{"single userGamedata", "userGamedata"},
		{"single missing key", "userAreas"},
		{"multi key", "userCards,userDecks,userMusicResults"},
		{"multi with missing", "userCards,userHonors"},
		{"duplicate key", "userCards,userCards"},
	}

	compared, mismatched := 0, 0
	for _, u := range users {
		for _, sh := range shapes {
			mResp, mErr := HandleSuiteRequest(ctx, mongoHelper, u.id, u.server, sh.key, allowSet, allowlist)
			pResp, pErr := HandleSuiteRequest(ctx, pgHelper, u.id, u.server, sh.key, allowSet, allowlist)

			if (mErr == nil) != (pErr == nil) {
				t.Errorf("%d/%s [%s]: error mismatch mongo=%v pg=%v", u.id, u.server, sh.name, mErr, pErr)
				mismatched++
				continue
			}
			if mErr != nil {
				compared++
				continue
			}
			mBody, err := EncodeGameDataBody(mResp)
			if err != nil {
				t.Fatal(err)
			}
			pBody, err := EncodeGameDataBody(pResp)
			if err != nil {
				t.Fatal(err)
			}
			mv, pv := canonJSON(t, mBody), canonJSON(t, pBody)
			compared++
			if !reflect.DeepEqual(mv, pv) {
				mismatched++
				if mismatched <= 5 {
					t.Errorf("%d/%s [%s]: response differs\n  mongo keys: %v\n  pg    keys: %v",
						u.id, u.server, sh.name, topKeys(mv), topKeys(pv))
				}
			}
		}
	}
	// mysekai: no allowlist, no single-key unwrap, absent keys omitted.
	mysekaiUsers := sampleUsers(ctx, t, cl.Database(mongoDB), "mysekai", 40)
	mysekaiShapes := []string{"", "updatedResources", "updatedResources,isEnabled", "_id", "server", "isEnabled"}
	for _, u := range mysekaiUsers {
		for _, key := range mysekaiShapes {
			mResp, mErr := HandleMysekaiRequest(ctx, mongoHelper, u.id, u.server, key)
			pResp, pErr := HandleMysekaiRequest(ctx, pgHelper, u.id, u.server, key)
			if (mErr == nil) != (pErr == nil) {
				t.Errorf("mysekai %d/%s [%q]: error mismatch mongo=%v pg=%v", u.id, u.server, key, mErr, pErr)
				mismatched++
				continue
			}
			compared++
			if mErr != nil {
				continue
			}
			mBody, _ := EncodeGameDataBody(mResp)
			pBody, _ := EncodeGameDataBody(pResp)
			if !reflect.DeepEqual(canonJSON(t, mBody), canonJSON(t, pBody)) {
				mismatched++
				if mismatched <= 5 {
					t.Errorf("mysekai %d/%s [%q]: response differs\n  mongo: %v\n  pg   : %v",
						u.id, u.server, key, topKeys(canonJSON(t, mBody)), topKeys(canonJSON(t, pBody)))
				}
			}
		}
	}

	t.Logf("dress rehearsal: %d responses compared across %d suite + %d mysekai users; %d mismatched",
		compared, len(users), len(mysekaiUsers), mismatched)
	if mismatched > 0 {
		t.Fatalf("%d responses differ — the read flip is NOT safe", mismatched)
	}
	if compared < 500 {
		t.Fatalf("only %d responses compared; the rehearsal did not exercise enough of the surface", compared)
	}
}

type sampleUser struct {
	id     int64
	server harukiUtils.SupportedDataUploadServer
}

func sampleUsers(ctx context.Context, t *testing.T, db *mongo.Database, coll string, n int64) []sampleUser {
	t.Helper()
	cur, err := db.Collection(coll).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$sample", Value: bson.D{{Key: "size", Value: n}}}},
		{{Key: "$project", Value: bson.D{{Key: "server", Value: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cur.Close(context.Background()) }()
	var out []sampleUser
	for cur.Next(ctx) {
		var row struct {
			ID     int64  `bson:"_id"`
			Server string `bson:"server"`
		}
		if err := cur.Decode(&row); err != nil {
			continue
		}
		if row.Server == "" {
			continue
		}
		out = append(out, sampleUser{id: row.ID, server: harukiUtils.SupportedDataUploadServer(row.Server)})
	}
	return out
}

// canonJSON decodes with UseNumber: a game user id above 2^53 must compare as
// its literal, not through float64.
func canonJSON(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytesReaderFor(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("undecodable response: %v\n%s", err, b)
	}
	return v
}

func topKeys(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func bytesReaderFor(b []byte) io.Reader { return bytes.NewReader(b) }

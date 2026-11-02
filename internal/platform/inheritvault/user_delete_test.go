package inheritvault

import "testing"

func TestDeleteUserFallbackReportsCorruptionAndOutages(t *testing.T) {
	if DeleteUserFallback(t.Context(), nil, "u1") == nil {
		t.Fatal("missing redis succeeded")
	}
	for _, scenario := range []string{"wrong-index-type", "invalid-record", "wrong-owner", "write-failure"} {
		t.Run(scenario, func(t *testing.T) {
			env := newStoreEnv(t)
			rec := fallbackRecord{ID: "entry", UserID: "u1", CredentialSealed: []byte("sealed"), CreatedAt: env.now}
			if scenario == "wrong-owner" {
				rec.UserID = "u2"
			}
			if err := env.store.putFallback(rec); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "wrong-index-type":
				env.redis.Del(fallbackUserPrefix + "u1")
				env.redis.Set(fallbackUserPrefix+"u1", "wrong-type")
			case "invalid-record":
				env.redis.Set(fallbackKeyPrefix+"entry", "not-json")
			case "wrong-owner":
				_, _ = env.redis.SAdd(fallbackUserPrefix+"u1", "entry")
			case "write-failure":
				hook := &failTxPipelines{}
				hook.on.Store(true)
				env.store.redis.AddHook(hook)
			}
			if err := DeleteUserFallback(t.Context(), env.store.redis, "u1"); err == nil {
				t.Fatal("cleanup failure was hidden")
			}
			if !env.redis.Exists(fallbackKeyPrefix + "entry") {
				t.Fatal("failed cleanup removed the record")
			}
		})
	}
}

func TestDeleteUserFallbackKeepsOtherOwners(t *testing.T) {
	env := newStoreEnv(t)
	for _, rec := range []fallbackRecord{{ID: "one", UserID: "u1", CredentialSealed: []byte("sealed-one"), CreatedAt: env.now}, {ID: "two", UserID: "u2", CredentialSealed: []byte("sealed-two"), CreatedAt: env.now}} {
		if err := env.store.putFallback(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteUserFallback(t.Context(), env.store.redis, "u1"); err != nil {
		t.Fatal(err)
	}
	if env.redis.Exists(fallbackKeyPrefix+"one") || env.redis.Exists(fallbackUserPrefix+"u1") {
		t.Fatal("deleted owner fallback remains")
	}
	if !env.redis.Exists(fallbackKeyPrefix + "two") {
		t.Fatal("other owner's fallback was deleted")
	}
	if got, err := env.store.redis.SMembers(t.Context(), fallbackIndexKey).Result(); err != nil || len(got) != 1 || got[0] != "two" {
		t.Fatalf("fallback index=%v error=%v", got, err)
	}
	if err := DeleteUserFallback(t.Context(), env.store.redis, "u1"); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

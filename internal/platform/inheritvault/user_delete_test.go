package inheritvault

import "testing"

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

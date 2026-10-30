package inheritvault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	"github.com/alicebob/miniredis/v2"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

func TestMain(m *testing.M) {
	// Real parameters cost 64 MiB and ~100 ms per derivation.
	DefaultKDFParams = KDFParams{Algorithm: "argon2id", Iterations: 1, MemoryKiB: 64, Parallelism: 1}
	dbWriteBackoff = func(int) time.Duration { return time.Millisecond }
	os.Exit(m.Run())
}

func testMaster(t *testing.T, fill byte) *MasterKey {
	t.Helper()
	m, err := NewMasterKey(bytes.Repeat([]byte{fill}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSealOpenRoundTripAndAssociatedData(t *testing.T) {
	master := testMaster(t, 1)
	k, err := GenerateKey(master, "u1", 1, []byte("hunter22"))
	if err != nil {
		t.Fatal(err)
	}
	priv, err := UnwrapPrivateKey(master, k, []byte("hunter22"))
	if err != nil {
		t.Fatal(err)
	}
	ad := EntryAD("credential", "e1", "u1", "jp", 1)
	blob, err := Seal(k.PublicKey, []byte("secret credential"), ad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("secret credential")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := Open(priv, blob, ad)
	if err != nil || string(got) != "secret credential" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	for name, other := range map[string][]byte{
		"entry":       EntryAD("credential", "e2", "u1", "jp", 1),
		"user":        EntryAD("credential", "e1", "u2", "jp", 1),
		"server":      EntryAD("credential", "e1", "u1", "en", 1),
		"key version": EntryAD("credential", "e1", "u1", "jp", 2),
		"purpose":     EntryAD("code", "e1", "u1", "jp", 1),
	} {
		if _, err := Open(priv, blob, other); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("%s mismatch opened: %v", name, err)
		}
	}
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 1
	if _, err := Open(priv, tampered, ad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered blob opened: %v", err)
	}
}

func TestUnwrapNeedsTheSecretAndTheMasterKey(t *testing.T) {
	master := testMaster(t, 1)
	k, err := GenerateKey(master, "u1", 1, []byte("correct horse"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapPrivateKey(master, k, []byte("wrong horse")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong password: %v", err)
	}
	// The database row with a different master key (a stolen dump without the
	// key file) opens nothing, even with the right password.
	other, _ := NewMasterKey(bytes.Repeat([]byte{2}, 32), 1)
	if _, err := UnwrapPrivateKey(other, k, []byte("correct horse")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other master key: %v", err)
	}
	// A key wrapped for another user cannot be swapped in.
	moved := *k
	moved.UserID = "u2"
	if _, err := UnwrapPrivateKey(master, &moved, []byte("correct horse")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("moved key: %v", err)
	}
	// Master key version mismatch is reported, not guessed.
	v2, _ := NewMasterKey(bytes.Repeat([]byte{1}, 32), 2)
	if _, err := UnwrapPrivateKey(v2, k, []byte("correct horse")); !errors.Is(err, ErrMasterKey) {
		t.Fatalf("master version mismatch: %v", err)
	}
}

// The server holds the master key and the database; without the owner's
// secret, peeling the master layer leaves only a second ciphertext.
func TestServerKeyAloneCannotDecrypt(t *testing.T) {
	master := testMaster(t, 1)
	k, _ := GenerateKey(master, "u1", 1, []byte("owner secret"))
	priv, _ := UnwrapPrivateKey(master, k, []byte("owner secret"))
	inner, err := openGCM(master.aead, k.WrappedPrivate, keyMasterAD("u1", 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(inner, priv) {
		t.Fatal("the master layer alone exposes the private key")
	}
	blob, _ := Seal(k.PublicKey, []byte("cred"), EntryAD("credential", "e", "u1", "jp", 1))
	for _, guess := range [][]byte{inner, priv[:16], bytes.Repeat([]byte{1}, 32)} {
		if len(guess) == 32 && bytes.Equal(guess, priv) {
			continue
		}
		if _, err := Open(guess, blob, EntryAD("credential", "e", "u1", "jp", 1)); err == nil {
			t.Fatal("opened without the private key")
		}
	}
}

func TestLiveSealIsBoundToJobAndOwner(t *testing.T) {
	master := testMaster(t, 1)
	blob := master.SealLive([]byte("code"), "job1", "u1")
	if got, err := master.OpenLive(blob, "job1", "u1"); err != nil || string(got) != "code" {
		t.Fatalf("OpenLive = %q, %v", got, err)
	}
	if _, err := master.OpenLive(blob, "job1", "u2"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other owner: %v", err)
	}
	if _, err := master.OpenLive(blob, "job2", "u1"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other job: %v", err)
	}
}

func TestLoadMasterKeyFile(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{7}, 32)
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, data := range map[string][]byte{
		"raw":    key,
		"hex":    []byte(hex.EncodeToString(key) + "\n"),
		"base64": []byte(base64.StdEncoding.EncodeToString(key) + "\n"),
	} {
		m, err := LoadMasterKeyFile(write(name, data), 3)
		if err != nil || m.Version() != 3 {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(fmt.Sprintf("%v %+v", m, m), hex.EncodeToString(key)) {
			t.Fatalf("%s: key printable", name)
		}
	}
	for name, path := range map[string]string{
		"short":   write("short", key[:31]),
		"long":    write("long", append(key, 1)),
		"missing": filepath.Join(dir, "missing"),
		"empty":   "",
	} {
		if _, err := LoadMasterKeyFile(path, 1); !errors.Is(err, ErrMasterKey) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if _, err := LoadMasterKeyFile(write("v0", key), 0); !errors.Is(err, ErrMasterKey) {
		t.Fatalf("version 0 accepted: %v", err)
	}
}

type storeEnv struct {
	store  *Store
	redis  *miniredis.Miniredis
	dbPath string
	now    time.Time
}

func newStoreEnv(t *testing.T) *storeEnv {
	t.Helper()
	env := &storeEnv{redis: miniredis.RunT(t), dbPath: filepath.Join(t.TempDir(), "vault.db"), now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	db := env.openDB(t)
	for _, id := range []string{"u1", "u2"} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").ExecX(context.Background())
	}
	rdb := redis.NewClient(&redis.Options{Addr: env.redis.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	env.store = NewStore(db, rdb, testMaster(t, 1))
	env.store.now = func() time.Time { return env.now }
	return env
}

func (env *storeEnv) openDB(t *testing.T) *postgresql.Client {
	t.Helper()
	db := enttest.Open(t, "sqlite3", "file:"+env.dbPath+"?_fk=1&_busy_timeout=5000")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func putCredential(t *testing.T, s *Store, u *Unlocked, entryID, cred string) Location {
	t.Helper()
	loc, err := s.PutCredential(context.Background(), CredentialInput{
		EntryID: entryID, UserID: u.UserID, Server: "jp", GameUserID: 1234567890, JobID: "job",
		KeyVersion: u.KeyVersion, PublicKey: u.PublicKey, Credential: []byte(cred),
	})
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// A credential written right after the executing inherit call can be read
// back by a fresh process with only the owner's password.
func TestCredentialSurvivesAProcessRestart(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u, err := env.store.CreateKey(ctx, "u1", SecretKindPassword, []byte("pw-123456"))
	if err != nil {
		t.Fatal(err)
	}
	pub, version := u.PublicKey, u.KeyVersion
	u.Close()
	// The job only kept the public key; the private key is gone.
	if loc := putCredential(t, env.store, &Unlocked{UserID: "u1", KeyVersion: version, PublicKey: pub}, "e1", "cred-1"); loc != LocationDB {
		t.Fatalf("location = %s", loc)
	}

	restarted := NewStore(env.openDB(t), env.store.redis, testMaster(t, 1))
	restarted.now = env.store.now
	entry, err := restarted.Get(ctx, "u1", "e1")
	if err != nil {
		t.Fatal(err)
	}
	u2, err := restarted.Unlock(ctx, "u1", []byte("pw-123456"))
	if err != nil {
		t.Fatal(err)
	}
	defer u2.Close()
	cred, err := u2.OpenCredential(entry)
	if err != nil || string(cred) != "cred-1" {
		t.Fatalf("credential = %q, %v", cred, err)
	}
	if _, err := restarted.Unlock(ctx, "u1", []byte("pw-wrong1")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong password unlock: %v", err)
	}
}

func TestCodeRoundTripAndOwnership(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u, _ := env.store.CreateKey(ctx, "u1", SecretKindPassword, []byte("pw-123456"))
	defer u.Close()
	putCredential(t, env.store, u, "e1", "cred")
	if _, err := env.store.PutCode(ctx, CodeInput{EntryID: "e1", UserID: "u1", Server: "jp", KeyVersion: u.KeyVersion, PublicKey: u.PublicKey, InheritID: "NEWCODE000000001", Password: "pw12345678"}); err != nil {
		t.Fatal(err)
	}
	e, err := env.store.Get(ctx, "u1", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if !e.CodeLive(env.now) {
		t.Fatal("code not live")
	}
	id, pw, err := u.OpenCode(e)
	if err != nil || id != "NEWCODE000000001" || pw != "pw12345678" {
		t.Fatalf("OpenCode = %q %q %v", id, pw, err)
	}
	if _, err := env.store.Get(ctx, "u2", "e1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner Get: %v", err)
	}
	if err := env.store.Destroy(ctx, "u2", "e1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner Destroy: %v", err)
	}
	if _, err := env.store.PutCode(ctx, CodeInput{EntryID: "e1", UserID: "u2", Server: "jp", KeyVersion: 1, PublicKey: u.PublicKey, InheritID: "x", Password: "y"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner PutCode: %v", err)
	}
}

// With PostgreSQL down, the sealed credential goes to Redis and moves into
// PostgreSQL once it is back.
func TestDatabaseDownUsesRedisFallbackAndReconciles(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u, _ := env.store.CreateKey(ctx, "u1", SecretKindPassword, []byte("pw-123456"))
	defer u.Close()
	healthy := env.store.db
	broken := env.openDB(t)
	_ = broken.Close()
	env.store.db = broken

	if loc := putCredential(t, env.store, u, "e1", "cred-fallback"); loc != LocationFallback {
		t.Fatalf("location = %s, want fallback", loc)
	}
	if loc, err := env.store.PutCode(ctx, CodeInput{EntryID: "e1", UserID: "u1", Server: "jp", KeyVersion: u.KeyVersion, PublicKey: u.PublicKey, InheritID: "CODE", Password: "pw12345678"}); err != nil || loc != LocationFallback {
		t.Fatalf("PutCode = %s, %v", loc, err)
	}
	if !env.redis.Exists(fallbackKeyPrefix + "e1") {
		t.Fatal("no fallback record")
	}
	if ttl := env.redis.TTL(fallbackKeyPrefix + "e1"); ttl <= 0 || ttl > CredentialRetention {
		t.Fatalf("fallback TTL = %s", ttl)
	}
	raw, _ := env.redis.Get(fallbackKeyPrefix + "e1")
	if strings.Contains(raw, "cred-fallback") || strings.Contains(raw, "pw12345678") {
		t.Fatal("fallback record holds plaintext")
	}

	env.store.db = healthy
	entries, err := env.store.List(ctx, "u1")
	if err != nil || len(entries) != 1 || entries[0].Pending {
		t.Fatalf("List = %+v, %v", entries, err)
	}
	if env.redis.Exists(fallbackKeyPrefix + "e1") {
		t.Fatal("fallback record not removed after reconcile")
	}
	cred, err := u.OpenCredential(entries[0])
	if err != nil || string(cred) != "cred-fallback" {
		t.Fatalf("credential = %q, %v", cred, err)
	}
	if id, _, err := u.OpenCode(entries[0]); err != nil || id != "CODE" {
		t.Fatalf("code = %q, %v", id, err)
	}
}

// The reaper reconciles too, for owners who never come back to list.
func TestReapReconcilesAndPurges(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u, _ := env.store.CreateKey(ctx, "u1", SecretKindPassword, []byte("pw-123456"))
	defer u.Close()
	putCredential(t, env.store, u, "keep", "a")
	putCredential(t, env.store, u, "destroy", "b")
	_, _ = env.store.PutCode(ctx, CodeInput{EntryID: "keep", UserID: "u1", Server: "jp", KeyVersion: 1, PublicKey: u.PublicKey, InheritID: "c", Password: "p"})
	if err := env.store.Destroy(ctx, "u1", "destroy"); err != nil {
		t.Fatal(err)
	}
	if e, _ := env.store.db.InheritVaultEntry.Get(ctx, "destroy"); len(e.CredentialSealed) != 0 {
		t.Fatal("destroy left the credential readable")
	}
	healthy := env.store.db
	broken := env.openDB(t)
	_ = broken.Close()
	env.store.db = broken
	putCredential(t, env.store, u, "parked", "c")
	env.store.db = healthy

	env.now = env.now.Add(25 * time.Hour)
	res, err := env.store.Reap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 || res.CodesCleared != 1 || res.Reconciled != 1 {
		t.Fatalf("first reap = %+v", res)
	}
	e, err := env.store.Get(ctx, "u1", "keep")
	if err != nil || e.CodeSealed != nil {
		t.Fatalf("keep = %+v, %v", e, err)
	}
	env.now = env.now.Add(CredentialRetention)
	res, err = env.store.Reap(ctx)
	if err != nil || res.Deleted != 2 {
		t.Fatalf("second reap = %+v, %v", res, err)
	}
	if n := env.store.db.InheritVaultEntry.Query().CountX(ctx); n != 0 {
		t.Fatalf("%d rows left", n)
	}
}

// A changed Toolbox password is carried over with the previous one; the old
// secret stops working.
func TestRewrapAfterPasswordChange(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u, _ := env.store.CreateKey(ctx, "u1", SecretKindPassword, []byte("old-password"))
	putCredential(t, env.store, u, "e1", "cred")
	u.Close()
	if _, err := env.store.Unlock(ctx, "u1", []byte("new-password")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("new password before rewrap: %v", err)
	}
	u2, err := env.store.Rewrap(ctx, "u1", []byte("old-password"), []byte("new-password"))
	if err != nil {
		t.Fatal(err)
	}
	defer u2.Close()
	e, _ := env.store.Get(ctx, "u1", "e1")
	if cred, err := u2.OpenCredential(e); err != nil || string(cred) != "cred" {
		t.Fatalf("after rewrap = %q, %v", cred, err)
	}
	if _, err := env.store.Unlock(ctx, "u1", []byte("old-password")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("old password after rewrap: %v", err)
	}
	if _, err := env.store.Rewrap(ctx, "u1", []byte("wrong"), []byte("x-password")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("rewrap with wrong old password: %v", err)
	}
}

// After a reset the old key and every entry sealed to it are gone, parked
// records included; a new key starts clean.
func TestResetKeyDiscardsEverything(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u, _ := env.store.CreateKey(ctx, "u1", SecretKindPIN, []byte("pin-12345678"))
	defer u.Close()
	putCredential(t, env.store, u, "e1", "cred")
	healthy := env.store.db
	broken := env.openDB(t)
	_ = broken.Close()
	env.store.db = broken
	putCredential(t, env.store, u, "parked", "cred")
	env.store.db = healthy
	other, _ := env.store.CreateKey(ctx, "u2", SecretKindPassword, []byte("pw-other1"))
	defer other.Close()
	putCredential(t, env.store, other, "e2", "cred")

	n, err := env.store.ResetKey(ctx, "u1")
	if err != nil || n != 2 {
		t.Fatalf("ResetKey = %d, %v", n, err)
	}
	if _, err := env.store.KeyInfo(ctx, "u1"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("key after reset: %v", err)
	}
	if entries, _ := env.store.List(ctx, "u1"); len(entries) != 0 {
		t.Fatalf("entries after reset: %+v", entries)
	}
	if env.redis.Exists(fallbackKeyPrefix + "parked") {
		t.Fatal("parked record survived the reset")
	}
	if _, err := env.store.Get(ctx, "u2", "e2"); err != nil {
		t.Fatalf("other user's entry: %v", err)
	}
	fresh, err := env.store.CreateKey(ctx, "u1", SecretKindPassword, []byte("pw-123456"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := u.OpenCredential(&Entry{ID: "e1", UserID: "u1", Server: "jp", KeyVersion: 1, CredentialSealed: []byte{1}}); err == nil {
		t.Fatal("old unlocked key still opens")
	}
}

func TestStaleKeyVersionIsReported(t *testing.T) {
	u := &Unlocked{UserID: "u1", KeyVersion: 2}
	if _, err := u.OpenCredential(&Entry{KeyVersion: 1}); !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v", err)
	}
}

func TestMaskGameUserID(t *testing.T) {
	if got := MaskGameUserID(164337024457871363); got != "16*************363" {
		t.Fatalf("got %q", got)
	}
	if got := MaskGameUserID(123); got != "***" {
		t.Fatalf("got %q", got)
	}
}

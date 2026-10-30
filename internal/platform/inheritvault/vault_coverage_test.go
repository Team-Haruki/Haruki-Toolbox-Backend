package inheritvault

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/inheritvaultkey"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"entgo.io/ent"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var errInjected = errors.New("injected failure")

// brokenDB returns a client whose every query fails, standing in for a
// PostgreSQL outage.
func (env *storeEnv) brokenDB(t *testing.T) *postgresql.Client {
	t.Helper()
	db := env.openDB(t)
	_ = db.Close()
	return db
}

// withDBDown runs fn with the store's database unreachable.
func (env *storeEnv) withDBDown(t *testing.T, fn func()) {
	t.Helper()
	healthy := env.store.db
	env.store.db = env.brokenDB(t)
	defer func() { env.store.db = healthy }()
	fn()
}

// failEntryOps makes InheritVaultEntry mutations of kind op fail while the
// returned switch is on.
func failEntryOps(db *postgresql.Client, op ent.Op) *atomic.Bool {
	on := new(atomic.Bool)
	db.InheritVaultEntry.Use(func(next postgresql.Mutator) postgresql.Mutator {
		return postgresql.MutateFunc(func(ctx context.Context, m postgresql.Mutation) (postgresql.Value, error) {
			if on.Load() && m.Op().Is(op) {
				return nil, errInjected
			}
			return next.Mutate(ctx, m)
		})
	})
	return on
}

// failKeyOps is failEntryOps for InheritVaultKey.
func failKeyOps(db *postgresql.Client, op ent.Op) *atomic.Bool {
	on := new(atomic.Bool)
	db.InheritVaultKey.Use(func(next postgresql.Mutator) postgresql.Mutator {
		return postgresql.MutateFunc(func(ctx context.Context, m postgresql.Mutation) (postgresql.Value, error) {
			if on.Load() && m.Op().Is(op) {
				return nil, errInjected
			}
			return next.Mutate(ctx, m)
		})
	})
	return on
}

// failTxPipelines makes every Redis MULTI/EXEC (and pipeline) fail while on.
type failTxPipelines struct{ on atomic.Bool }

func (f *failTxPipelines) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (f *failTxPipelines) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (f *failTxPipelines) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if f.on.Load() {
			return errInjected
		}
		return next(ctx, cmds)
	}
}

func newUser(t *testing.T, env *storeEnv, userID, secret string) *Unlocked {
	t.Helper()
	u, err := env.store.CreateKey(context.Background(), userID, SecretKindPassword, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	return u
}

func codeInput(u *Unlocked, entryID, inheritID string) CodeInput {
	return CodeInput{EntryID: entryID, UserID: u.UserID, Server: "jp", KeyVersion: u.KeyVersion, PublicKey: u.PublicKey, InheritID: inheritID, Password: "pw12345678"}
}

// ---- crypto ----

func TestNewMasterKeyRejectsBadLengthsAndVersions(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := NewMasterKey(make([]byte, n), 1); !errors.Is(err, ErrMasterKey) {
			t.Fatalf("%d-byte key: err = %v", n, err)
		}
	}
	for _, v := range []int{0, -1} {
		if _, err := NewMasterKey(make([]byte, 32), v); !errors.Is(err, ErrMasterKey) {
			t.Fatalf("version %d: err = %v", v, err)
		}
	}
	if _, err := newGCM(make([]byte, 7)); err == nil {
		t.Fatal("newGCM accepted a 7-byte key")
	}
}

func TestOpenLiveRejectsTruncatedBlobs(t *testing.T) {
	master := testMaster(t, 1)
	blob := master.SealLive([]byte("code"), "job", "u1")
	for _, b := range [][]byte{nil, blob[:nonceSize], blob[:len(blob)-1]} {
		if _, err := master.OpenLive(b, "job", "u1"); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("%d-byte blob: err = %v", len(b), err)
		}
	}
}

func TestKDFParamsOutsideTheSupportedRangeAreRefused(t *testing.T) {
	master := testMaster(t, 1)
	k, err := GenerateKey(master, "u1", 1, []byte("secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	good := k.KDF
	if err := good.validate(); err != nil {
		t.Fatalf("generated params invalid: %v", err)
	}
	for name, mutate := range map[string]func(*KDFParams){
		"algorithm":        func(p *KDFParams) { p.Algorithm = "scrypt" },
		"short salt":       func(p *KDFParams) { p.Salt = p.Salt[:saltSize-1] },
		"zero iterations":  func(p *KDFParams) { p.Iterations = 0 },
		"many iterations":  func(p *KDFParams) { p.Iterations = maximumIterations + 1 },
		"memory too small": func(p *KDFParams) { p.Parallelism = 4; p.MemoryKiB = 31 },
		"memory too large": func(p *KDFParams) { p.MemoryKiB = maximumMemoryKiB + 1 },
		"zero parallelism": func(p *KDFParams) { p.Parallelism = 0 },
		"many lanes":       func(p *KDFParams) { p.Parallelism = maximumParallelism + 1; p.MemoryKiB = 1024 },
	} {
		p := good
		p.Salt = append([]byte(nil), good.Salt...)
		mutate(&p)
		if err := p.validate(); err == nil {
			t.Fatalf("%s: validate accepted %+v", name, p)
		}
		// A stored row with such parameters is refused before any derivation.
		bad := *k
		bad.KDF = p
		if _, err := UnwrapPrivateKey(master, &bad, []byte("secret-1")); err == nil || errors.Is(err, ErrDecrypt) {
			t.Fatalf("%s: unwrap err = %v, want a parameter error", name, err)
		}
	}
}

func TestSecretLengthIsBounded(t *testing.T) {
	master := testMaster(t, 1)
	for name, secret := range map[string][]byte{
		"empty":    nil,
		"too long": bytes.Repeat([]byte{'a'}, maxSecretBytes+1),
	} {
		if _, err := GenerateKey(master, "u1", 1, secret); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("%s secret: GenerateKey err = %v", name, err)
		}
	}
	longest := bytes.Repeat([]byte{'a'}, maxSecretBytes)
	k, err := GenerateKey(master, "u1", 1, longest)
	if err != nil {
		t.Fatalf("%d-byte secret refused: %v", maxSecretBytes, err)
	}
	if _, err := UnwrapPrivateKey(master, k, nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("empty secret unwrap: %v", err)
	}
	priv, err := UnwrapPrivateKey(master, k, longest)
	if err != nil || len(priv) != keySize {
		t.Fatalf("unwrap = %d bytes, %v", len(priv), err)
	}
}

func TestWrappingNeedsAMasterKeyAndAKnownScheme(t *testing.T) {
	master := testMaster(t, 1)
	if _, err := GenerateKey(nil, "u1", 1, []byte("secret-1")); !errors.Is(err, ErrMasterKey) {
		t.Fatalf("GenerateKey without master: %v", err)
	}
	k, err := GenerateKey(master, "u1", 1, []byte("secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	priv, err := UnwrapPrivateKey(master, k, []byte("secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rewrap(nil, k, priv, []byte("secret-2")); !errors.Is(err, ErrMasterKey) {
		t.Fatalf("Rewrap without master: %v", err)
	}
	if _, err := UnwrapPrivateKey(nil, k, []byte("secret-1")); !errors.Is(err, ErrMasterKey) {
		t.Fatalf("unwrap without master: %v", err)
	}
	if _, err := UnwrapPrivateKey(master, nil, []byte("secret-1")); !errors.Is(err, ErrMasterKey) {
		t.Fatalf("unwrap nil key: %v", err)
	}
	other := *k
	other.WrapScheme = "x25519-argon2id-v0"
	if _, err := UnwrapPrivateKey(master, &other, []byte("secret-1")); err == nil || !strings.Contains(err.Error(), "unsupported key wrap scheme") {
		t.Fatalf("unknown scheme: %v", err)
	}
	truncated := *k
	truncated.WrappedPrivate = k.WrappedPrivate[:nonceSize]
	if _, err := UnwrapPrivateKey(master, &truncated, []byte("secret-1")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("truncated wrapped key: %v", err)
	}
}

// A wrapped value that opens but is not a 32-byte X25519 key is not handed
// out as one.
func TestUnwrapRejectsAWrongSizedPrivateKey(t *testing.T) {
	master := testMaster(t, 1)
	params := DefaultKDFParams
	params.Salt = bytes.Repeat([]byte{9}, saltSize)
	k, err := wrapPrivateKey(master, "u1", 1, bytes.Repeat([]byte{5}, 16), bytes.Repeat([]byte{6}, keySize), []byte("secret-1"), params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapPrivateKey(master, k, []byte("secret-1")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("err = %v", err)
	}
}

func TestSealRejectsUnusablePublicKeys(t *testing.T) {
	ad := EntryAD(purposeCredential, "e1", "u1", "jp", 1)
	if _, err := Seal(make([]byte, 31), []byte("x"), ad); err == nil {
		t.Fatal("31-byte public key accepted")
	}
	// The all-zero point yields an all-zero shared secret, which X25519 refuses.
	if _, err := Seal(make([]byte, keySize), []byte("x"), ad); err == nil {
		t.Fatal("low-order public key accepted")
	}
}

func TestOpenRejectsMalformedInput(t *testing.T) {
	master := testMaster(t, 1)
	k, _ := GenerateKey(master, "u1", 1, []byte("secret-1"))
	priv, err := UnwrapPrivateKey(master, k, []byte("secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	ad := EntryAD(purposeCredential, "e1", "u1", "jp", 1)
	blob, err := Seal(k.PublicKey, []byte("cred"), ad)
	if err != nil {
		t.Fatal(err)
	}
	wrongVersion := append([]byte(nil), blob...)
	wrongVersion[0] = sealVersion + 1
	lowOrderEph := append([]byte(nil), blob...)
	clear(lowOrderEph[1 : 1+keySize])
	for name, c := range map[string]struct{ priv, blob []byte }{
		"short blob":          {priv, blob[:keySize]},
		"wrong version":       {priv, wrongVersion},
		"short private key":   {priv[:16], blob},
		"low-order ephemeral": {priv, lowOrderEph},
		"no ciphertext":       {priv, blob[:1+keySize]},
	} {
		if _, err := Open(c.priv, c.blob, ad); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if got, err := Open(priv, blob, ad); err != nil || string(got) != "cred" {
		t.Fatalf("intact blob = %q, %v", got, err)
	}
}

// ---- store: availability ----

func TestUnavailableStoreRefusesEveryCall(t *testing.T) {
	ctx := context.Background()
	env := newStoreEnv(t)
	for name, s := range map[string]*Store{
		"nil store": nil,
		"no master": NewStore(env.store.db, env.store.redis, nil),
		"no db":     NewStore(nil, env.store.redis, testMaster(t, 1)),
	} {
		if s.Available() {
			t.Fatalf("%s: Available", name)
		}
		if s == nil {
			if s.Master() != nil {
				t.Fatalf("%s: Master not nil", name)
			}
			continue
		}
		if _, err := s.KeyInfo(ctx, "u1"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s KeyInfo: %v", name, err)
		}
		if _, err := s.CreateKey(ctx, "u1", SecretKindPIN, []byte("1234")); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s CreateKey: %v", name, err)
		}
		if _, err := s.Unlock(ctx, "u1", []byte("1234")); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s Unlock: %v", name, err)
		}
		if _, err := s.Rewrap(ctx, "u1", []byte("1234"), []byte("5678")); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s Rewrap: %v", name, err)
		}
		if _, err := s.ResetKey(ctx, "u1"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s ResetKey: %v", name, err)
		}
		if _, err := s.PutCredential(ctx, CredentialInput{EntryID: "e1", UserID: "u1"}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s PutCredential: %v", name, err)
		}
		if _, err := s.PutCode(ctx, CodeInput{EntryID: "e1", UserID: "u1"}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s PutCode: %v", name, err)
		}
		if _, err := s.List(ctx, "u1"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s List: %v", name, err)
		}
		if _, err := s.Get(ctx, "u1", "e1"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s Get: %v", name, err)
		}
		if err := s.Destroy(ctx, "u1", "e1"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s Destroy: %v", name, err)
		}
		if n, err := s.Reconcile(ctx, ""); n != 0 || err != nil {
			t.Fatalf("%s Reconcile = %d, %v", name, n, err)
		}
		if res, err := s.Reap(ctx); res != (ReapResult{}) || err != nil {
			t.Fatalf("%s Reap = %+v, %v", name, res, err)
		}
	}
	if m := env.store.Master(); m == nil || m.Version() != 1 {
		t.Fatalf("Master = %v", m)
	}
}

// ---- store: keys ----

func TestKeyInfoDescribesTheStoredKey(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	if _, err := env.store.KeyInfo(ctx, "u1"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("before CreateKey: %v", err)
	}
	u, err := env.store.CreateKey(ctx, "u1", SecretKindPIN, []byte("pin-1234"))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	info, err := env.store.KeyInfo(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if info.KeyVersion != u.KeyVersion || info.SecretKind != SecretKindPIN || !bytes.Equal(info.PublicKey, u.PublicKey) ||
		!info.CreatedAt.Equal(env.now) || !info.UpdatedAt.Equal(env.now) {
		t.Fatalf("KeyInfo = %+v", info)
	}
	if u.SecretKind != SecretKindPIN || u.UserID != "u1" {
		t.Fatalf("Unlocked = %+v", u)
	}
	u.Close()
	if u.priv != nil {
		t.Fatal("Close kept the private key")
	}
	var nilUnlocked *Unlocked
	nilUnlocked.Close() // must not panic
}

func TestCreateKeyRefusesBadInputAndDuplicates(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	if _, err := env.store.CreateKey(ctx, "u1", "passphrase", []byte("pw-123456")); err == nil || !strings.Contains(err.Error(), "unknown secret kind") {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := env.store.CreateKey(ctx, "u1", SecretKindPassword, nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("empty secret: %v", err)
	}
	if _, err := env.store.KeyInfo(ctx, "u1"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("a refused CreateKey stored a key: %v", err)
	}
	first := newUser(t, env, "u1", "pw-123456")
	if _, err := env.store.CreateKey(ctx, "u1", SecretKindPassword, []byte("pw-other1")); err == nil {
		t.Fatal("second CreateKey succeeded")
	}
	// The original key is untouched.
	again, err := env.store.Unlock(ctx, "u1", []byte("pw-123456"))
	if err != nil || !bytes.Equal(again.PublicKey, first.PublicKey) {
		t.Fatalf("original key after duplicate = %v", err)
	}
	again.Close()
}

func TestUnlockAndRewrapWithoutAKey(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	if _, err := env.store.Unlock(ctx, "u1", []byte("pw-123456")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Unlock: %v", err)
	}
	if _, err := env.store.Rewrap(ctx, "u1", []byte("pw-123456"), []byte("pw-654321")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Rewrap: %v", err)
	}
}

func TestCorruptKDFParametersAreReported(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	newUser(t, env, "u1", "pw-123456")
	env.store.db.InheritVaultKey.Update().Where(inheritvaultkey.UserIDEQ("u1")).SetKdfParams("{not json").ExecX(ctx)
	if _, err := env.store.Unlock(ctx, "u1", []byte("pw-123456")); err == nil || !strings.Contains(err.Error(), "bad KDF parameters") {
		t.Fatalf("Unlock: %v", err)
	}
	if _, err := env.store.Rewrap(ctx, "u1", []byte("pw-123456"), []byte("pw-654321")); err == nil || !strings.Contains(err.Error(), "bad KDF parameters") {
		t.Fatalf("Rewrap: %v", err)
	}
}

func TestRewrapFailuresLeaveTheOldSecretWorking(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	newUser(t, env, "u1", "pw-123456")
	if _, err := env.store.Rewrap(ctx, "u1", []byte("pw-123456"), nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("empty new secret: %v", err)
	}
	fail := failKeyOps(env.store.db, ent.OpUpdate)
	fail.Store(true)
	if _, err := env.store.Rewrap(ctx, "u1", []byte("pw-123456"), []byte("pw-654321")); !errors.Is(err, errInjected) {
		t.Fatalf("database error: %v", err)
	}
	fail.Store(false)
	u, err := env.store.Unlock(ctx, "u1", []byte("pw-123456"))
	if err != nil {
		t.Fatalf("old secret after failed rewraps: %v", err)
	}
	u.Close()
}

// If the key row changes version between the read and the write, the rewrap
// must not overwrite it.
func TestRewrapLosesARaceWithAKeyReplacement(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	newUser(t, env, "u1", "pw-123456")
	var armed atomic.Bool
	armed.Store(true)
	db := env.store.db
	db.InheritVaultKey.Use(func(next postgresql.Mutator) postgresql.Mutator {
		return postgresql.MutateFunc(func(ctx context.Context, m postgresql.Mutation) (postgresql.Value, error) {
			if m.Op().Is(ent.OpUpdate) && armed.CompareAndSwap(true, false) {
				db.InheritVaultKey.Update().Where(inheritvaultkey.UserIDEQ("u1")).SetKeyVersion(2).ExecX(ctx)
			}
			return next.Mutate(ctx, m)
		})
	})
	if _, err := env.store.Rewrap(ctx, "u1", []byte("pw-123456"), []byte("pw-654321")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("err = %v, want ErrNoKey", err)
	}
	row := db.InheritVaultKey.Query().Where(inheritvaultkey.UserIDEQ("u1")).OnlyX(ctx)
	if row.KeyVersion != 2 {
		t.Fatalf("key version = %d", row.KeyVersion)
	}
}

func TestResetKeyReportsDatabaseErrors(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "e1", "cred")

	failEntries := failEntryOps(env.store.db, ent.OpDelete)
	failEntries.Store(true)
	if n, err := env.store.ResetKey(ctx, "u1"); n != 0 || !errors.Is(err, errInjected) {
		t.Fatalf("entry delete failure: %d, %v", n, err)
	}
	failEntries.Store(false)
	if _, err := env.store.KeyInfo(ctx, "u1"); err != nil {
		t.Fatalf("key removed although entries were not: %v", err)
	}

	failKeys := failKeyOps(env.store.db, ent.OpDelete)
	failKeys.Store(true)
	if n, err := env.store.ResetKey(ctx, "u1"); n != 1 || !errors.Is(err, errInjected) {
		t.Fatalf("key delete failure: %d, %v", n, err)
	}
}

func TestNewEntryIDIsAFreshUUID(t *testing.T) {
	a, b := NewEntryID(), NewEntryID()
	if a == b {
		t.Fatal("NewEntryID repeated")
	}
	for _, id := range []string{a, b} {
		if _, err := uuid.Parse(id); err != nil {
			t.Fatalf("%q: %v", id, err)
		}
	}
}

// ---- store: writes ----

func TestPutRefusesAnUnusablePublicKey(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	bad := []byte{1, 2, 3}
	if _, err := env.store.PutCredential(ctx, CredentialInput{EntryID: "e1", UserID: "u1", Server: "jp", KeyVersion: 1, PublicKey: bad, Credential: []byte("c")}); err == nil {
		t.Fatal("PutCredential sealed to a bad key")
	}
	if _, err := env.store.PutCode(ctx, CodeInput{EntryID: "e1", UserID: "u1", Server: "jp", KeyVersion: 1, PublicKey: bad, InheritID: "i", Password: "p"}); err == nil {
		t.Fatal("PutCode sealed to a bad key")
	}
	if env.store.db.InheritVaultEntry.Query().CountX(ctx) != 0 || len(env.redis.Keys()) != 0 {
		t.Fatal("a refused write left data behind")
	}
}

func TestWritesFailWhenBothDatabaseAndRedisAreUnavailable(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "e1", "cred")
	noRedis := NewStore(env.brokenDB(t), nil, env.store.master)
	noRedis.now = env.store.now
	_, err := noRedis.PutCredential(ctx, CredentialInput{EntryID: "e2", UserID: "u1", Server: "jp", KeyVersion: u.KeyVersion, PublicKey: u.PublicKey, Credential: []byte("c")})
	if err == nil || !strings.Contains(err.Error(), "no redis for fallback") {
		t.Fatalf("PutCredential: %v", err)
	}
	_, err = noRedis.PutCode(ctx, codeInput(u, "e1", "CODE"))
	if err == nil || !strings.Contains(err.Error(), "no redis for fallback") {
		t.Fatalf("PutCode: %v", err)
	}
	if n, err := noRedis.Reconcile(ctx, ""); n != 0 || err != nil {
		t.Fatalf("Reconcile without redis = %d, %v", n, err)
	}
	if rec, err := noRedis.getFallback(ctx, "e1"); rec != nil || err != nil {
		t.Fatalf("getFallback without redis = %+v, %v", rec, err)
	}
}

// PostgreSQL holding the entry but refusing the code update parks a
// code-only record; once PostgreSQL is back it is applied to the row.
func TestCodeParkedWhileDatabaseDownIsAppliedLater(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "e1", "cred")
	env.withDBDown(t, func() {
		loc, err := env.store.PutCode(ctx, codeInput(u, "e1", "PARKEDCODE"))
		if err != nil || loc != LocationFallback {
			t.Fatalf("PutCode = %s, %v", loc, err)
		}
	})
	rec, err := env.store.getFallback(ctx, "e1")
	if err != nil || rec == nil || len(rec.CredentialSealed) != 0 || len(rec.CodeSealed) == 0 {
		t.Fatalf("parked record = %+v, %v", rec, err)
	}
	if ttl := env.redis.TTL(fallbackKeyPrefix + "e1"); ttl != CredentialRetention {
		t.Fatalf("code-only record TTL = %s, want %s", ttl, CredentialRetention)
	}
	if n, err := env.store.Reconcile(ctx, ""); n != 1 || err != nil {
		t.Fatalf("Reconcile = %d, %v", n, err)
	}
	e, err := env.store.Get(ctx, "u1", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if id, _, err := u.OpenCode(e); err != nil || id != "PARKEDCODE" {
		t.Fatalf("code = %q, %v", id, err)
	}
	if cred, err := u.OpenCredential(e); err != nil || string(cred) != "cred" {
		t.Fatalf("credential = %q, %v", cred, err)
	}
}

// A parked code older than the one PostgreSQL already holds is dropped.
func TestOlderParkedCodeDoesNotOverwriteANewerOne(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "e1", "cred")
	env.withDBDown(t, func() {
		if _, err := env.store.PutCode(ctx, codeInput(u, "e1", "OLDCODE")); err != nil {
			t.Fatal(err)
		}
	})
	env.now = env.now.Add(time.Hour)
	if loc, err := env.store.PutCode(ctx, codeInput(u, "e1", "NEWCODE")); err != nil || loc != LocationDB {
		t.Fatalf("PutCode = %s, %v", loc, err)
	}
	if n, err := env.store.Reconcile(ctx, "u1"); n != 1 || err != nil {
		t.Fatalf("Reconcile = %d, %v", n, err)
	}
	if env.redis.Exists(fallbackKeyPrefix + "e1") {
		t.Fatal("stale parked code kept")
	}
	e, _ := env.store.Get(ctx, "u1", "e1")
	if id, _, err := u.OpenCode(e); err != nil || id != "NEWCODE" {
		t.Fatalf("code = %q, %v", id, err)
	}
}

func TestPutCodeOnAnEntryParkedForSomeoneElse(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	u2 := newUser(t, env, "u2", "pw-other1")
	env.withDBDown(t, func() { putCredential(t, env.store, u, "e1", "cred") })
	if _, err := env.store.PutCode(ctx, codeInput(u2, "e1", "HIJACK")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner: %v", err)
	}
	if _, err := env.store.PutCode(ctx, codeInput(u, "missing", "X")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown entry: %v", err)
	}
	// The owner's own code update goes onto the parked record.
	if loc, err := env.store.PutCode(ctx, codeInput(u, "e1", "OWNCODE")); err != nil || loc != LocationFallback {
		t.Fatalf("owner PutCode = %s, %v", loc, err)
	}
	rec, _ := env.store.getFallback(ctx, "e1")
	if rec == nil || rec.UserID != "u1" || len(rec.CredentialSealed) == 0 || len(rec.CodeSealed) == 0 {
		t.Fatalf("parked record = %+v", rec)
	}
}

// A job whose context has already ended still gets its credential parked:
// the waits stop, but nothing is lost.
func TestCancelledContextStillParksTheCredential(t *testing.T) {
	env := newStoreEnv(t)
	u := newUser(t, env, "u1", "pw-123456")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env.withDBDown(t, func() {
		loc, err := env.store.PutCredential(ctx, CredentialInput{EntryID: "e1", UserID: "u1", Server: "jp", KeyVersion: u.KeyVersion, PublicKey: u.PublicKey, Credential: []byte("c")})
		if err != nil || loc != LocationFallback {
			t.Fatalf("PutCredential = %s, %v", loc, err)
		}
	})
	if !env.redis.Exists(fallbackKeyPrefix + "e1") {
		t.Fatal("credential not parked")
	}
}

func TestRetryDBStopsWaitingWhenTheContextEnds(t *testing.T) {
	saved := dbWriteBackoff
	dbWriteBackoff = func(int) time.Duration { return time.Hour }
	t.Cleanup(func() { dbWriteBackoff = saved })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := (&Store{}).retryDB(ctx, func(context.Context) error { calls++; return errInjected })
	if calls != 1 || !errors.Is(err, errInjected) || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls = %d, err = %v", calls, err)
	}
	calls = 0
	err = (&Store{}).retryDB(context.Background(), func(context.Context) error {
		calls++
		if calls == 1 {
			return nil
		}
		return errInjected
	})
	if err != nil || calls != 1 {
		t.Fatalf("success on first try: calls = %d, err = %v", calls, err)
	}
}

func TestPutFallbackGivesUpAfterRepeatedRedisErrors(t *testing.T) {
	env := newStoreEnv(t)
	hook := &failTxPipelines{}
	env.store.redis.AddHook(hook)
	hook.on.Store(true)
	err := env.store.putFallback(fallbackRecord{ID: "e1", UserID: "u1", CredentialExpiresAt: env.now.Add(-time.Hour)})
	if !errors.Is(err, errInjected) {
		t.Fatalf("err = %v", err)
	}
	if env.redis.Exists(fallbackKeyPrefix + "e1") {
		t.Fatal("record written despite the error")
	}
	hook.on.Store(false)
	// An already-expired credential still gets a positive TTL.
	if err := env.store.putFallback(fallbackRecord{ID: "e1", UserID: "u1", CredentialExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if ttl := env.redis.TTL(fallbackKeyPrefix + "e1"); ttl != CredentialRetention {
		t.Fatalf("TTL = %s", ttl)
	}
	if !env.redis.Exists(fallbackIndexKey) || !env.redis.Exists(fallbackUserPrefix+"u1") {
		t.Fatal("indexes not written")
	}
}

// ---- store: fallback reads and reconcile ----

func TestGetFallbackHandlesMissingGarbledAndFailingRecords(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	if rec, err := env.store.getFallback(ctx, "missing"); rec != nil || err != nil {
		t.Fatalf("missing = %+v, %v", rec, err)
	}
	if err := env.redis.Set(fallbackKeyPrefix+"garbled", "{not json"); err != nil {
		t.Fatal(err)
	}
	if rec, err := env.store.getFallback(ctx, "garbled"); rec != nil || err == nil {
		t.Fatalf("garbled = %+v, %v", rec, err)
	}
	env.redis.SetError("LOADING")
	defer env.redis.SetError("")
	if rec, err := env.store.getFallback(ctx, "missing"); rec != nil || err == nil {
		t.Fatalf("redis error = %+v, %v", rec, err)
	}
}

func TestReconcileErrorsKeepTheParkedRecords(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	env.withDBDown(t, func() {
		putCredential(t, env.store, u, "e1", "cred")
		if _, err := env.store.Reconcile(ctx, "u1"); err == nil {
			t.Fatal("Reconcile with PostgreSQL down reported no error")
		}
	})
	if !env.redis.Exists(fallbackKeyPrefix + "e1") {
		t.Fatal("record dropped although it was not moved")
	}

	env.redis.SetError("LOADING")
	if n, err := env.store.Reconcile(ctx, "u1"); n != 0 || err == nil {
		t.Fatalf("redis error = %d, %v", n, err)
	}
	env.redis.SetError("")

	if err := env.redis.Set(fallbackKeyPrefix+"e1", "{not json"); err != nil {
		t.Fatal(err)
	}
	if n, err := env.store.Reconcile(ctx, "u1"); n != 0 || err == nil {
		t.Fatalf("garbled record = %d, %v", n, err)
	}
	if env.store.db.InheritVaultEntry.Query().CountX(ctx) != 0 {
		t.Fatal("garbled record reached PostgreSQL")
	}
}

// When the move succeeds but the Redis cleanup fails, the row is in
// PostgreSQL and the next pass removes the leftover record.
func TestReconcileReportsAFailedCleanupAndRecovers(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	env.withDBDown(t, func() { putCredential(t, env.store, u, "e1", "cred") })
	hook := &failTxPipelines{}
	env.store.redis.AddHook(hook)
	hook.on.Store(true)
	if n, err := env.store.Reconcile(ctx, ""); n != 0 || !errors.Is(err, errInjected) {
		t.Fatalf("Reconcile = %d, %v", n, err)
	}
	hook.on.Store(false)
	if env.store.db.InheritVaultEntry.Query().CountX(ctx) != 1 {
		t.Fatal("row not inserted")
	}
	if n, err := env.store.Reconcile(ctx, ""); n != 1 || err != nil {
		t.Fatalf("second Reconcile = %d, %v", n, err)
	}
	if env.redis.Exists(fallbackKeyPrefix + "e1") {
		t.Fatal("leftover record kept")
	}
	e, err := env.store.Get(ctx, "u1", "e1")
	if err != nil || e.Pending {
		t.Fatalf("Get = %+v, %v", e, err)
	}
}

func TestReconcileDropsDanglingAndInapplicableRecords(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "e1", "cred")

	// An index entry whose record has expired.
	env.redis.SAdd(fallbackIndexKey, "ghost")
	env.redis.SAdd(fallbackUserPrefix+"u1", "ghost")
	// A code update for a row that no longer exists.
	now := env.now
	if err := env.store.putFallback(fallbackRecord{ID: "gone", UserID: "u1", Server: "jp", KeyVersion: 1, CodeSealed: []byte{1}, CodeIssuedAt: &now}); err != nil {
		t.Fatal(err)
	}
	// A record naming another owner for u1's row.
	if err := env.store.putFallback(fallbackRecord{ID: "e1", UserID: "u2", Server: "jp", KeyVersion: 1, CodeSealed: []byte{1}, CodeIssuedAt: &now}); err != nil {
		t.Fatal(err)
	}

	if n, err := env.store.Reconcile(ctx, "u1"); n != 1 || err != nil {
		t.Fatalf("Reconcile(u1) = %d, %v", n, err)
	}
	for _, set := range []string{fallbackIndexKey, fallbackUserPrefix + "u1"} {
		if ok, _ := env.redis.SIsMember(set, "ghost"); ok {
			t.Fatalf("ghost still in %s", set)
		}
	}
	if env.store.db.InheritVaultEntry.Query().CountX(ctx) != 1 {
		t.Fatal("a code-only record created a row")
	}
	if n, err := env.store.Reconcile(ctx, "u2"); n != 1 || err != nil {
		t.Fatalf("Reconcile(u2) = %d, %v", n, err)
	}
	row := env.store.db.InheritVaultEntry.GetX(ctx, "e1")
	if row.UserID != "u1" || (row.CodeSealed != nil && len(*row.CodeSealed) > 0) || row.CodeIssuedAt != nil {
		t.Fatalf("another owner's record changed the row: %+v", row)
	}
	if keys := env.redis.Keys(); len(keys) != 0 {
		t.Fatalf("redis keys left: %v", keys)
	}
}

// While a parked record cannot be moved, List and Get still show it as
// pending, and an entry already in PostgreSQL is not listed twice.
func TestListAndGetShowRecordsStillParked(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "indb", "cred-db")
	env.withDBDown(t, func() {
		putCredential(t, env.store, u, "parked", "cred-parked")
		if _, err := env.store.PutCode(ctx, codeInput(u, "indb", "CODE")); err != nil {
			t.Fatal(err)
		}
	})
	// A parked record past its retention is not shown.
	expired := env.now.Add(-time.Minute)
	if err := env.store.putFallback(fallbackRecord{ID: "expired", UserID: "u1", Server: "jp", KeyVersion: 1, CredentialSealed: []byte{1}, CredentialExpiresAt: expired}); err != nil {
		t.Fatal(err)
	}
	failCreate := failEntryOps(env.store.db, ent.OpCreate)
	failUpdate := failEntryOps(env.store.db, ent.OpUpdateOne)
	failCreate.Store(true)
	failUpdate.Store(true)

	entries, err := env.store.List(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.ID] = e.Pending
	}
	if len(entries) != 2 || got["indb"] || !got["parked"] {
		t.Fatalf("List = %v", got)
	}
	e, err := env.store.Get(ctx, "u1", "parked")
	if err != nil || !e.Pending {
		t.Fatalf("Get parked = %+v, %v", e, err)
	}
	if cred, err := u.OpenCredential(e); err != nil || string(cred) != "cred-parked" {
		t.Fatalf("parked credential = %q, %v", cred, err)
	}
	if _, err := env.store.Get(ctx, "u2", "parked"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner Get parked: %v", err)
	}
	if _, err := env.store.Get(ctx, "u1", "expired"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get expired parked: %v", err)
	}
	if others, err := env.store.List(ctx, "u2"); err != nil || len(others) != 0 {
		t.Fatalf("List(u2) = %v, %v", others, err)
	}
}

func TestGetAndListReportDatabaseErrorsAndExpiry(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "e1", "cred")
	env.withDBDown(t, func() {
		if _, err := env.store.List(ctx, "u1"); err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("List: %v", err)
		}
		if _, err := env.store.Get(ctx, "u1", "e1"); err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("Get: %v", err)
		}
		if err := env.store.Destroy(ctx, "u1", "e1"); err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("Destroy: %v", err)
		}
	})
	env.now = env.now.Add(CredentialRetention)
	if _, err := env.store.Get(ctx, "u1", "e1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired Get: %v", err)
	}
	if entries, err := env.store.List(ctx, "u1"); err != nil || len(entries) != 0 {
		t.Fatalf("expired List = %v, %v", entries, err)
	}
}

// ---- store: open, destroy, reap ----

func TestOpenCodeErrors(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "e1", "cred")
	e, err := env.store.Get(ctx, "u1", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := u.OpenCode(e); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no code: %v", err)
	}
	if _, err := env.store.PutCode(ctx, codeInput(u, "e1", "CODE")); err != nil {
		t.Fatal(err)
	}
	e, _ = env.store.Get(ctx, "u1", "e1")
	stale := &Unlocked{UserID: "u1", KeyVersion: u.KeyVersion + 1, priv: u.priv}
	if _, _, err := stale.OpenCode(e); !errors.Is(err, ErrStale) {
		t.Fatalf("stale key: %v", err)
	}
	tampered := *e
	tampered.CodeSealed = append([]byte(nil), e.CodeSealed...)
	tampered.CodeSealed[len(tampered.CodeSealed)-1] ^= 1
	if _, _, err := u.OpenCode(&tampered); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered code: %v", err)
	}
	// A seal that opens but does not hold a code payload.
	notJSON, err := Seal(u.PublicKey, []byte("not a code"), EntryAD(purposeCode, e.ID, e.UserID, e.Server, e.KeyVersion))
	if err != nil {
		t.Fatal(err)
	}
	odd := *e
	odd.CodeSealed = notJSON
	if _, _, err := u.OpenCode(&odd); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("non-JSON payload: %v", err)
	}
	// The credential blob is not accepted as a code (purpose is bound).
	swapped := *e
	swapped.CodeSealed = e.CredentialSealed
	if _, _, err := u.OpenCode(&swapped); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("credential as code: %v", err)
	}
	if id, pw, err := u.OpenCode(e); err != nil || id != "CODE" || pw != "pw12345678" {
		t.Fatalf("intact code = %q %q %v", id, pw, err)
	}
	if bytes.Contains(e.CodeSealed, []byte("pw12345678")) {
		t.Fatal("code sealed in the clear")
	}
}

func TestDestroyParkedEntry(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	env.withDBDown(t, func() { putCredential(t, env.store, u, "parked", "cred") })
	failCreate := failEntryOps(env.store.db, ent.OpCreate)
	failCreate.Store(true) // keep it parked through Get's reconcile

	if err := env.store.Destroy(ctx, "u2", "parked"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner: %v", err)
	}
	if !env.redis.Exists(fallbackKeyPrefix + "parked") {
		t.Fatal("other owner's Destroy removed the record")
	}
	if err := env.store.Destroy(ctx, "u1", "parked"); err != nil {
		t.Fatal(err)
	}
	if keys := env.redis.Keys(); len(keys) != 0 {
		t.Fatalf("redis keys left: %v", keys)
	}
	failCreate.Store(false)
	if _, err := env.store.Get(ctx, "u1", "parked"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after destroy: %v", err)
	}
	if err := env.store.Destroy(ctx, "u1", "parked"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Destroy: %v", err)
	}
}

func TestDestroyReportsAFailedRedisCleanup(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	env.withDBDown(t, func() { putCredential(t, env.store, u, "parked", "cred") })
	hook := &failTxPipelines{}
	env.store.redis.AddHook(hook)
	hook.on.Store(true)
	if err := env.store.Destroy(ctx, "u1", "parked"); !errors.Is(err, errInjected) {
		t.Fatalf("err = %v", err)
	}
	if !env.redis.Exists(fallbackKeyPrefix + "parked") {
		t.Fatal("record missing although the cleanup failed")
	}
}

func TestReapWithNothingToDo(t *testing.T) {
	env := newStoreEnv(t)
	res, err := env.store.Reap(context.Background())
	if err != nil || res != (ReapResult{}) {
		t.Fatalf("Reap = %+v, %v", res, err)
	}
}

func TestReapReportsDatabaseErrors(t *testing.T) {
	env := newStoreEnv(t)
	env.store.db = env.brokenDB(t)
	if res, err := env.store.Reap(context.Background()); err == nil || res != (ReapResult{}) {
		t.Fatalf("Reap = %+v, %v", res, err)
	}
}

// ---- reaper ----

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStartReaperIsANoOpWithoutAVault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, s := range []*Store{nil, NewStore(nil, nil, nil)} {
		wait := StartReaper(ctx, s, time.Millisecond, nil)
		done := make(chan struct{})
		go func() { wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("wait blocked for an unavailable store")
		}
	}
}

func TestStartReaperRunsAtStartAndOnEveryTickUntilCancelled(t *testing.T) {
	env := newStoreEnv(t)
	ctx := context.Background()
	u := newUser(t, env, "u1", "pw-123456")
	putCredential(t, env.store, u, "e1", "cred")
	if err := env.store.Destroy(ctx, "u1", "e1"); err != nil {
		t.Fatal(err)
	}
	var passes atomic.Int32
	env.store.db.InheritVaultEntry.Use(func(next postgresql.Mutator) postgresql.Mutator {
		return postgresql.MutateFunc(func(ctx context.Context, m postgresql.Mutation) (postgresql.Value, error) {
			if m.Op().Is(ent.OpDelete) {
				passes.Add(1)
			}
			return next.Mutate(ctx, m)
		})
	})
	logs := &syncBuffer{}
	runCtx, cancel := context.WithCancel(ctx)
	wait := StartReaper(runCtx, env.store, time.Millisecond, harukiLogger.NewLogger("test", "INFO", logs))
	waitFor(t, "three reap passes", func() bool { return passes.Load() >= 3 })
	cancel()
	wait()
	after := passes.Load()
	if n := env.store.db.InheritVaultEntry.Query().CountX(ctx); n != 0 {
		t.Fatalf("%d rows left", n)
	}
	out := logs.String()
	if strings.Count(out, "deleted=1") != 1 || strings.Contains(out, "WARN") {
		t.Fatalf("logs = %q", out)
	}
	if passes.Load() != after {
		t.Fatal("reaper kept running after wait returned")
	}
}

func TestStartReaperLogsFailuresAndUsesTheDefaultInterval(t *testing.T) {
	env := newStoreEnv(t)
	env.store.db = env.brokenDB(t)
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	wait := StartReaper(ctx, env.store, 0, harukiLogger.NewLogger("test", "INFO", logs))
	waitFor(t, "the reaper warning", func() bool { return strings.Contains(logs.String(), "Inherit vault reaper:") })
	cancel()
	wait()
	out := logs.String()
	if !strings.Contains(out, "WARN") || strings.Count(out, "Inherit vault reaper:") != 1 {
		t.Fatalf("logs = %q", out)
	}
}

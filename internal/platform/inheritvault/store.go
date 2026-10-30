package inheritvault

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	json "encoding/json/v2"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/inheritvaultentry"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/inheritvaultkey"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// CredentialRetention is how long a stored login credential is kept.
	CredentialRetention = 7 * 24 * time.Hour
	// CodeLifetime is how long the game accepts a transfer code.
	CodeLifetime = 24 * time.Hour

	SecretKindPassword = "password"
	SecretKindPIN      = "pin"

	purposeCredential = "credential"
	purposeCode       = "code"

	fallbackKeyPrefix   = "inherit:vault:fallback:"
	fallbackIndexKey    = "inherit:vault:fallback:index"
	fallbackUserPrefix  = "inherit:vault:fallback:user:"
	dbWriteAttemptLimit = 4
	dbWriteTimeout      = 5 * time.Second
)

var (
	// ErrNoKey means the user has not set up a vault key yet.
	ErrNoKey = errors.New("inherit vault: no key")
	// ErrNotFound covers a missing entry and one owned by someone else alike.
	ErrNotFound = errors.New("inherit vault: entry not found")
	// ErrUnavailable means the vault is not configured (no master key).
	ErrUnavailable = errors.New("inherit vault: unavailable")
	// ErrStale means the entry was sealed to a key that has since been reset.
	ErrStale = errors.New("inherit vault: entry sealed to a replaced key")
)

// dbWriteBackoff is the wait before database write attempt i (1-based).
var dbWriteBackoff = func(attempt int) time.Duration { return time.Duration(attempt) * 250 * time.Millisecond }

// Store reads and writes vault keys and entries. Entries are written to
// PostgreSQL; if that fails after retries, the sealed record goes to Redis
// and is moved into PostgreSQL later (Reconcile).
type Store struct {
	db     *postgresql.Client
	redis  *redis.Client
	master *MasterKey
	now    func() time.Time
}

// NewStore returns a store, or one whose every call fails with
// ErrUnavailable when master is nil.
func NewStore(db *postgresql.Client, rdb *redis.Client, master *MasterKey) *Store {
	return &Store{db: db, redis: rdb, master: master, now: func() time.Time { return time.Now().UTC() }}
}

// Available reports whether the vault can be used.
func (s *Store) Available() bool { return s != nil && s.master != nil && s.db != nil }

// Master is the loaded master key (nil when unavailable).
func (s *Store) Master() *MasterKey {
	if s == nil {
		return nil
	}
	return s.master
}

// KeyInfo is the public part of a user's key.
type KeyInfo struct {
	KeyVersion int
	PublicKey  []byte
	SecretKind string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (s *Store) loadKeyRow(ctx context.Context, userID string) (*postgresql.InheritVaultKey, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	row, err := s.db.InheritVaultKey.Query().Where(inheritvaultkey.UserIDEQ(userID)).Only(ctx)
	if postgresql.IsNotFound(err) {
		return nil, ErrNoKey
	}
	return row, err
}

// KeyInfo returns the user's key metadata, or ErrNoKey.
func (s *Store) KeyInfo(ctx context.Context, userID string) (*KeyInfo, error) {
	row, err := s.loadKeyRow(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &KeyInfo{KeyVersion: row.KeyVersion, PublicKey: row.PublicKey, SecretKind: string(row.SecretKind), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func wrappedFromRow(row *postgresql.InheritVaultKey) (*WrappedKey, error) {
	k := &WrappedKey{
		UserID:           row.UserID,
		KeyVersion:       row.KeyVersion,
		PublicKey:        row.PublicKey,
		WrappedPrivate:   row.WrappedPrivateKey,
		MasterKeyVersion: row.MasterKeyVersion,
		WrapScheme:       row.WrapScheme,
	}
	if err := json.Unmarshal([]byte(row.KdfParams), &k.KDF); err != nil {
		return nil, fmt.Errorf("inherit vault: bad KDF parameters: %w", err)
	}
	return k, nil
}

// Unlocked holds a user's private key in memory for one operation. Close it
// as soon as the operation ends.
type Unlocked struct {
	UserID     string
	KeyVersion int
	PublicKey  []byte
	SecretKind string
	priv       []byte
}

// Close clears the private key.
func (u *Unlocked) Close() {
	if u != nil {
		clear(u.priv)
		u.priv = nil
	}
}

// lastKeyVersion is the last version nextKeyVersion handed out.
var lastKeyVersion atomic.Int64

// nextKeyVersion returns the version for a new key pair: the wall clock in
// microseconds, kept strictly increasing within the process. ResetKey deletes
// the key row, so the version cannot be counted up from the previous one; a
// fixed version would let an entry sealed to a discarded key (by a job that
// was already running during the reset) pass the ErrStale check, and would
// let a Rewrap that raced a reset write the old private key next to the new
// public key.
func nextKeyVersion() int {
	v := time.Now().UnixMicro()
	for {
		last := lastKeyVersion.Load()
		if v <= last {
			v = last + 1
		}
		if lastKeyVersion.CompareAndSwap(last, v) {
			return int(v)
		}
	}
}

// CreateKey sets up the user's key pair protected by secret. It fails if a
// key already exists.
func (s *Store) CreateKey(ctx context.Context, userID, kind string, secret []byte) (*Unlocked, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	if kind != SecretKindPassword && kind != SecretKindPIN {
		return nil, fmt.Errorf("inherit vault: unknown secret kind %q", kind)
	}
	k, err := GenerateKey(s.master, userID, nextKeyVersion(), secret)
	if err != nil {
		return nil, err
	}
	params, err := json.Marshal(k.KDF)
	if err != nil {
		return nil, err
	}
	now := s.now()
	err = s.db.InheritVaultKey.Create().
		SetUserID(userID).
		SetKeyVersion(k.KeyVersion).
		SetPublicKey(k.PublicKey).
		SetWrappedPrivateKey(k.WrappedPrivate).
		SetWrapScheme(k.WrapScheme).
		SetSecretKind(inheritvaultkey.SecretKind(kind)).
		SetKdfParams(string(params)).
		SetMasterKeyVersion(k.MasterKeyVersion).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	return s.Unlock(ctx, userID, secret)
}

// Unlock unwraps the user's private key with secret. ErrDecrypt means the
// secret does not open the key (a changed password, or a wrong PIN).
func (s *Store) Unlock(ctx context.Context, userID string, secret []byte) (*Unlocked, error) {
	row, err := s.loadKeyRow(ctx, userID)
	if err != nil {
		return nil, err
	}
	k, err := wrappedFromRow(row)
	if err != nil {
		return nil, err
	}
	priv, err := UnwrapPrivateKey(s.master, k, secret)
	if err != nil {
		return nil, err
	}
	return &Unlocked{UserID: userID, KeyVersion: k.KeyVersion, PublicKey: k.PublicKey, SecretKind: string(row.SecretKind), priv: priv}, nil
}

// Rewrap opens the key with oldSecret and protects it with newSecret from
// then on. It is how a changed Toolbox password is carried over.
func (s *Store) Rewrap(ctx context.Context, userID string, oldSecret, newSecret []byte) (*Unlocked, error) {
	row, err := s.loadKeyRow(ctx, userID)
	if err != nil {
		return nil, err
	}
	k, err := wrappedFromRow(row)
	if err != nil {
		return nil, err
	}
	priv, err := UnwrapPrivateKey(s.master, k, oldSecret)
	if err != nil {
		return nil, err
	}
	defer clear(priv)
	nk, err := Rewrap(s.master, k, priv, newSecret)
	if err != nil {
		return nil, err
	}
	params, err := json.Marshal(nk.KDF)
	if err != nil {
		return nil, err
	}
	n, err := s.db.InheritVaultKey.Update().
		Where(inheritvaultkey.UserIDEQ(userID), inheritvaultkey.KeyVersionEQ(k.KeyVersion)).
		SetWrappedPrivateKey(nk.WrappedPrivate).
		SetKdfParams(string(params)).
		SetMasterKeyVersion(nk.MasterKeyVersion).
		SetUpdatedAt(s.now()).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNoKey
	}
	return s.Unlock(ctx, userID, newSecret)
}

// ResetKey discards the user's key pair and every vault entry sealed to it,
// including entries still waiting in the Redis fallback. It is the only way
// forward when the owner no longer knows the secret the key was wrapped with
// (a password reset through account recovery, a forgotten PIN). It returns
// how many entries were destroyed.
func (s *Store) ResetKey(ctx context.Context, userID string) (int, error) {
	if !s.Available() {
		return 0, ErrUnavailable
	}
	destroyed, err := s.db.InheritVaultEntry.Delete().Where(inheritvaultentry.UserIDEQ(userID)).Exec(ctx)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.InheritVaultKey.Delete().Where(inheritvaultkey.UserIDEQ(userID)).Exec(ctx); err != nil {
		return destroyed, err
	}
	if s.redis != nil {
		ids, err := s.redis.SMembers(ctx, fallbackUserPrefix+userID).Result()
		if err == nil {
			for _, id := range ids {
				_ = s.dropFallback(ctx, id, userID)
			}
			destroyed += len(ids)
		}
	}
	return destroyed, nil
}

// NewEntryID returns a fresh vault entry ID.
func NewEntryID() string { return uuid.NewString() }

// CredentialInput is a credential to store, sealed to the public key the job
// read when the owner confirmed it.
type CredentialInput struct {
	EntryID    string
	UserID     string
	Server     string
	GameUserID int64
	JobID      string
	KeyVersion int
	PublicKey  []byte
	Credential []byte
}

// Location says where a write landed.
type Location string

const (
	LocationDB       Location = "db"
	LocationFallback Location = "redis_fallback"
)

// fallbackRecord is a vault entry parked in Redis while PostgreSQL could not
// take it. It holds only sealed values. A record without a credential only
// carries a code update for a row that is already in PostgreSQL.
type fallbackRecord struct {
	ID                  string     `json:"id"`
	UserID              string     `json:"user_id"`
	Server              string     `json:"server"`
	GameUserID          int64      `json:"game_user_id"`
	JobID               string     `json:"job_id,omitempty"`
	KeyVersion          int        `json:"key_version"`
	WrapScheme          string     `json:"wrap_scheme"`
	CredentialSealed    []byte     `json:"credential_sealed,omitempty"`
	CodeSealed          []byte     `json:"code_sealed,omitempty"`
	CodeIssuedAt        *time.Time `json:"code_issued_at,omitempty"`
	CodeExpiresAt       *time.Time `json:"code_expires_at,omitempty"`
	CredentialExpiresAt time.Time  `json:"credential_expires_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// PutCredential seals the credential and stores it. It retries PostgreSQL a
// few times with a short backoff and, if that still fails, parks the sealed
// record in Redis (owner-bound, 7 days). Each attempt uses its own deadline,
// not ctx's, so a job running out of time cannot lose a credential the game
// has already handed over; ctx only bounds the waits between attempts.
func (s *Store) PutCredential(ctx context.Context, in CredentialInput) (Location, error) {
	if !s.Available() {
		return "", ErrUnavailable
	}
	now := s.now()
	sealed, err := Seal(in.PublicKey, in.Credential, EntryAD(purposeCredential, in.EntryID, in.UserID, in.Server, in.KeyVersion))
	if err != nil {
		return "", err
	}
	rec := fallbackRecord{
		ID: in.EntryID, UserID: in.UserID, Server: in.Server, GameUserID: in.GameUserID, JobID: in.JobID,
		KeyVersion: in.KeyVersion, WrapScheme: EntryWrapScheme, CredentialSealed: sealed,
		CredentialExpiresAt: now.Add(CredentialRetention), CreatedAt: now, UpdatedAt: now,
	}
	dbErr := s.retryDB(ctx, func(ctx context.Context) error { return s.insertEntry(ctx, rec) })
	if dbErr == nil {
		return LocationDB, nil
	}
	if err := s.putFallback(rec); err != nil {
		return "", errors.Join(dbErr, err)
	}
	return LocationFallback, nil
}

// CodeInput is a newly issued transfer code for an existing entry.
type CodeInput struct {
	EntryID    string
	UserID     string
	Server     string
	KeyVersion int
	PublicKey  []byte
	InheritID  string
	Password   string
}

type codePayload struct {
	InheritID string `json:"inherit_id"`
	Password  string `json:"password"`
}

// PutCode seals the code and attaches it to the entry, with the same retry
// and Redis fallback as PutCredential.
func (s *Store) PutCode(ctx context.Context, in CodeInput) (Location, error) {
	if !s.Available() {
		return "", ErrUnavailable
	}
	plain, err := json.Marshal(codePayload{InheritID: in.InheritID, Password: in.Password})
	if err != nil {
		return "", err
	}
	defer clear(plain)
	sealed, err := Seal(in.PublicKey, plain, EntryAD(purposeCode, in.EntryID, in.UserID, in.Server, in.KeyVersion))
	if err != nil {
		return "", err
	}
	now := s.now()
	expires := now.Add(CodeLifetime)
	var inDB bool
	dbErr := s.retryDB(ctx, func(ctx context.Context) error {
		n, err := s.db.InheritVaultEntry.Update().
			Where(inheritvaultentry.IDEQ(in.EntryID), inheritvaultentry.UserIDEQ(in.UserID), inheritvaultentry.DestroyedAtIsNil()).
			SetCodeSealed(sealed).SetCodeIssuedAt(now).SetCodeExpiresAt(expires).SetUpdatedAt(now).
			Save(ctx)
		inDB = n > 0
		return err
	})
	if dbErr == nil && inDB {
		return LocationDB, nil
	}
	// Either PostgreSQL is down, or the entry itself is still parked in Redis.
	rec, _ := s.getFallback(context.Background(), in.EntryID)
	if rec == nil {
		if dbErr == nil {
			return "", ErrNotFound
		}
		rec = &fallbackRecord{ID: in.EntryID, UserID: in.UserID, Server: in.Server, KeyVersion: in.KeyVersion, WrapScheme: EntryWrapScheme, CreatedAt: now}
	}
	if rec.UserID != in.UserID {
		return "", ErrNotFound
	}
	rec.CodeSealed, rec.CodeIssuedAt, rec.CodeExpiresAt, rec.UpdatedAt = sealed, &now, &expires, now
	if err := s.putFallback(*rec); err != nil {
		return "", errors.Join(dbErr, err)
	}
	return LocationFallback, nil
}

func (s *Store) retryDB(ctx context.Context, write func(context.Context) error) error {
	var err error
	for attempt := range dbWriteAttemptLimit {
		if attempt > 0 {
			timer := time.NewTimer(dbWriteBackoff(attempt))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(err, ctx.Err())
			}
		}
		wctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
		err = write(wctx)
		cancel()
		if err == nil {
			return nil
		}
	}
	return err
}

func (s *Store) insertEntry(ctx context.Context, rec fallbackRecord) error {
	create := s.db.InheritVaultEntry.Create().
		SetID(rec.ID).
		SetUserID(rec.UserID).
		SetServer(inheritvaultentry.Server(rec.Server)).
		SetGameUserID(rec.GameUserID).
		SetJobID(rec.JobID).
		SetKeyVersion(rec.KeyVersion).
		SetWrapScheme(rec.WrapScheme).
		SetCredentialSealed(rec.CredentialSealed).
		SetCredentialExpiresAt(rec.CredentialExpiresAt).
		SetCreatedAt(rec.CreatedAt).
		SetUpdatedAt(rec.UpdatedAt)
	if rec.CodeSealed != nil {
		create.SetCodeSealed(rec.CodeSealed).SetNillableCodeIssuedAt(rec.CodeIssuedAt).SetNillableCodeExpiresAt(rec.CodeExpiresAt)
	}
	return create.Exec(ctx)
}

func (s *Store) putFallback(rec fallbackRecord) error {
	if s.redis == nil {
		return errors.New("inherit vault: no redis for fallback")
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	ttl := time.Until(rec.CredentialExpiresAt)
	if rec.CredentialExpiresAt.IsZero() || ttl <= 0 {
		ttl = CredentialRetention
	}
	var lastErr error
	for attempt := range dbWriteAttemptLimit {
		if attempt > 0 {
			time.Sleep(dbWriteBackoff(attempt))
		}
		ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
		_, lastErr = s.redis.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, fallbackKeyPrefix+rec.ID, body, ttl)
			p.SAdd(ctx, fallbackIndexKey, rec.ID)
			p.SAdd(ctx, fallbackUserPrefix+rec.UserID, rec.ID)
			return nil
		})
		cancel()
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (s *Store) getFallback(ctx context.Context, id string) (*fallbackRecord, error) {
	if s.redis == nil {
		return nil, nil
	}
	body, err := s.redis.Get(ctx, fallbackKeyPrefix+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec fallbackRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Store) dropFallback(ctx context.Context, id, userID string) error {
	_, err := s.redis.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, fallbackKeyPrefix+id)
		p.SRem(ctx, fallbackIndexKey, id)
		if userID != "" {
			p.SRem(ctx, fallbackUserPrefix+userID, id)
		}
		return nil
	})
	return err
}

// Reconcile moves parked Redis records into PostgreSQL: all of them, or only
// userID's when it is set. It returns how many records were moved.
func (s *Store) Reconcile(ctx context.Context, userID string) (int, error) {
	if !s.Available() || s.redis == nil {
		return 0, nil
	}
	setKey := fallbackIndexKey
	if userID != "" {
		setKey = fallbackUserPrefix + userID
	}
	ids, err := s.redis.SMembers(ctx, setKey).Result()
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, id := range ids {
		rec, err := s.getFallback(ctx, id)
		if err != nil {
			return moved, err
		}
		if rec == nil {
			_ = s.dropFallback(ctx, id, userID)
			continue
		}
		if err := s.applyFallback(ctx, *rec); err != nil {
			return moved, err
		}
		if err := s.dropFallback(ctx, id, rec.UserID); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

func (s *Store) applyFallback(ctx context.Context, rec fallbackRecord) error {
	row, err := s.db.InheritVaultEntry.Get(ctx, rec.ID)
	switch {
	case postgresql.IsNotFound(err):
		if len(rec.CredentialSealed) == 0 {
			return nil // a code update for a row that no longer exists
		}
		return s.insertEntry(ctx, rec)
	case err != nil:
		return err
	case row.UserID != rec.UserID:
		return nil
	case rec.CodeSealed != nil && (row.CodeIssuedAt == nil || rec.CodeIssuedAt != nil && rec.CodeIssuedAt.After(*row.CodeIssuedAt)):
		return s.db.InheritVaultEntry.UpdateOneID(rec.ID).
			SetCodeSealed(rec.CodeSealed).SetNillableCodeIssuedAt(rec.CodeIssuedAt).SetNillableCodeExpiresAt(rec.CodeExpiresAt).
			SetUpdatedAt(s.now()).Exec(ctx)
	}
	return nil
}

// Entry is a vault entry with its sealed fields.
type Entry struct {
	ID                  string
	UserID              string
	Server              string
	GameUserID          int64
	JobID               string
	KeyVersion          int
	WrapScheme          string
	CredentialSealed    []byte
	CodeSealed          []byte
	CodeIssuedAt        *time.Time
	CodeExpiresAt       *time.Time
	CredentialExpiresAt time.Time
	CreatedAt           time.Time
	// Pending is true while the entry is still parked in Redis.
	Pending bool
}

func entryFromRow(row *postgresql.InheritVaultEntry) *Entry {
	e := &Entry{
		ID: row.ID, UserID: row.UserID, Server: string(row.Server), GameUserID: row.GameUserID, JobID: row.JobID,
		KeyVersion: row.KeyVersion, WrapScheme: row.WrapScheme, CredentialSealed: row.CredentialSealed,
		CodeIssuedAt: row.CodeIssuedAt, CodeExpiresAt: row.CodeExpiresAt, CredentialExpiresAt: row.CredentialExpiresAt, CreatedAt: row.CreatedAt,
	}
	if row.CodeSealed != nil {
		e.CodeSealed = *row.CodeSealed
	}
	return e
}

func entryFromFallback(rec *fallbackRecord) *Entry {
	return &Entry{
		ID: rec.ID, UserID: rec.UserID, Server: rec.Server, GameUserID: rec.GameUserID, JobID: rec.JobID,
		KeyVersion: rec.KeyVersion, WrapScheme: rec.WrapScheme, CredentialSealed: rec.CredentialSealed, CodeSealed: rec.CodeSealed,
		CodeIssuedAt: rec.CodeIssuedAt, CodeExpiresAt: rec.CodeExpiresAt, CredentialExpiresAt: rec.CredentialExpiresAt, CreatedAt: rec.CreatedAt, Pending: true,
	}
}

func (e *Entry) live(now time.Time) bool {
	return len(e.CredentialSealed) > 0 && now.Before(e.CredentialExpiresAt)
}

// CodeLive reports whether the entry holds a code the game still accepts.
func (e *Entry) CodeLive(now time.Time) bool {
	return len(e.CodeSealed) > 0 && e.CodeExpiresAt != nil && now.Before(*e.CodeExpiresAt)
}

// List returns the owner's live entries, newest first. Parked Redis records
// are moved into PostgreSQL first when possible, and listed as pending
// otherwise.
func (s *Store) List(ctx context.Context, userID string) ([]*Entry, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	_, _ = s.Reconcile(ctx, userID)
	now := s.now()
	rows, err := s.db.InheritVaultEntry.Query().
		Where(inheritvaultentry.UserIDEQ(userID), inheritvaultentry.DestroyedAtIsNil(), inheritvaultentry.CredentialExpiresAtGT(now)).
		Order(postgresql.Desc(inheritvaultentry.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		out = append(out, entryFromRow(row))
		seen[row.ID] = true
	}
	if s.redis != nil {
		ids, _ := s.redis.SMembers(ctx, fallbackUserPrefix+userID).Result()
		for _, id := range ids {
			if seen[id] {
				continue
			}
			if rec, _ := s.getFallback(ctx, id); rec != nil && rec.UserID == userID {
				if e := entryFromFallback(rec); e.live(now) {
					out = append(out, e)
				}
			}
		}
	}
	return out, nil
}

// Get returns one of the owner's live entries, or ErrNotFound.
func (s *Store) Get(ctx context.Context, userID, entryID string) (*Entry, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	_, _ = s.Reconcile(ctx, userID)
	now := s.now()
	row, err := s.db.InheritVaultEntry.Query().
		Where(inheritvaultentry.IDEQ(entryID), inheritvaultentry.UserIDEQ(userID), inheritvaultentry.DestroyedAtIsNil()).
		Only(ctx)
	if err == nil {
		if e := entryFromRow(row); e.live(now) {
			return e, nil
		}
		return nil, ErrNotFound
	}
	if !postgresql.IsNotFound(err) {
		return nil, err
	}
	if rec, _ := s.getFallback(ctx, entryID); rec != nil && rec.UserID == userID {
		if e := entryFromFallback(rec); e.live(now) {
			return e, nil
		}
	}
	return nil, ErrNotFound
}

// OpenCredential decrypts the entry's credential. The caller clears it.
func (u *Unlocked) OpenCredential(e *Entry) ([]byte, error) {
	if e.KeyVersion != u.KeyVersion {
		return nil, ErrStale
	}
	return Open(u.priv, e.CredentialSealed, EntryAD(purposeCredential, e.ID, e.UserID, e.Server, e.KeyVersion))
}

// OpenCode decrypts the entry's transfer code.
func (u *Unlocked) OpenCode(e *Entry) (inheritID, password string, err error) {
	if e.KeyVersion != u.KeyVersion {
		return "", "", ErrStale
	}
	if len(e.CodeSealed) == 0 {
		return "", "", ErrNotFound
	}
	plain, err := Open(u.priv, e.CodeSealed, EntryAD(purposeCode, e.ID, e.UserID, e.Server, e.KeyVersion))
	if err != nil {
		return "", "", err
	}
	defer clear(plain)
	var c codePayload
	if err := json.Unmarshal(plain, &c); err != nil {
		return "", "", ErrDecrypt
	}
	return c.InheritID, c.Password, nil
}

// Destroy makes the entry unreadable at once (its sealed fields are wiped)
// and leaves the row for the reaper to delete.
func (s *Store) Destroy(ctx context.Context, userID, entryID string) error {
	if !s.Available() {
		return ErrUnavailable
	}
	now := s.now()
	n, err := s.db.InheritVaultEntry.Update().
		Where(inheritvaultentry.IDEQ(entryID), inheritvaultentry.UserIDEQ(userID), inheritvaultentry.DestroyedAtIsNil()).
		SetCredentialSealed([]byte{}).ClearCodeSealed().SetDestroyedAt(now).SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return err
	}
	parked := false
	if s.redis != nil {
		if rec, _ := s.getFallback(ctx, entryID); rec != nil && rec.UserID == userID {
			parked = true
			if err := s.dropFallback(ctx, entryID, userID); err != nil {
				return err
			}
		}
	}
	if n == 0 && !parked {
		return ErrNotFound
	}
	return nil
}

// ReapResult counts what one Reap pass did.
type ReapResult struct {
	Deleted      int
	CodesCleared int
	Reconciled   int
}

// Reap moves parked records into PostgreSQL, deletes destroyed and expired
// entries, and wipes transfer codes the game no longer accepts.
func (s *Store) Reap(ctx context.Context) (ReapResult, error) {
	var res ReapResult
	if !s.Available() {
		return res, nil
	}
	var errs []error
	moved, err := s.Reconcile(ctx, "")
	res.Reconciled = moved
	errs = append(errs, err)
	now := s.now()
	res.Deleted, err = s.db.InheritVaultEntry.Delete().
		Where(inheritvaultentry.Or(inheritvaultentry.DestroyedAtNotNil(), inheritvaultentry.CredentialExpiresAtLTE(now))).
		Exec(ctx)
	errs = append(errs, err)
	res.CodesCleared, err = s.db.InheritVaultEntry.Update().
		Where(inheritvaultentry.CodeSealedNotNil(), inheritvaultentry.CodeExpiresAtLTE(now)).
		ClearCodeSealed().SetUpdatedAt(now).
		Save(ctx)
	errs = append(errs, err)
	return res, errors.Join(errs...)
}

// MaskGameUserID keeps the first two and last three digits.
func MaskGameUserID(id int64) string {
	s := fmt.Sprintf("%d", id)
	if len(s) <= 5 {
		return strings.Repeat("*", len(s))
	}
	return s[:2] + strings.Repeat("*", len(s)-5) + s[len(s)-3:]
}

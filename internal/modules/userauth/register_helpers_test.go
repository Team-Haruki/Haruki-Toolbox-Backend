package userauth

import (
	"errors"
	"haruki-suite/utils/database/postgresql"
	"io"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestFormatRegisterUID(t *testing.T) {
	t.Parallel()

	got := formatRegisterUID(123, 45)
	want := "0123000045"
	if got != want {
		t.Fatalf("formatRegisterUID = %q, want %q", got, want)
	}

	got = formatRegisterUID(9999, 999999)
	want = "9999999999"
	if got != want {
		t.Fatalf("formatRegisterUID max = %q, want %q", got, want)
	}
}

func TestDecideRegisterCreateUserFailure(t *testing.T) {
	t.Parallel()

	t.Run("non-constraint error should fail directly", func(t *testing.T) {
		t.Parallel()
		got := decideRegisterCreateUserFailure(errors.New("db down"), false)
		if got != registerCreateUserFailureDecisionFail {
			t.Fatalf("decision = %v, want fail", got)
		}
	})

	t.Run("constraint with existing email should return email conflict", func(t *testing.T) {
		t.Parallel()
		got := decideRegisterCreateUserFailure(new(postgresql.ConstraintError), true)
		if got != registerCreateUserFailureDecisionEmailConflict {
			t.Fatalf("decision = %v, want email conflict", got)
		}
	})

	t.Run("constraint without existing email should retry uid", func(t *testing.T) {
		t.Parallel()
		got := decideRegisterCreateUserFailure(new(postgresql.ConstraintError), false)
		if got != registerCreateUserFailureDecisionRetryUID {
			t.Fatalf("decision = %v, want retry uid", got)
		}
	})
}

func TestGenerateRegisterUID(t *testing.T) {
	originalRandInt := registerRandInt
	t.Cleanup(func() {
		registerRandInt = originalRandInt
	})

	t.Run("success", func(t *testing.T) {
		registerRandInt = func(_ io.Reader, _ *big.Int) (*big.Int, error) {
			return big.NewInt(42), nil
		}

		got, err := generateRegisterUID(time.UnixMicro(123_000_000))
		if err != nil {
			t.Fatalf("generateRegisterUID returned error: %v", err)
		}
		want := formatRegisterUID(0, 42)
		if got != want {
			t.Fatalf("generateRegisterUID = %q, want %q", got, want)
		}
	})

	t.Run("random failure", func(t *testing.T) {
		registerRandInt = func(_ io.Reader, _ *big.Int) (*big.Int, error) {
			return nil, errors.New("entropy unavailable")
		}

		_, err := generateRegisterUID(time.Now())
		if err == nil {
			t.Fatalf("generateRegisterUID should fail when random source fails")
		}
		if !strings.Contains(err.Error(), "generate uid random number") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

package usergamebindings

import (
	"testing"

	"haruki-suite/utils/database/postgresql"

	"github.com/gofiber/fiber/v3"
)

func TestParseDeckRecommendDataMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		raw       string
		want      deckRecommendDataMode
		wantError bool
	}{
		{name: "empty defaults to suite", raw: "", want: deckRecommendDataModeSuite},
		{name: "suite", raw: "suite", want: deckRecommendDataModeSuite},
		{name: "mysekai trims and lowercases", raw: " MySekai ", want: deckRecommendDataModeMysekai},
		{name: "invalid", raw: "all", wantError: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseDeckRecommendDataMode(tc.raw)
			if tc.wantError {
				if err == nil || err.Code != fiber.StatusBadRequest {
					t.Fatalf("expected bad request error, got %#v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateDeckRecommendDataBinding(t *testing.T) {
	t.Parallel()

	ownedVerified := &postgresql.GameAccountBinding{Verified: true}
	ownedVerified.Edges.User = &postgresql.User{ID: "u-1"}
	if err := validateVerifiedOwnedGameAccountBinding(ownedVerified, "u-1"); err != nil {
		t.Fatalf("owned verified binding should be accepted: %v", err)
	}

	if err := validateVerifiedOwnedGameAccountBinding(nil, "u-1"); err == nil || err.Code != fiber.StatusNotFound {
		t.Fatalf("nil binding should map to 404, got %#v", err)
	}

	orphan := &postgresql.GameAccountBinding{Verified: true}
	if err := validateVerifiedOwnedGameAccountBinding(orphan, "u-1"); err == nil || err.Code != fiber.StatusConflict {
		t.Fatalf("orphan binding should map to 409, got %#v", err)
	}

	ownedByOther := &postgresql.GameAccountBinding{Verified: true}
	ownedByOther.Edges.User = &postgresql.User{ID: "u-2"}
	if err := validateVerifiedOwnedGameAccountBinding(ownedByOther, "u-1"); err == nil || err.Code != fiber.StatusForbidden {
		t.Fatalf("other user's binding should map to 403, got %#v", err)
	}

	unverified := &postgresql.GameAccountBinding{Verified: false}
	unverified.Edges.User = &postgresql.User{ID: "u-1"}
	if err := validateVerifiedOwnedGameAccountBinding(unverified, "u-1"); err == nil || err.Code != fiber.StatusBadRequest {
		t.Fatalf("unverified binding should map to 400, got %#v", err)
	}
}

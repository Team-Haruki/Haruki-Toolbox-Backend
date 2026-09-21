package usergamebindings

import (
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
)

func capabilityKeys(capabilities map[string]accessibleGameAccountCapability) map[string]bool {
	keys := make(map[string]bool, len(capabilities))
	for key := range capabilities {
		keys[key] = true
	}
	return keys
}

func TestBuildAccessibleGameAccountItemsOwnedOrdering(t *testing.T) {
	accessible := &postgresql.AccessibleGameAccounts{
		Owned: []*postgresql.GameAccountBinding{
			{Server: "jp", GameUserID: "200", Verified: true},
			{Server: "en", GameUserID: "100", Verified: true, IsDefault: true},
			{Server: "jp", GameUserID: "100", Verified: false},
		},
	}
	items := buildAccessibleGameAccountItems(accessible)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	want := []string{"en/100", "jp/100", "jp/200"}
	for i, item := range items {
		if got := item.Server + "/" + item.GameUserID; got != want[i] {
			t.Fatalf("items[%d] = %s, want %s", i, got, want[i])
		}
		if item.Ownership != accessibleGameAccountOwnershipOwn {
			t.Fatalf("items[%d].Ownership = %s, want own", i, item.Ownership)
		}
		if item.Owner != nil {
			t.Fatalf("items[%d].Owner = %+v, want nil for an owned account", i, item.Owner)
		}
	}

	// The verified default owns every readable data type, including derived
	// recommend and owner-only profile.
	keys := capabilityKeys(items[0].Capabilities)
	for _, want := range []string{"suite", "mysekai", "profile", gameAccountCapabilityRecommend} {
		if !keys[want] {
			t.Fatalf("verified owned capabilities missing %q: %v", want, keys)
		}
	}
	for key, capability := range items[0].Capabilities {
		if capability.ExpiresAt != nil {
			t.Fatalf("owned capability %q carries an expiry", key)
		}
	}

	// An unverified binding is listed but unreadable, so it gates to nothing.
	if len(items[1].Capabilities) != 0 {
		t.Fatalf("unverified owned capabilities = %v, want empty", items[1].Capabilities)
	}
	if items[1].Verified {
		t.Fatalf("items[1].Verified = true, want false")
	}
}

func TestBuildAccessibleGameAccountItemsAggregatesGrants(t *testing.T) {
	suiteExpiry := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	mysekaiExpiry := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	avatar := "/avatars/owner.png"
	accessible := &postgresql.AccessibleGameAccounts{
		Grants: []postgresql.AccessibleGameAccountGrant{
			{
				Server: "jp", GameUserID: "987", DataType: "suite",
				ExpiresAt: suiteExpiry,
				GrantedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
				Owner:     postgresql.AccessibleGameAccountOwner{UserID: "owner-1", Name: "Owner One", AvatarPath: &avatar},
			},
			{
				Server: "jp", GameUserID: "987", DataType: "mysekai",
				ExpiresAt: mysekaiExpiry,
				GrantedAt: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
				Owner:     postgresql.AccessibleGameAccountOwner{UserID: "owner-1", Name: "Owner One", AvatarPath: &avatar},
			},
		},
	}
	items := buildAccessibleGameAccountItems(accessible)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 aggregated account", len(items))
	}
	item := items[0]
	if item.Ownership != accessibleGameAccountOwnershipGranted {
		t.Fatalf("Ownership = %s, want granted", item.Ownership)
	}
	if item.Owner == nil || item.Owner.UserID != "owner-1" || item.Owner.Name != "Owner One" {
		t.Fatalf("Owner = %+v, want owner-1/Owner One", item.Owner)
	}
	if item.Owner.AvatarPath == nil || *item.Owner.AvatarPath != avatar {
		t.Fatalf("Owner.AvatarPath = %v, want %s", item.Owner.AvatarPath, avatar)
	}
	// profile is grantable now, but only when granted: this account was granted
	// suite and mysekai only, so profile must be absent.
	if _, ok := item.Capabilities["profile"]; ok {
		t.Fatalf("granted capabilities expose profile without a profile grant: %v", capabilityKeys(item.Capabilities))
	}
	if got := item.Capabilities["suite"].ExpiresAt; got == nil || !got.Equal(suiteExpiry) {
		t.Fatalf("suite expiry = %v, want %v", got, suiteExpiry)
	}
	if got := item.Capabilities["mysekai"].ExpiresAt; got == nil || !got.Equal(mysekaiExpiry) {
		t.Fatalf("mysekai expiry = %v, want %v", got, mysekaiExpiry)
	}
	// recommend depends on suite alone, so it expires with suite even though the
	// mysekai grant lapses earlier.
	got := item.Capabilities[gameAccountCapabilityRecommend].ExpiresAt
	if got == nil || !got.Equal(suiteExpiry) {
		t.Fatalf("recommend expiry = %v, want %v", got, suiteExpiry)
	}
}

func TestBuildAccessibleGameAccountItemsGrantWithoutSuiteHasNoRecommend(t *testing.T) {
	accessible := &postgresql.AccessibleGameAccounts{
		Grants: []postgresql.AccessibleGameAccountGrant{
			{
				Server: "jp", GameUserID: "555", DataType: "mysekai",
				ExpiresAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
				GrantedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
				Owner:     postgresql.AccessibleGameAccountOwner{UserID: "owner-2", Name: "Owner Two"},
			},
		},
	}
	items := buildAccessibleGameAccountItems(accessible)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	keys := capabilityKeys(items[0].Capabilities)
	if keys[gameAccountCapabilityRecommend] {
		t.Fatalf("recommend offered without a suite grant: %v", keys)
	}
	if !keys["mysekai"] {
		t.Fatalf("mysekai capability missing: %v", keys)
	}
}

func TestBuildAccessibleGameAccountItemsOwnedBeforeGranted(t *testing.T) {
	accessible := &postgresql.AccessibleGameAccounts{
		Owned: []*postgresql.GameAccountBinding{
			{Server: "jp", GameUserID: "999", Verified: true},
		},
		Grants: []postgresql.AccessibleGameAccountGrant{
			{
				Server: "jp", GameUserID: "111", DataType: "suite",
				ExpiresAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
				GrantedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
				Owner:     postgresql.AccessibleGameAccountOwner{UserID: "owner-3", Name: "Owner Three"},
			},
			{
				Server: "jp", GameUserID: "222", DataType: "suite",
				ExpiresAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
				GrantedAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
				Owner:     postgresql.AccessibleGameAccountOwner{UserID: "owner-4", Name: "Owner Four"},
			},
		},
	}
	items := buildAccessibleGameAccountItems(accessible)
	want := []string{"999", "222", "111"}
	if len(items) != len(want) {
		t.Fatalf("items = %d, want %d", len(items), len(want))
	}
	for i, item := range items {
		if item.GameUserID != want[i] {
			t.Fatalf("items[%d] = %s, want %s", i, item.GameUserID, want[i])
		}
	}
}

func TestBuildAccessibleGameAccountItemsNil(t *testing.T) {
	if items := buildAccessibleGameAccountItems(nil); items == nil || len(items) != 0 {
		t.Fatalf("items = %v, want empty non-nil slice", items)
	}
}

func TestDeckRecommendRequiredDataTypes(t *testing.T) {
	if got := deckRecommendRequiredDataTypes(deckRecommendDataModeSuite); len(got) != 1 || got[0] != ownedGameAccountDataTypeSuite {
		t.Fatalf("suite mode requires %v, want [suite]", got)
	}
	got := deckRecommendRequiredDataTypes(deckRecommendDataModeMysekai)
	if len(got) != 2 || got[0] != ownedGameAccountDataTypeSuite || got[1] != ownedGameAccountDataTypeMysekai {
		t.Fatalf("mysekai mode requires %v, want [suite mysekai]", got)
	}
}

// profile became grantable on 2026-08-26. The aggregate must surface it, because
// capabilities is the only gate a client consults — a profile grant the endpoint
// does not report is a grant the UI can never act on.
func TestBuildAccessibleGameAccountItemsSurfacesGrantedProfile(t *testing.T) {
	expiry := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	accessible := &postgresql.AccessibleGameAccounts{
		Grants: []postgresql.AccessibleGameAccountGrant{
			{
				Server: "jp", GameUserID: "777", DataType: "profile",
				ExpiresAt: expiry,
				GrantedAt: time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC),
				Owner:     postgresql.AccessibleGameAccountOwner{UserID: "owner-5", Name: "Owner Five"},
			},
		},
	}
	items := buildAccessibleGameAccountItems(accessible)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	got := items[0].Capabilities["profile"].ExpiresAt
	if got == nil || !got.Equal(expiry) {
		t.Fatalf("profile expiry = %v, want %v", got, expiry)
	}
	// A profile grant alone does not make recommend-data callable: that needs suite.
	if _, ok := items[0].Capabilities[gameAccountCapabilityRecommend]; ok {
		t.Fatalf("recommend offered from a profile-only grant: %v", capabilityKeys(items[0].Capabilities))
	}
}

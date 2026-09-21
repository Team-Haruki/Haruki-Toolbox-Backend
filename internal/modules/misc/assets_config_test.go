package misc

import "testing"

func TestAssetsConfigPreservesFriendLinkURLJoining(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		fileName string
		want     string
	}{
		{
			name:     "regular base URL",
			baseURL:  "https://assets.example.test",
			fileName: "avatar.png",
			want:     "https://assets.example.test/friend-links/avatar.png",
		},
		{
			name:     "trailing slash remains visible",
			baseURL:  "https://assets.example.test/",
			fileName: "background.png",
			want:     "https://assets.example.test//friend-links/background.png",
		},
		{
			name:     "empty base URL",
			fileName: "avatar.png",
			want:     "/friend-links/avatar.png",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assets := NewAssetsConfig(AssetsConfigOptions{AvatarBaseURL: test.baseURL})
			if got := assets.FriendLinkURL(test.fileName); got != test.want {
				t.Fatalf("FriendLinkURL(%q) = %q, want %q", test.fileName, got, test.want)
			}
		})
	}
}

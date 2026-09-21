package userprofile

import "testing"

func TestNewConfigNormalizesAvatarSettings(t *testing.T) {
	profileConfig := NewConfig(ConfigOptions{
		AvatarSaveDir: "  /srv/haruki/avatars  ",
		AvatarBaseURL: "  https://assets.example.test///  ",
	})

	if got, want := profileConfig.AvatarSaveDir(), "/srv/haruki/avatars"; got != want {
		t.Fatalf("AvatarSaveDir() = %q, want %q", got, want)
	}
	if got, want := profileConfig.AvatarURL("avatar.png"), "https://assets.example.test/avatars/avatar.png"; got != want {
		t.Fatalf("AvatarURL() = %q, want %q", got, want)
	}
}

func TestConfigZeroValuePreservesEmptyDefaults(t *testing.T) {
	var profileConfig Config

	if got := profileConfig.AvatarSaveDir(); got != "" {
		t.Fatalf("AvatarSaveDir() = %q, want empty", got)
	}
	if got, want := profileConfig.AvatarURL("avatar.png"), "/avatars/avatar.png"; got != want {
		t.Fatalf("AvatarURL() = %q, want %q", got, want)
	}
}

package usersocial

import (
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

const (
	socialPlatformVerifyTTL         = 5 * time.Minute
	socialPlatformVerifyMaxAttempts = 5
)

// The status token also identifies the issuance generation. Verification
// consumes exactly this immutable record before writing any account binding.
type socialVerificationChallenge struct {
	CodeDigest  string `json:"digest"`
	OwnerID     string `json:"owner"`
	StatusToken string `json:"statusToken"`
}

func encodeSocialVerificationChallenge(keys harukiRedis.KeyBuilder, code, ownerID, statusToken string) (string, error) {
	record, err := jsoncodec.Marshal(socialVerificationChallenge{
		CodeDigest:  keys.SocialVerificationCodeDigest(statusToken, code),
		OwnerID:     ownerID,
		StatusToken: statusToken,
	})
	return string(record), err
}

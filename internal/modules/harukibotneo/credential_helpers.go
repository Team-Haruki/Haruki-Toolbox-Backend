package harukibotneo

import (
	"context"
	"crypto/rand"
	"fmt"
	"haruki-suite/utils/database/neopg"
	botUser "haruki-suite/utils/database/neopg/user"
	"math/big"

	"github.com/golang-jwt/jwt/v5"
)

func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func generateUniqueBotID(ctx context.Context, botDB *neopg.Client) (int, error) {
	for range botIDRetries {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(botIDMax-botIDMin+1)))
		if err != nil {
			return 0, err
		}
		botID := int(n.Int64()) + botIDMin
		exists, err := botDB.User.Query().
			Where(botUser.BotIDEQ(botID)).
			Exist(ctx)
		if err != nil {
			return 0, err
		}
		if !exists {
			return botID, nil
		}
	}
	return 0, fmt.Errorf("failed to generate unique bot_id after %d attempts", botIDRetries)
}

func signCredentialJWT(secret, botID, credential string) (string, error) {
	claims := jwt.MapClaims{
		"bot_id":     botID,
		"credential": credential,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

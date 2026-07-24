package usergamebindings

import (
	"errors"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekaiapi"

	"github.com/gofiber/fiber/v3"
)

// The Sekai profile client returns a structured result alongside its error for
// definitive upstream conditions. classifyGameAccountProfileError must prefer
// that structured classification so a non-existent game UID becomes a 400
// (logged at INFO), not a 5xx (logged at ERROR).
func TestClassifyGameAccountProfileError(t *testing.T) {
	t.Parallel()

	upstream := errors.New("this user does not exist: user not found")

	cases := []struct {
		name       string
		resultInfo *sekaiapi.HarukiSekaiAPIResult
		err        error
		wantErr    error // sentinel expected via errors.Is; nil means the wrapped generic failure
		wantCode   int   // resulting fiber code after mapGameAccountOwnershipVerificationError
	}{
		{
			name:    "no error passes through",
			err:     nil,
			wantErr: nil,
		},
		{
			name:       "account missing is classified as not found",
			resultInfo: &sekaiapi.HarukiSekaiAPIResult{ServerAvailable: true, AccountExists: false},
			err:        upstream,
			wantErr:    errGameAccountNotFound,
			wantCode:   fiber.StatusBadRequest,
		},
		{
			name:       "server unavailable takes precedence",
			resultInfo: &sekaiapi.HarukiSekaiAPIResult{ServerAvailable: false, AccountExists: false},
			err:        errors.New("game server under maintenance"),
			wantErr:    errGameAccountServerUnavailable,
			wantCode:   fiber.StatusBadGateway,
		},
		{
			name:       "reachable account with error is a generic request failure",
			resultInfo: &sekaiapi.HarukiSekaiAPIResult{ServerAvailable: true, AccountExists: true},
			err:        errors.New("connection reset"),
			wantErr:    errGameAccountProfileRequestFailed,
			wantCode:   fiber.StatusBadGateway,
		},
		{
			name:     "nil result falls back to generic request failure",
			err:      errors.New("dial timeout"),
			wantErr:  errGameAccountProfileRequestFailed,
			wantCode: fiber.StatusBadGateway,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyGameAccountProfileError(tc.resultInfo, tc.err)
			if tc.wantErr == nil {
				if got != nil {
					t.Fatalf("classifyGameAccountProfileError() = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tc.wantErr) {
				t.Fatalf("classifyGameAccountProfileError() = %v, want errors.Is(_, %v)", got, tc.wantErr)
			}
			if mapped := mapGameAccountOwnershipVerificationError(got); mapped.Code != tc.wantCode {
				t.Fatalf("mapped code = %d, want %d", mapped.Code, tc.wantCode)
			}
		})
	}
}

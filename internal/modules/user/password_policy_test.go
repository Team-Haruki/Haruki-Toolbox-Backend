package user

import "testing"

func TestPasswordLengthPolicies(t *testing.T) {
	t.Parallel()

	t.Run("too short", func(t *testing.T) {
		t.Parallel()
		if !isPasswordTooShort("1234567") {
			t.Fatalf("expected password to be too short")
		}
		if isPasswordTooShort("12345678") {
			t.Fatalf("expected password with length 8 to be allowed")
		}
	})

	t.Run("too long by bytes", func(t *testing.T) {
		t.Parallel()
		if !isPasswordTooLong(string(make([]byte, passwordMaxLengthBytes+1))) {
			t.Fatalf("expected password to be too long")
		}
		if isPasswordTooLong(string(make([]byte, passwordMaxLengthBytes))) {
			t.Fatalf("expected password with max byte length to be allowed")
		}
	})
}

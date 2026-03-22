package user

const (
	PasswordMinLengthChars = 8
	PasswordMaxLengthBytes = 72

	PasswordTooShortMessage = "password must be at least 8 characters"
	PasswordTooLongMessage  = "password is too long (max 72 bytes)"
)

func isPasswordTooShort(password string) bool {
	return len(password) < PasswordMinLengthChars
}

func isPasswordTooLong(password string) bool {
	return len([]byte(password)) > PasswordMaxLengthBytes
}

func IsPasswordTooShort(password string) bool {
	return isPasswordTooShort(password)
}

func IsPasswordTooLong(password string) bool {
	return isPasswordTooLong(password)
}

package user

const (
	PasswordMinLengthChars = 8
	PasswordMaxLengthBytes = 72

	PasswordTooShortMessage = "password must be at least 8 characters"
	PasswordTooLongMessage  = "password is too long (max 72 bytes)"

	passwordMinLengthChars = PasswordMinLengthChars
	passwordMaxLengthBytes = PasswordMaxLengthBytes

	passwordTooShortMessage = PasswordTooShortMessage
	passwordTooLongMessage  = PasswordTooLongMessage
)

func isPasswordTooShort(password string) bool {
	return len(password) < passwordMinLengthChars
}

func isPasswordTooLong(password string) bool {
	return len([]byte(password)) > passwordMaxLengthBytes
}

func IsPasswordTooShort(password string) bool {
	return isPasswordTooShort(password)
}

func IsPasswordTooLong(password string) bool {
	return isPasswordTooLong(password)
}

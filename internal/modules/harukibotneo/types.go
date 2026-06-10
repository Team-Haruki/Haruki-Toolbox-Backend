package harukibotneo

type sendMailPayload struct {
	QQNumber int64 `json:"qq_number"`
}

type registerPayload struct {
	QQNumber         int64  `json:"qq_number"`
	VerificationCode string `json:"verification_code"`
}

type registrationStatusResponse struct {
	Enabled bool `json:"enabled"`
}

type registrationResultData struct {
	BotID      string `json:"bot_id"`
	Credential string `json:"credential"`
}

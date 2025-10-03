package schema

type SuiteDataPrivacySettings struct {
	AllowPublicApi bool `json:"allowPublicApi"`
	AllowSakura    bool `json:"allowSakura"`
	Allow8823      bool `json:"allow8823"`
	AllowResona    bool `json:"allowResona"`
}

type MysekaiDataPrivacySettings struct {
	AllowPublicApi  bool `json:"allowPublicApi"`
	AllowFixtureApi bool `json:"allowFixtureApi"`
	Allow8823       bool `json:"allow8823"`
	AllowResona     bool `json:"allowResona"`
}

package schema

import (
	"fmt"
	"slices"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type UploadLog struct {
	ent.Schema
}

func (UploadLog) Fields() []ent.Field {
	validServers := []string{"jp", "en", "tw", "kr", "cn"}
	validDataTypes := []string{"suite", "mysekai", "mysekai_birthday_party"}
	return []ent.Field{
		field.String("server").
			Comment("jp en tw kr cn").
			Validate(func(s string) error {
				if slices.Contains(validServers, s) {
					return nil
				}
				return fmt.Errorf("invalid server: %s", s)
			}),
		field.String("game_user_id").
			MaxLen(30).Optional(),
		field.String("toolbox_user_id").
			MaxLen(10).
			Optional(),
		field.String("data_type").
			Comment("suite mysekai mysekai_birthday_party").
			Validate(func(s string) error {
				if slices.Contains(validDataTypes, s) {
					return nil
				}
				return fmt.Errorf("invalid data_type: %s", s)
			}),
		field.String("upload_method").
			Comment("manual harukiproxy iosproxy inherit"),
		field.Bool("success"),
		field.String("error_message").
			Optional().
			Nillable(),
		field.Time("upload_time"),
		field.String("client_name").MaxLen(64).Optional().Nillable(),
		field.String("client_version").MaxLen(128).Optional().Nillable(),
		field.String("client_channel").MaxLen(32).Optional().Nillable(),
		field.String("client_metadata_format").MaxLen(16).Optional().Nillable(),
		field.String("protocol_version").MaxLen(16).Optional().Nillable(),
		field.String("platform").MaxLen(16).Optional().Nillable(),
		field.String("os_version").MaxLen(64).Optional().Nillable(),
		field.String("os_build").MaxLen(64).Optional().Nillable(),
		field.String("os_arch").MaxLen(16).Optional().Nillable(),
		field.String("app_arch").MaxLen(16).Optional().Nillable(),
		field.String("failure_stage").MaxLen(32).Optional().Nillable(),
		field.String("error_code").MaxLen(64).Optional().Nillable(),
		field.String("request_id").MaxLen(36).Optional().Nillable(),
		field.String("claimed_game_user_id").MaxLen(30).Optional().Nillable(),
		field.String("oauth_client_id").MaxLen(255).Optional().Nillable(),
		field.Int64("processing_duration_ms").NonNegative().Optional().Nillable(),
		field.Int64("request_bytes").NonNegative().Optional().Nillable(),
		field.Bool("identity_verified").Optional().Nillable(),
		field.Time("received_at").Optional().Nillable(),
	}
}

func (UploadLog) Edges() []ent.Edge {
	return nil
}

func (UploadLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("upload_time"),
		index.Fields("received_at"),
		index.Fields("request_id"),
		index.Fields("upload_method", "protocol_version", "received_at"),
		index.Fields("upload_method", "client_channel", "received_at"),
		index.Fields("upload_method", "platform", "received_at"),
		index.Fields("server", "game_user_id", "upload_time"),
		index.Fields("upload_method", "upload_time"),
		index.Fields("data_type", "upload_time"),
		index.Fields("success", "upload_time"),
	}
}

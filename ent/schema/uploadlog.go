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
			MaxLen(30),
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
	}
}

func (UploadLog) Edges() []ent.Edge {
	return nil
}

func (UploadLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("upload_time"),
		index.Fields("server", "game_user_id", "upload_time"),
		index.Fields("upload_method", "upload_time"),
		index.Fields("data_type", "upload_time"),
		index.Fields("success", "upload_time"),
	}
}

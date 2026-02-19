package schema

import (
	"fmt"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
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
				for _, v := range validServers {
					if s == v {
						return nil
					}
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
				for _, v := range validDataTypes {
					if s == v {
						return nil
					}
				}
				return fmt.Errorf("invalid data_type: %s", s)
			}),
		field.String("upload_method").
			Comment("manual harukiproxy iosproxy inherit"),
		field.Bool("success"),
		field.Time("upload_time"),
	}
}

func (UploadLog) Edges() []ent.Edge {
	return nil
}

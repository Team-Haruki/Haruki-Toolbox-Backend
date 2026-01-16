package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type UploadLog struct {
	ent.Schema
}

func (UploadLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("server").
			Comment("jp en tw kr cn"),
		field.String("game_user_id").
			MaxLen(30),
		field.String("toolbox_user_id").
			MaxLen(10),
		field.String("data_type").
			Comment("suite mysekai mysekai_birthday"),
		field.String("upload_method").
			Comment("manual harukiproxy iosproxy inherit"),
		field.Bool("success"),
	}
}

func (UploadLog) Edges() []ent.Edge {
	return nil
}

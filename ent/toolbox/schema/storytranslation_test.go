package schema

import (
	"slices"
	"testing"

	"entgo.io/ent/dialect/entsql"
)

// The story player reads lines/effects/names by these names, and the worker
// relies on one row per (scenario_id, lang).
func TestStoryTranslationSchema(t *testing.T) {
	def := StoryTranslation{}
	var names []string
	for _, f := range def.Fields() {
		names = append(names, f.Descriptor().Name)
	}
	for _, column := range []string{"scenario_id", "lang", "source_hash", "status", "model", "names", "lines", "effects", "summary", "error", "attempts", "prompt_tokens", "completion_tokens", "created_at", "updated_at"} {
		if !slices.Contains(names, column) {
			t.Errorf("column %s missing (have %v)", column, names)
		}
	}
	unique := false
	for _, idx := range def.Indexes() {
		d := idx.Descriptor()
		if d.Unique && slices.Equal(d.Fields, []string{"scenario_id", "lang"}) {
			unique = true
		}
	}
	if !unique || def.Edges() != nil {
		t.Errorf("want a unique (scenario_id, lang) index and no edges")
	}
	if annotation, ok := def.Annotations()[0].(entsql.Annotation); !ok || annotation.Table != "story_translations" {
		t.Errorf("table name = %+v", def.Annotations())
	}
}

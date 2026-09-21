package msgpackcodec

import "fmt"

// JSONOptions controls JSON projection. Its zero value performs conversion
// without application-specific field derivation.
type JSONOptions struct {
	DerivedStringField StringFieldRule
}

// StringFieldRule derives Target from a nonempty string or numeric Source in
// an object reached directly through an ObjectName field. Objects inside an
// intervening array do not inherit the match. The source stays in place; the
// target is emitted at the end of the object. A derived value overrides a
// supplied target; otherwise the supplied target is retained unchanged.
// Duplicate source/target names are rejected. The zero value disables the rule.
type StringFieldRule struct {
	ObjectName string
	Source     string
	Target     string
}

func (o JSONOptions) validate() error {
	r := o.DerivedStringField
	if r == (StringFieldRule{}) {
		return nil
	}
	if r.ObjectName == "" || r.Source == "" || r.Target == "" || r.Source == r.Target {
		return fmt.Errorf("invalid JSON string field rule: require object name and distinct source/target fields")
	}
	return nil
}

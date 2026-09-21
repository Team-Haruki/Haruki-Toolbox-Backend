package data

import "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/msgpackcodec"

// ProviderJSONOptions preserves provider userIdString derivation without putting
// game field names in the general MessagePack codec. Return a value so callers
// cannot mutate another request's policy.
func ProviderJSONOptions() msgpackcodec.JSONOptions {
	return msgpackcodec.JSONOptions{DerivedStringField: msgpackcodec.StringFieldRule{
		ObjectName: fieldUserGamedata, Source: fieldUserID, Target: fieldUserIDString,
	}}
}

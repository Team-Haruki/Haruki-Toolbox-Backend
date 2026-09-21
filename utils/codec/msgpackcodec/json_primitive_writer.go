package msgpackcodec

import (
	"encoding/json/jsontext"

	"math"
)

func writeJSONString(w *jsontext.Encoder, s []byte) error {
	return w.WriteToken(jsontext.String(string(s)))
}

func writeFloat(w *jsontext.Encoder, f float64) error {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return w.WriteToken(jsontext.Null)
	}
	return w.WriteToken(jsontext.Float(f))
}

func skipAndWriteNull(r *jsonReader, w *jsontext.Encoder, n int) error {
	if _, err := r.take(n); err != nil {
		return err
	}
	return w.WriteToken(jsontext.Null)
}

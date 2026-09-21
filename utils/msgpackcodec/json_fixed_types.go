package msgpackcodec

import (
	"encoding/json/jsontext"
)

func writeFixedCollectionOrString(r *jsonReader, w *jsontext.Encoder, typeByte byte, objectName string) error {
	if typeByte >= msgpackFixMapMin && typeByte <= msgpackFixMapMax {
		return writeMap(r, w, int(typeByte&0x0f), objectName)
	}
	if typeByte >= msgpackFixArrMin && typeByte <= msgpackFixArrMax {
		return writeArray(r, w, int(typeByte&0x0f))
	}
	if typeByte >= msgpackFixStrMin && typeByte <= msgpackFixStrMax {
		return writeString(r, w, int(typeByte&0x1f))
	}
	return errNotFixedType
}

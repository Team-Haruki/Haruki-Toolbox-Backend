package streamjson

import (
	"bytes"
	"io"
)

func writeFixedCollectionOrString(r *bytes.Reader, w io.Writer, typeByte byte, objectName string) error {
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

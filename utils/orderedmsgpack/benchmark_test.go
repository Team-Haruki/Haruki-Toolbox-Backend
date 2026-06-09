package orderedmsgpack

import (
	"encoding/binary"
	"fmt"
	"testing"
)

func benchmarkOrderedMapData(b *testing.B, entries int, nested bool) []byte {
	b.Helper()
	data := appendMapHeader(nil, entries)
	for i := range entries {
		key := fmt.Sprintf("field_%04d", i)
		data = appendString(data, key)
		if nested && i%4 == 0 {
			data = appendMapHeader(data, 3)
			data = appendString(data, "id")
			data = appendInt64(data, int64(i))
			data = appendString(data, "name")
			data = appendString(data, key)
			data = appendString(data, "values")
			data = appendArrayHeader(data, 3)
			data = appendInt64(data, int64(i))
			data = appendInt64(data, int64(i+1))
			data = appendInt64(data, int64(i+2))
			continue
		}
		data = appendInt64(data, int64(i))
	}
	return data
}

func BenchmarkMsgpackToOrderedMapSmall(b *testing.B) {
	data := benchmarkOrderedMapData(b, 8, false)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if _, err := MsgpackToOrderedMap(data); err != nil {
			b.Fatalf("MsgpackToOrderedMap: %v", err)
		}
	}
}

func BenchmarkMsgpackToOrderedMapNested(b *testing.B) {
	data := benchmarkOrderedMapData(b, 128, true)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if _, err := MsgpackToOrderedMap(data); err != nil {
			b.Fatalf("MsgpackToOrderedMap: %v", err)
		}
	}
}

func BenchmarkMsgpackToOrderedMapLargeArray(b *testing.B) {
	data := appendMapHeader(nil, 2)
	data = appendString(data, "values")
	data = appendArrayHeader(data, 4096)
	for i := range 4096 {
		data = appendInt64(data, int64(i))
	}
	data = appendString(data, "label")
	data = appendString(data, "large-array")
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if _, err := MsgpackToOrderedMap(data); err != nil {
			b.Fatalf("MsgpackToOrderedMap: %v", err)
		}
	}
}

func appendMapHeader(data []byte, n int) []byte {
	switch {
	case n <= 15:
		return append(data, msgpackFixMapMin|byte(n))
	case n <= 0xffff:
		data = append(data, msgpackMap16, 0, 0)
		binary.BigEndian.PutUint16(data[len(data)-2:], uint16(n))
		return data
	default:
		data = append(data, msgpackMap32, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(data[len(data)-4:], uint32(n))
		return data
	}
}

func appendArrayHeader(data []byte, n int) []byte {
	switch {
	case n <= 15:
		return append(data, msgpackFixArrMin|byte(n))
	case n <= 0xffff:
		data = append(data, msgpackArray16, 0, 0)
		binary.BigEndian.PutUint16(data[len(data)-2:], uint16(n))
		return data
	default:
		data = append(data, msgpackArray32, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(data[len(data)-4:], uint32(n))
		return data
	}
}

func appendString(data []byte, s string) []byte {
	n := len(s)
	switch {
	case n <= 31:
		data = append(data, msgpackFixStrMin|byte(n))
	case n <= 0xff:
		data = append(data, msgpackStr8, byte(n))
	case n <= 0xffff:
		data = append(data, msgpackStr16, 0, 0)
		binary.BigEndian.PutUint16(data[len(data)-2:], uint16(n))
	default:
		data = append(data, msgpackStr32, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(data[len(data)-4:], uint32(n))
	}
	return append(data, s...)
}

func appendInt64(data []byte, v int64) []byte {
	switch {
	case v >= 0 && v <= msgpackFixPosIntMax:
		return append(data, byte(v))
	case v >= 0 && v <= 0xff:
		return append(data, msgpackUint8, byte(v))
	case v >= 0 && v <= 0xffff:
		data = append(data, msgpackUint16, 0, 0)
		binary.BigEndian.PutUint16(data[len(data)-2:], uint16(v))
		return data
	case v >= 0 && v <= 0xffffffff:
		data = append(data, msgpackUint32, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(data[len(data)-4:], uint32(v))
		return data
	case v >= -32 && v < 0:
		return append(data, byte(int8(v)))
	case v >= -128 && v < 0:
		return append(data, msgpackInt8, byte(int8(v)))
	case v >= -32768 && v < 0:
		data = append(data, msgpackInt16, 0, 0)
		binary.BigEndian.PutUint16(data[len(data)-2:], uint16(int16(v)))
		return data
	case v >= -2147483648 && v < 0:
		data = append(data, msgpackInt32, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(data[len(data)-4:], uint32(int32(v)))
		return data
	default:
		data = append(data, msgpackInt64, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(data[len(data)-8:], uint64(v))
		return data
	}
}

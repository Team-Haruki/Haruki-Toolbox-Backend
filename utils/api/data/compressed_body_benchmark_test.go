package data

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/klauspost/compress/gzip"
)

var benchmarkCompressedGameDataBody string

// Keep the previous allocation strategy as an in-process benchmark baseline.
func compressGameDataBodyUnpooled(encoded []byte) (string, error) {
	var buffer bytes.Buffer
	buffer.Grow(len(encoded)/4 + 64)
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestSpeed)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(encoded); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func benchmarkGameDataBody(size int) []byte {
	var buffer bytes.Buffer
	buffer.Grow(size + 256)
	buffer.WriteString(`{"upload_time":1788711433,"userEvents":[`)
	for index := 0; buffer.Len() < size; index++ {
		if index > 0 {
			buffer.WriteByte(',')
		}
		fmt.Fprintf(&buffer, `{"eventId":%d,"eventPoint":%d,"rank":%d,"name":"snapshot-%d"}`, index, (int64(index)*104729)%144520517, index%10000+1, index%1000)
	}
	buffer.WriteString("]}")
	return buffer.Bytes()
}

func BenchmarkCompressGameDataBody(b *testing.B) {
	for _, size := range []int{1 << 20, 8 << 20} {
		encoded := benchmarkGameDataBody(size)
		for _, implementation := range []struct {
			name     string
			compress func([]byte) (string, error)
		}{
			{name: "Unpooled", compress: compressGameDataBodyUnpooled},
			{name: "Pooled", compress: CompressGameDataBody},
		} {
			b.Run(fmt.Sprintf("%dMiB/%s", size>>20, implementation.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(encoded)))
				for b.Loop() {
					stored, err := implementation.compress(encoded)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkCompressedGameDataBody = stored
				}
			})
		}
	}
}

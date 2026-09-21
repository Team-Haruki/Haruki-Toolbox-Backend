package msgpackcodec

import (
	"encoding/binary"
	"fmt"
)

// cursor borrows immutable input for the duration of an operation. Durable
// decoded strings and binary payloads own their storage.
type cursor struct {
	data []byte
	off  int
}

func (d *cursor) readByte() (byte, error) {
	if d.off >= len(d.data) {
		return 0, fmt.Errorf("unexpected EOF at offset %d", d.off)
	}
	b := d.data[d.off]
	d.off++
	return b, nil
}

func (d *cursor) readUint8() (uint8, error) {
	b, err := d.readByte()
	return uint8(b), err
}

func (d *cursor) readUint16() (uint16, error) {
	start, err := d.reserve(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(d.data[start:d.off]), nil
}

func (d *cursor) readUint32() (uint32, error) {
	start, err := d.reserve(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(d.data[start:d.off]), nil
}

func (d *cursor) readUint64() (uint64, error) {
	start, err := d.reserve(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(d.data[start:d.off]), nil
}

func (d *cursor) readBytesCopy(n int) ([]byte, error) {
	start, err := d.reserve(n)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, d.data[start:d.off])
	return out, nil
}

func (d *cursor) reserve(n int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("negative length %d", n)
	}
	if len(d.data)-d.off < n {
		return 0, fmt.Errorf("unexpected EOF at offset %d: need %d bytes, have %d", d.off, n, len(d.data)-d.off)
	}
	start := d.off
	d.off += n
	return start, nil
}

func (d *cursor) remaining() int { return len(d.data) - d.off }
func (d *cursor) take(n int) ([]byte, error) {
	start, err := d.reserve(n)
	if err != nil {
		return nil, err
	}
	return d.data[start:d.off], nil
}
func (d *cursor) unreadByte() error {
	if d.off == 0 {
		return fmt.Errorf("unread at start")
	}
	d.off--
	return nil
}

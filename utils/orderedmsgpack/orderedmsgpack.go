package orderedmsgpack

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"reflect"

	"github.com/iancoleman/orderedmap"
	"github.com/shamaton/msgpack/v3"
	"github.com/shamaton/msgpack/v3/def"
	"github.com/shamaton/msgpack/v3/ext"
)

func (iom internalOrderedMap) ToOrderedMap() (orderedmap.OrderedMap, error) {
	om := orderedmap.New()
	om.SetEscapeHTML(false)
	if len(iom.Keys) != len(iom.Vals) {
		return *om, fmt.Errorf("ordered map ext keys/vals length mismatch: keys=%d vals=%d", len(iom.Keys), len(iom.Vals))
	}
	for i, key := range iom.Keys {
		om.Set(key, iom.Vals[i])
	}
	return *om, nil
}

func newInternalMap(om orderedmap.OrderedMap) internalOrderedMap {
	keys := om.Keys()
	v := internalOrderedMap{
		Keys: keys,
		Vals: make([]any, 0, len(keys)),
	}
	for _, key := range keys {
		val, _ := om.Get(key)
		v.Vals = append(v.Vals, val)
	}
	return v
}

func (d *OrderedMapDecoder) Code() int8 {
	return orderedMapExtCode
}

func (d *OrderedMapDecoder) IsType(offset int, data *[]byte) bool {
	code, offset := d.ReadSize1(offset, data)
	typeOffset, ok := extPayloadTypeOffset(code, offset, data)
	if !ok {
		return false
	}
	t, _ := d.ReadSize1(typeOffset, data)
	return int8(t) == d.Code()
}

func (d *OrderedMapDecoder) AsValue(offset int, k reflect.Kind, data *[]byte) (any, int, error) {
	code, offset := d.ReadSize1(offset, data)

	switch code {
	case def.Ext8, def.Ext16, def.Ext32:
		size, offset, err := readExtSize(code, offset, d, data)
		if err != nil {
			return nil, 0, err
		}
		_, offset = d.ReadSize1(offset, data)
		extData, offset := d.ReadSizeN(offset, size, data)

		var iom internalOrderedMap
		err = msgpack.Unmarshal(extData, &iom)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal ordered map data: %w", err)
		}

		om, err := iom.ToOrderedMap()
		if err != nil {
			return nil, 0, fmt.Errorf("invalid ordered map ext payload: %w", err)
		}

		return om, offset, nil
	}
	return nil, 0, fmt.Errorf("should not reach this line!! code %x decoding %v", d.Code(), k)
}

func extPayloadTypeOffset(code byte, offset int, data *[]byte) (int, bool) {
	switch code {
	case def.Ext8:
		return offset + def.Byte1, offset+def.Byte1 < len(*data)
	case def.Ext16:
		return offset + def.Byte2, offset+def.Byte2 < len(*data)
	case def.Ext32:
		return offset + def.Byte4, offset+def.Byte4 < len(*data)
	default:
		return 0, false
	}
}

func readExtSize(code byte, offset int, r *OrderedMapDecoder, data *[]byte) (int, int, error) {
	switch code {
	case def.Ext8:
		size, offset := r.ReadSize1(offset, data)
		return int(size), offset, nil
	case def.Ext16:
		size, offset := r.ReadSize2(offset, data)
		return int(binary.BigEndian.Uint16(size)), offset, nil
	case def.Ext32:
		size, offset := r.ReadSize4(offset, data)
		return int(binary.BigEndian.Uint32(size)), offset, nil
	default:
		return 0, offset, fmt.Errorf("unsupported ordered map ext code: %x", code)
	}
}

func (d *OrderedMapStreamDecoder) Code() int8 {
	return orderedMapExtCode
}

func (d *OrderedMapStreamDecoder) IsType(code byte, innerType int8, _ int) bool {
	return (code == def.Ext8 || code == def.Ext16 || code == def.Ext32) && innerType == d.Code()
}

func (d *OrderedMapStreamDecoder) ToValue(code byte, data []byte, k reflect.Kind) (any, error) {
	if code == def.Ext8 || code == def.Ext16 || code == def.Ext32 {
		var iom internalOrderedMap
		err := msgpack.Unmarshal(data, &iom)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal ordered map data: %w", err)
		}

		om, err := iom.ToOrderedMap()
		if err != nil {
			return nil, fmt.Errorf("invalid ordered map ext payload: %w", err)
		}

		return om, nil
	}
	return nil, fmt.Errorf("should not reach this line!! code %x decoding %v", d.Code(), k)
}

func (e *OrderedMapEncoder) Code() int8 {
	return orderedMapExtCode
}

func (e *OrderedMapEncoder) Type() reflect.Type {
	return reflect.TypeFor[orderedmap.OrderedMap]()
}

func (e *OrderedMapEncoder) CalcByteSize(value reflect.Value) (int, error) {
	om := value.Interface().(orderedmap.OrderedMap)
	v := newInternalMap(om)

	data, err := msgpack.Marshal(v)
	if err != nil {
		return 0, err
	}

	headerSize, err := orderedMapExtHeaderSize(len(data))
	if err != nil {
		return 0, err
	}
	return headerSize + len(data), nil
}

func (e *OrderedMapEncoder) WriteToBytes(value reflect.Value, offset int, bytes *[]byte) int {
	om := value.Interface().(orderedmap.OrderedMap)
	v := newInternalMap(om)
	data, _ := msgpack.Marshal(v)

	offset = writeOrderedMapExtHeaderToBytes(e, len(data), offset, bytes)
	offset = e.SetByte1Int(int(e.Code()), offset, bytes)
	offset = e.SetBytes(data, offset, bytes)
	return offset
}

func (e *OrderedMapStreamEncoder) Code() int8 {
	return orderedMapExtCode
}

func (e *OrderedMapStreamEncoder) Type() reflect.Type {
	return reflect.TypeFor[orderedmap.OrderedMap]()
}

func (e *OrderedMapStreamEncoder) Write(w ext.StreamWriter, value reflect.Value) error {
	om := value.Interface().(orderedmap.OrderedMap)
	v := newInternalMap(om)

	data, err := msgpack.Marshal(v)
	if err != nil {
		return err
	}

	if err := writeOrderedMapExtHeaderToStream(w, len(data)); err != nil {
		return err
	}
	if err := w.WriteByte1Int(int(e.Code())); err != nil {
		return err
	}
	if err := w.WriteBytes(data); err != nil {
		return err
	}

	return nil
}

func RegisterOrderedMapExt() error {
	if err := msgpack.AddExtCoder(&OrderedMapEncoder{}, &OrderedMapDecoder{}); err != nil {
		return fmt.Errorf("failed to register OrderedMap ext coder: %w", err)
	}
	if err := msgpack.AddExtStreamCoder(&OrderedMapStreamEncoder{}, &OrderedMapStreamDecoder{}); err != nil {
		return fmt.Errorf("failed to register OrderedMap stream ext coder: %w", err)
	}

	return nil
}

func orderedMapExtHeaderSize(payloadLen int) (int, error) {
	switch {
	case payloadLen < 0:
		return 0, fmt.Errorf("negative ordered map ext payload length: %d", payloadLen)
	case payloadLen <= math.MaxUint8:
		return def.Byte1 + def.Byte1 + def.Byte1, nil
	case payloadLen <= math.MaxUint16:
		return def.Byte1 + def.Byte2 + def.Byte1, nil
	case uint64(payloadLen) <= math.MaxUint32:
		return def.Byte1 + def.Byte4 + def.Byte1, nil
	default:
		return 0, fmt.Errorf("ordered map ext payload too large: %d", payloadLen)
	}
}

func writeOrderedMapExtHeaderToBytes(e *OrderedMapEncoder, payloadLen int, offset int, bytes *[]byte) int {
	switch {
	case payloadLen <= math.MaxUint8:
		offset = e.SetByte1Int(def.Ext8, offset, bytes)
		offset = e.SetByte1Int(payloadLen, offset, bytes)
	case payloadLen <= math.MaxUint16:
		offset = e.SetByte1Int(def.Ext16, offset, bytes)
		offset = e.SetByte2Int(payloadLen, offset, bytes)
	default:
		offset = e.SetByte1Int(def.Ext32, offset, bytes)
		offset = e.SetByte4Int(payloadLen, offset, bytes)
	}
	return offset
}

func writeOrderedMapExtHeaderToStream(w ext.StreamWriter, payloadLen int) error {
	switch {
	case payloadLen <= math.MaxUint8:
		if err := w.WriteByte1Int(def.Ext8); err != nil {
			return err
		}
		return w.WriteByte1Int(payloadLen)
	case payloadLen <= math.MaxUint16:
		if err := w.WriteByte1Int(def.Ext16); err != nil {
			return err
		}
		return w.WriteByte2Int(payloadLen)
	default:
		if err := w.WriteByte1Int(def.Ext32); err != nil {
			return err
		}
		return w.WriteByte4Int(payloadLen)
	}
}

func Marshal(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

func Unmarshal(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}

func MarshalWrite(w io.Writer, v any) error {
	return msgpack.MarshalWrite(w, v)
}

func UnmarshalRead(r io.Reader, v any) error {
	return msgpack.UnmarshalRead(r, v)
}

func MsgpackToOrderedMap(b []byte) (*orderedmap.OrderedMap, error) {
	om, err := decodeOrderedMapBytes(b)
	if err != nil {
		return nil, fmt.Errorf("decode msgpack: %w", err)
	}
	return om, nil
}

func MsgpackToOrderedMapFromReader(r io.Reader) (*orderedmap.OrderedMap, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read all: %w", err)
	}
	return MsgpackToOrderedMap(data)
}

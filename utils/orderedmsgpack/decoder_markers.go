package orderedmsgpack

const (
	msgpackFixPosIntMax = 0x7f
	msgpackFixNegIntMin = 0xe0

	msgpackFixMapMin = 0x80
	msgpackFixMapMax = 0x8f
	msgpackFixArrMin = 0x90
	msgpackFixArrMax = 0x9f
	msgpackFixStrMin = 0xa0
	msgpackFixStrMax = 0xbf

	msgpackNil   = 0xc0
	msgpackFalse = 0xc2
	msgpackTrue  = 0xc3

	msgpackBin8  = 0xc4
	msgpackBin16 = 0xc5
	msgpackBin32 = 0xc6
	msgpackExt8  = 0xc7
	msgpackExt16 = 0xc8
	msgpackExt32 = 0xc9

	msgpackFloat32  = 0xca
	msgpackFloat64  = 0xcb
	msgpackUint8    = 0xcc
	msgpackUint16   = 0xcd
	msgpackUint32   = 0xce
	msgpackUint64   = 0xcf
	msgpackInt8     = 0xd0
	msgpackInt16    = 0xd1
	msgpackInt32    = 0xd2
	msgpackInt64    = 0xd3
	msgpackFixExt1  = 0xd4
	msgpackFixExt2  = 0xd5
	msgpackFixExt4  = 0xd6
	msgpackFixExt8  = 0xd7
	msgpackFixExt16 = 0xd8
	msgpackStr8     = 0xd9
	msgpackStr16    = 0xda
	msgpackStr32    = 0xdb
	msgpackArray16  = 0xdc
	msgpackArray32  = 0xdd
	msgpackMap16    = 0xde
	msgpackMap32    = 0xdf
)

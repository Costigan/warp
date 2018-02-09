package ccsds

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"unsafe"
)

// TelemetryDictionary describes a list of packets each containing a list of points
type TelemetryDictionary struct {
	Packets          []PacketInfo
	Units            []string
	FirstEnum        string
	LastEnum         string
	ListConversions  []DiscreteConversionList
	MapConversions   []DiscreteConversionMap
	RangeConversions []DiscreteConversionRangeList
	PolyConversions  []PolynomialConversion
	PacketLookup     [2048]*PacketInfo
}

// PacketInfo describes a single packet
type PacketInfo struct {
	APID          int32
	Documentation string
	ID            string `json:"Id"`
	IsTable       bool
	Name          string
	Points        []PointInfo
}

// PointInfo describes a single telemetry point, providing the information needed to extract its value from a binary packet
type PointInfo struct {
	APID          int32
	Documentation string
	FieldType     byte
	ID            string `json:"Id"`
	Name          string
	UnitsIndex    int
	BitStart      uint `json:"bit_start"`
	BitStop       uint `json:"bit_stop"`
	ByteOffset    uint `json:"byte_offset"`
	ByteSize      uint `json:"byte_size"`

	ConversionFunction *ConversionName
	Conversion         IConversion
}

//
// Conversions
//

type ConversionName struct {
	Name string
}

// IConversion is a raw to engineering units conversion
type IConversion interface {
	GetName() string
	convert(interface{}) (interface{}, error)
	ReturnedType() byte
}

type ConversionBase struct {
	Name string
}

func (c *ConversionBase) GetName() string {
	return c.Name
}

type IdentityConversion struct {
	ConversionBase
}

func (c *IdentityConversion) ReturnedType() byte {
	return Raw
}

func (c *IdentityConversion) convert(v interface{}) (interface{}, error) {
	return v, nil
}

//
// Time conversions
//

type Time42Conversion struct {
	ConversionBase
}

func (c *Time42Conversion) ReturnedType() byte {
	return String
}

func (c *Time42Conversion) convert(v interface{}) (interface{}, error) {
	v1, ok := v.(uint64)
	if !ok {
		return "bad_time_conversion", fmt.Errorf("bad time value: %v", v)
	}
	return Time42ToITOS(v1), nil
}

var time42ConversionSingleton = new(Time42Conversion)

// DiscreteConversionList describes an enumeration with a contiguous set of values
type DiscreteConversionList struct {
	ConversionBase
	Values   []string
	LowIndex int
}

func (c *DiscreteConversionList) ReturnedType() byte {
	return Enum
}

func (c *DiscreteConversionList) convert(v interface{}) (interface{}, error) {
	raw, ok := v.(int)
	if !ok {
		return "Illegal_conversion", nil
	}
	var idx = raw - c.LowIndex
	if idx >= 0 && idx < len(c.Values) {
		return c.Values[idx], nil
	}
	return "Illegal_conversion", nil
}

// DiscreteConversionMap describes an enumeration non-contiguous set of values
type DiscreteConversionMap struct {
	ConversionBase
	Values  []string
	Indices []int
}

func (c *DiscreteConversionMap) ReturnedType() byte {
	return Enum
}

func (c *DiscreteConversionMap) convert(v interface{}) (interface{}, error) {
	raw, ok := v.(int)
	if !ok {
		return "Illegal_conversion", nil
	}
	for i := range c.Indices {
		if c.Indices[i] == raw {
			return c.Values[i], nil
		}
	}
	return "Illegal_conversion", nil
}

// DiscreteConversionRangeList describes an enumeration with multiple values mapping to single strings
type DiscreteConversionRangeList struct {
	ConversionBase
	Ranges []DiscreteConversionRange
}

func (c *DiscreteConversionRangeList) ReturnedType() byte {
	return Enum
}

func (c *DiscreteConversionRangeList) convert(v interface{}) (interface{}, error) {
	raw, ok := v.(int)
	if !ok {
		return "Illegal_conversion", nil
	}
	for i := range c.Ranges {
		if c.Ranges[i].Low <= raw && raw <= c.Ranges[i].High {
			return c.Ranges[i].Value, nil
		}
	}
	return "Illegal_conversion", nil
}

// DiscreteConversionRange describes a contiguous range of values that map to a single string; used by DiscreteConversionRangeList
type DiscreteConversionRange struct {
	ConversionBase
	Low   int
	High  int
	Value string
}

// PolynomialConversion describes a polynomial engineering function
type PolynomialConversion struct {
	ConversionBase
	Name         string
	Order        int
	Coefficients []float64
}

func (c *PolynomialConversion) ReturnedType() byte {
	return Number
}

func (conv *PolynomialConversion) convert(v interface{}) (interface{}, error) {
	var raw float64
	switch v1 := v.(type) {
	case float32:
		raw = float64(v1)
	case float64:
		raw = float64(v1)
	case string:
		return "Illegal_conversion", nil
	case byte:
		raw = float64(v1)
	case int16:
		raw = float64(v1)
	case int32:
		raw = float64(v1)
	case int64:
		raw = float64(v1)
	case uint16:
		raw = float64(v1)
	case uint32:
		raw = float64(v1)
	case uint64:
		raw = float64(v1)
	default:
		return "Illegal_conversion", nil
	}
	c := conv.Coefficients
	switch order := conv.Order; order {
	case 0:
		return c[0], nil
	case 1:
		return c[0] + raw*c[1], nil
	case 2:
		return c[0] + raw*c[1] + raw*raw*c[2], nil
	case 3:
		{
			sum := c[0]
			r := raw
			sum += c[1] * r
			r *= raw
			sum += c[2] * r
			r *= raw
			sum += c[3] * r
			return sum, nil
		}
	case 4:
		{
			sum := c[0]
			r := raw
			sum += c[1] * r
			r *= raw
			sum += c[2] * r
			r *= raw
			sum += c[3] * r
			r *= raw
			sum += c[4] * r
			return sum, nil
		}
	case 5:
		{
			sum := c[0]
			r := raw
			sum += c[1] * r
			r *= raw
			sum += c[2] * r
			r *= raw
			sum += c[3] * r
			r *= raw
			sum += c[4] * r
			r *= raw
			sum += c[5] * r
			return sum, nil
		}
	case 6:
		{
			sum := c[0]
			r := raw
			sum += c[1] * r
			r *= raw
			sum += c[2] * r
			r *= raw
			sum += c[3] * r
			r *= raw
			sum += c[4] * r
			r *= raw
			sum += c[5] * r
			r *= raw
			sum += c[6] * r
			return sum, nil
		}
	case 7:
		{
			sum := c[0]
			r := raw
			sum += c[1] * r
			r *= raw
			sum += c[2] * r
			r *= raw
			sum += c[3] * r
			r *= raw
			sum += c[4] * r
			r *= raw
			sum += c[5] * r
			r *= raw
			sum += c[6] * r
			r *= raw
			sum += c[7] * r
			return sum, nil
		}
	default:
		return "Illegal_conversion", nil
	}
}

// Point type constants (they appear in serialized dictionaries)
const (
	F1234     = byte(iota)
	F12345678 = byte(iota)
	I1        = byte(iota)
	I12       = byte(iota)
	I1234     = byte(iota)
	S1        = byte(iota)
	TIME40    = byte(iota)
	TIME42    = byte(iota)
	TIME44    = byte(iota)
	U1        = byte(iota)
	U12       = byte(iota)
	U1234     = byte(iota)
	U12345678 = byte(iota)
	U21       = byte(iota)
	U4321     = byte(iota)
	// These are versions needed for bit extraction
	U1b                  = byte(iota)
	U12b                 = byte(iota)
	U1234b               = byte(iota)
	U4321b               = byte(iota)
	I12b                 = byte(iota)
	I1234b               = byte(iota)
	Pseudo               = byte(iota)
	FullPacketConversion = byte(iota)
	URL                  = byte(iota)
)

// Engineering constants are used by conversion functions
const (
	NoEngineeringType = byte(iota)
	Number            = byte(iota)
	Enum              = byte(iota)
	String            = byte(iota)
	Image             = byte(iota)
	Spectrum          = byte(iota)
	Raw               = byte(iota)
)

var bit32 = [...]uint32{0x0001, 0x0002, 0x0004, 0x0008, 0x0010,
	0x0020, 0x0040, 0x0080, 0x0100, 0x0200, 0x0400, 0x0800, 0x1000,
	0x2000, 0x4000, 0x8000, 0x10000, 0x20000, 0x40000, 0x80000, 0x100000,
	0x200000, 0x400000, 0x800000, 0x1000000, 0x2000000, 0x4000000,
	0x8000000, 0x10000000, 0x20000000, 0x40000000, 0x80000000}

var mask32 = [...]uint32{0x0, 0x1, 0x3, 0x7, 0xF, 0x1F, 0x3F, 0x7F,
	0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF, 0x1FFF, 0x3FFF, 0x7FFF, 0xFFFF,
	0x1FFFF, 0x3FFFF, 0x7FFFF, 0xFFFFF, 0x1FFFFF, 0x3FFFFF, 0x7FFFFF,
	0xFFFFFF, 0x1FFFFFF, 0x3FFFFFF, 0x7FFFFFF, 0xFFFFFFF, 0x1FFFFFFF,
	0x3FFFFFFF, 0x7FFFFFFF, 0xFFFFFFFF}

// GetValue extracts the engineering value of a telemetry point
// The engineering value is the conversion function applied to the raw value
func (point *PointInfo) GetValue(p *Packet) (v interface{}, err error) {
	raw, err := point.GetRawValue(p)
	if err != nil {
		return nil, fmt.Errorf("Decomm raw extraction error in %s: %v", point.Name, err)
	}

	if point.ID == "hazcamio_dat.CCSDS_TIME_324" {
		fmt.Println("here")
	}

	if point.Conversion == nil {
		return raw, nil
	}
	v, err2 := (point.Conversion).convert(raw)
	if err2 != nil {
		return v, fmt.Errorf("Decomm coercion error in %s: %v", point.Name, err2)
	}
	return v, nil
}

var decomDispatchArray [URL + 1]func(point *PointInfo, p *Packet) (interface{}, error)

// GetRawValue returns the point's value (of type FieldType) extracted from the packet
func (point *PointInfo) GetRawValue(p *Packet) (interface{}, error) {
	decommer := decomDispatchArray[point.FieldType]
	if decommer == nil {
		return nil, fmt.Errorf("No decom handler for field type %d", point.FieldType)
	}
	return decommer(point, p)
}

func init() {

	decomDispatchArray[F1234] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+3 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[o]) << 24) | (uint32(pkt[1+o]) << 16) | (uint32(pkt[2+o]) << 8) | uint32(pkt[3+o])
		return *(*float32)(unsafe.Pointer(&v)), nil
	}

	decomDispatchArray[F12345678] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+7 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint64(pkt[0+o]) << 56) | (uint64(pkt[1+o]) << 48) |
			(uint64(pkt[2+o]) << 40) | (uint64(pkt[3+o]) << 32) |
			(uint64(pkt[4+o]) << 24) | (uint64(pkt[5+o]) << 16) |
			(uint64(pkt[6+o]) << 8) | (uint64(pkt[7+o]))
		return *(*float64)(unsafe.Pointer(&v)), nil
	}

	decomDispatchArray[I1] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		return byte(pkt[o]), nil
	}

	decomDispatchArray[I12] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (int16(pkt[0+o]) << 8) | (int16(pkt[1+o]))
		return v, nil
	}

	decomDispatchArray[I12b] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[0+o]) << 8) | (uint32(pkt[1+o]))
		v = v >> (15 - point.BitStop)
		len := point.BitStop - point.BitStart + 1
		result := uint32(mask32[len] & v)
		isNeg := (bit32[len-1] & result) != 0
		var r int16
		if isNeg {
			result = result - bit32[len]
			r = int16(result)
		}
		return r, nil
	}

	decomDispatchArray[I1234] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+4 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (int32(pkt[0+o]) << 24) | (int32(pkt[1+o]) << 16) | (int32(pkt[2+o]) << 8) | (int32(pkt[3+o]))
		return v, nil
	}

	decomDispatchArray[I1234b] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint64(pkt[0+o]) << 24) | (uint64(pkt[1+o]) << 16) | (uint64(pkt[2+o]) << 8) | (uint64(pkt[3+o]))
		v = v >> (31 - point.BitStop)
		len := point.BitStop - point.BitStart + 1
		result := uint64(mask32[len]) & v
		isNeg := (uint64(bit32[len-1]) & result) != 0
		var r int16
		if isNeg {
			result = result - uint64(bit32[len])
			r = int16(result)
		}
		return r, nil
	}

	decomDispatchArray[S1] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		offset := int(point.ByteOffset)
		packetByteCount := pkt.Length() + 7
		count := min(int(point.ByteSize), packetByteCount-offset)
		for i := 0; i < count; i++ {
			if pkt[i+offset] == 0 {
				count = i
				break
			}
		}
		sbuf := pkt[offset : offset+count]
		s := string(sbuf)
		return s, nil
	}

	decomDispatchArray[URL] = decomDispatchArray[S1]

	decomDispatchArray[TIME40] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if len(pkt) <= int(o+4) {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[0+o]) << 24) | (uint32(pkt[1+o]) << 16) | (uint32(pkt[2+o]) << 8) | (uint32(pkt[3+o]))
		return v, nil
	}

	decomDispatchArray[TIME42] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if len(pkt) <= int(o+6) {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint64(pkt[0+o]) << 40) | (uint64(pkt[1+o]) << 32) | (uint64(pkt[2+o]) << 24) | (uint64(pkt[3+o]) << 16) | (uint64(pkt[4+o]) << 8) | (uint64(pkt[5+o]))
		return v, nil
	}

	decomDispatchArray[TIME44] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if len(pkt) <= int(o+8) {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint64(pkt[0+o]) << 56) | (uint64(pkt[1+o]) << 48) | (uint64(pkt[2+o]) << 40) | (uint64(pkt[3+o]) << 32) | (uint64(pkt[4+o]) << 24) | (uint64(pkt[5+o]) << 16) | (uint64(pkt[6+o]) << 8) | (uint64(pkt[7+o]))
		return v, nil
	}

	decomDispatchArray[U1] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		return uint8(pkt[o]), nil
	}

	decomDispatchArray[U1b] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		len := point.BitStop - point.BitStart + 1
		v := uint32(pkt[o]) >> (7 - point.BitStop)
		v = mask32[len] & v
		return uint8(v), nil
	}

	decomDispatchArray[U12] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint16(pkt[0+o]) << 8) | (uint16(pkt[1+o]))
		return v, nil
	}

	decomDispatchArray[U12b] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[0+o]) << 8) | (uint32(pkt[1+o]))
		v = v >> (15 - point.BitStop)
		len := point.BitStop - point.BitStart + 1
		result := uint16(mask32[len] & v)
		return result, nil
	}

	decomDispatchArray[U1234] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+3 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[0+o]) << 24) | (uint32(pkt[1+o]) << 16) | (uint32(pkt[2+o]) << 8) | (uint32(pkt[3+o]))
		return v, nil
	}

	decomDispatchArray[U12345678] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+7 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint64(pkt[0+o]) << 56) | (uint64(pkt[1+o]) << 48) | (uint64(pkt[2+o]) << 40) | (uint64(pkt[3+o]) << 32) | (uint64(pkt[4+o]) << 24) | (uint64(pkt[5+o]) << 16) | (uint64(pkt[6+o]) << 8) | (uint64(pkt[7+o]))
		return v, nil
	}

	decomDispatchArray[U1234b] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+3 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[0+o]) << 24) | (uint32(pkt[1+o]) << 16) | (uint32(pkt[2+o]) << 8) | (uint32(pkt[3+o]))
		v = v >> (31 - point.BitStop)
		len := point.BitStop - point.BitStart + 1
		result := uint32(mask32[len] & v)
		return result, nil
	}

	decomDispatchArray[U21] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint16(pkt[1+o]) << 8) | (uint16(pkt[o]))
		return v, nil
	}

	decomDispatchArray[U4321] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+3 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[3+o]) << 24) | (uint32(pkt[2+o]) << 16) | (uint32(pkt[1+o]) << 8) | (uint32(pkt[0+o]))
		return v, nil
	}

	decomDispatchArray[U4321b] = func(point *PointInfo, p *Packet) (interface{}, error) {
		pkt := *p
		o := point.ByteOffset
		if uint(len(pkt)) <= o+3 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[3+o]) << 24) | (uint32(pkt[2+o]) << 16) | (uint32(pkt[1+o]) << 8) | (uint32(pkt[o]))
		v = v >> (31 - point.BitStop)
		len := point.BitStop - point.BitStart + 1
		result := uint32(mask32[len] & v)
		return result, nil
	}

	// Not implementing these yet

	decomDispatchArray[Pseudo] = func(point *PointInfo, p *Packet) (interface{}, error) {
		return 0, nil
	}

	decomDispatchArray[FullPacketConversion] = func(point *PointInfo, p *Packet) (interface{}, error) {
		return 0, nil
	}

}

func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// LoadDictionary ...
func LoadDictionary(filename string) (*TelemetryDictionary, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("error opening dictionary in %s:%v", filename, err)
	}
	defer f.Close()

	breader := bufio.NewReader(f)

	var reader io.Reader = breader
	if path.Ext(filename) == ".gz" {
		if reader, err = gzip.NewReader(breader); err != nil {
			return nil, fmt.Errorf("Error opening gzipped file %s:%v", filename, err)
		}
	}

	var dictionary TelemetryDictionary
	if err = json.NewDecoder(reader).Decode(&dictionary); err != nil {
		return nil, fmt.Errorf("error deserializing dictionary in %s:%v", filename, err)
	}

	// Copy packet pointers to a lookup array for faster access
	for i, info := range dictionary.Packets {
		apid := info.APID
		// This silently ignores tables and malformed apids.  Maybe this should say something
		if apid >= 0 && apid < 2048 {
			if dictionary.PacketLookup[apid] == nil {
				dictionary.PacketLookup[apid] = &dictionary.Packets[i]
			}
		}
	}

	propagateConverters(&dictionary)

	return &dictionary, nil
}

func propagateConverters(dictionary *TelemetryDictionary) {
	m := make(map[string]IConversion)
	for i, c := range dictionary.ListConversions {
		m[c.Name] = &dictionary.ListConversions[i]
	}
	for i, c := range dictionary.MapConversions {
		m[c.Name] = &dictionary.MapConversions[i]
	}
	for i, c := range dictionary.RangeConversions {
		m[c.Name] = &dictionary.RangeConversions[i]
	}
	for i, c := range dictionary.PolyConversions {
		m[c.Name] = &dictionary.PolyConversions[i]
	}

	for _, pkt := range dictionary.Packets {
		for j, point := range pkt.Points {

			if point.ID == "hazcamio_dat.CCSDS_TIME_324" {
				fmt.Println("here")
			}

			// Handle time conversions
			if point.FieldType == TIME42 {
				point.Conversion = time42ConversionSingleton
				if pkt.Points[j].Conversion == nil {
					fmt.Println("there")
				}
			}

			f := point.ConversionFunction
			if f == nil {
				continue
			}
			if val, ok := m[f.Name]; ok {
				point.Conversion = val
			}
		}
	}
}

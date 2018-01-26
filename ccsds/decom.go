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

	//ConversionFunction
	Conversion func(v interface{}) (interface{}, error)
}

// DiscreteConversionList describes an enumeration with a contiguous set of values
type DiscreteConversionList struct {
	Name     string
	Values   []string
	LowIndex int
}

// DiscreteConversionMap describes an enumeration non-contiguous set of values
type DiscreteConversionMap struct {
	Name    string
	Values  []string
	Indices []int
}

// DiscreteConversionRangeList describes an enumeration with multiple values mapping to single strings
type DiscreteConversionRangeList struct {
	Name   string
	Ranges []DiscreteConversionRange
}

// DiscreteConversionRange describes a contiguous range of values that map to a single string; used by DiscreteConversionRangeList
type DiscreteConversionRange struct {
	Low   int
	High  int
	Value string
}

// PolynomialConversion describes a polynomial engineering function
type PolynomialConversion struct {
	Name         string
	Order        int
	Coefficients []float64
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
func (point PointInfo) GetValue(p Packet) (v interface{}, err error) {
	raw, err := point.GetRawValue(p)
	if err != nil {
		return nil, fmt.Errorf("Decomm raw extraction error in %s: %v", point.Name, err)
	}
	v, err2 := point.Conversion(raw)
	if err2 != nil {
		return v, fmt.Errorf("Decomm coercion error in %s: %v", point.Name, err2)
	}
	return v, nil
}

var decomDispatchArray [URL + 1]func(point PointInfo, p Packet) (interface{}, error)

// GetRawValue returns the point's value (of type FieldType) extracted from the packet
func (point PointInfo) GetRawValue(p Packet) (interface{}, error) {
	return decomDispatchArray[point.FieldType](point, p)
}

func init() {

	decomDispatchArray[F1234] = func(point PointInfo, pkt Packet) (interface{}, error) {
		o := point.ByteOffset
		if uint(len(pkt)) <= o+3 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[o]) << 24) | (uint32(pkt[1+o]) << 16) | (uint32(pkt[2+o]) << 8) | uint32(pkt[3+o])
		return *(*float32)(unsafe.Pointer(&v)), nil
	}

	decomDispatchArray[F12345678] = func(point PointInfo, pkt Packet) (interface{}, error) {
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

	decomDispatchArray[I1] = func(point PointInfo, pkt Packet) (interface{}, error) {
		o := point.ByteOffset
		if uint(len(pkt)) <= o {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		return byte(pkt[o]), nil
	}

	decomDispatchArray[I12] = func(point PointInfo, pkt Packet) (interface{}, error) {
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (int16(pkt[0+o]) << 8) | (int16(pkt[1+o]))
		return v, nil
	}

	decomDispatchArray[I12b] = func(point PointInfo, pkt Packet) (interface{}, error) {
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (uint32(pkt[0+o]) << 8) | (uint32(pkt[1+o]))
		v = v >> (15 - point.BitStop)
		len := point.BitStop - point.BitStart + 1
		result := uint32(mask32[len]) & v
		isNeg := (bit32[len-1] & result) != 0
		var r int16
		if isNeg {
			result = result - bit32[len]
			r = int16(result)
		}
		return r, nil
	}

	decomDispatchArray[I1234] = func(point PointInfo, pkt Packet) (interface{}, error) {
		o := point.ByteOffset
		if uint(len(pkt)) <= o+1 {
			return nil, fmt.Errorf("short packet:id=%s:byte_offset=%d:packet_len=%d", point.ID, o, len(pkt))
		}
		v := (int32(pkt[0+o]) << 24) | (int32(pkt[1+o]) << 16) | (int32(pkt[2+o]) << 8) | (int32(pkt[3+o]))
		return v, nil
	}

	decomDispatchArray[I1234b] = func(point PointInfo, pkt Packet) (interface{}, error) {
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

	return &dictionary, nil
}

package ccsds

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// TestF1234 tests decomm of F1234 values
func TestF1234(t *testing.T) {
	cases := []float32{0.0, 1.0, -1.0, math.MaxFloat32, -math.MaxFloat32}
	for i := 0; i < 128; i++ {
		cases = append(cases, float32(i))
	}
	for offset := 0; offset < 12; offset++ {
		for _, v1 := range cases {
			buf := new(bytes.Buffer)
			for j := 0; j < offset; j++ {
				binary.Write(buf, binary.BigEndian, byte(0)) // ignoring error
			}
			err := binary.Write(buf, binary.BigEndian, v1)
			if err != nil {
				t.Errorf("binary.Write failed:%s", err)
			}

			packetbuf := buf.Bytes()
			packet := Packet(packetbuf)

			point := PointInfo{FieldType: F1234, BitStart: 0, BitStop: 31, ByteOffset: uint(offset), ByteSize: uint(4)}
			v2, err2 := point.GetValue(&packet)
			if err2 != nil {
				t.Error("error extracting point value")
			}

			if v1 != v2 {
				t.Errorf("values didn't match:%f:%f", v1, v2)
			}
		}
	}
}

// TestF12345678 tests doubles
func TestF12345678(t *testing.T) {
	cases := []float64{0.0, 1.0, -1.0, math.MaxFloat64, -math.MaxFloat64}
	for i := 0; i < 128; i++ {
		cases = append(cases, float64(i))
	}
	for offset := 0; offset < 12; offset++ {
		for _, v1 := range cases {
			buf := new(bytes.Buffer)
			for j := 0; j < offset; j++ {
				binary.Write(buf, binary.BigEndian, byte(0)) // ignoring error
			}
			err := binary.Write(buf, binary.BigEndian, v1)
			if err != nil {
				t.Errorf("binary.Write failed:%s", err)
			}

			packetbuf := buf.Bytes()
			packet := Packet(packetbuf)

			point := PointInfo{FieldType: F12345678, BitStart: 0, BitStop: 63, ByteOffset: uint(offset), ByteSize: uint(8)}
			v2, err2 := point.GetValue(&packet)
			if err2 != nil {
				t.Error("error extracting point value")
			}

			if v1 != v2 {
				t.Errorf("values didn't match:%f:%f", v1, v2)
			}
		}
	}
}

// TestI1 tests decomm of I1 values
func TestI1(t *testing.T) {
	for offset := 0; offset < 12; offset++ {
		for i := 0; i < 2; i++ {
			v1 := byte(i)
			buf := new(bytes.Buffer)
			for j := 0; j < offset; j++ {
				binary.Write(buf, binary.BigEndian, byte(0)) // ignoring error
			}
			err := binary.Write(buf, binary.BigEndian, v1)
			if err != nil {
				t.Errorf("binary.Write failed:%s", err)
			}

			packetbuf := buf.Bytes()
			packet := Packet(packetbuf)

			point := PointInfo{FieldType: I1, BitStart: 0, BitStop: 7, ByteOffset: uint(offset), ByteSize: uint(1)}
			v2, err2 := point.GetValue(&packet)
			if err2 != nil {
				t.Error("error extracting point value")
			}

			if v1 != v2 {
				t.Errorf("values didn't match:%d:%d", v1, v2)
			}
		}
	}
}

// TestI12 tests shorts
func TestI12(t *testing.T) {
	cases := []int16{0.0, 1.0, -1.0, math.MaxInt16, math.MinInt16}
	for i := 0; i < 156; i++ {
		cases = append(cases, int16(i))
	}
	for offset := 0; offset < 12; offset++ {
		for _, v1 := range cases {
			buf := new(bytes.Buffer)
			for j := 0; j < offset; j++ {
				binary.Write(buf, binary.BigEndian, byte(0)) // ignoring error
			}
			err := binary.Write(buf, binary.BigEndian, v1)
			if err != nil {
				t.Errorf("binary.Write failed:%s", err)
			}

			packetbuf := buf.Bytes()
			packet := Packet(packetbuf)

			point := PointInfo{FieldType: I12, BitStart: 0, BitStop: 15, ByteOffset: uint(offset), ByteSize: uint(2)}
			v2, err2 := point.GetValue(&packet)
			if err2 != nil {
				t.Error("error extracting point value")
			}

			if v1 != v2 {
				t.Errorf("values didn't match:%d:%d", v1, v2)
			}
		}
	}
}

//func TestI12b(t *testing.T) {
//	cases := []int16{0.0, 1.0, -1.0, math.MaxInt16, math.MinInt16}
//	for i := 0; i < 156; i++ {
//		cases = append(cases, int16(i))
//	}
//	for offset := 0; offset < 12; offset++ {
//		for _, v1 := range cases {
//			buf := new(bytes.Buffer)
//			for j := 0; j < offset; j++ {
//				binary.Write(buf, binary.BigEndian, byte(0)) // ignoring error
//			}
//			err := binary.Write(buf, binary.BigEndian, v1)
//			if err != nil {
//				t.Errorf("binary.Write failed:%s", err)
//			}
//
//			packetbuf := buf.Bytes()
//			packet := Packet(packetbuf)
//
//			point := PointInfo{FieldType: I12, BitStart: 0, BitStop: 15, ByteOffset: uint(offset), ByteSize: uint(2)}
//			v2, err2 := point.GetValue(packet)
//			if err2 != nil {
//				t.Error("error extracting point value")
//			}
//
//			if v1 != v2 {
//				t.Errorf("values didn't match:%d:%d", v1, v2)
//			}
//		}
//	}
//}
//

// TestS1 tests S1 and URL point types
func TestS1(t *testing.T) {
	cases := []string{"", "a", "ab", "abc", "abcd"}
	for offset := 0; offset < 12; offset++ {
		for _, v1 := range cases {
			buf := generateCCSDSHeader(1, 2, offset+len(v1))
			for j := 0; j < offset; j++ {
				binary.Write(buf, binary.BigEndian, byte(0)) // ignoring error
			}
			for _, c := range v1 {
				err := binary.Write(buf, binary.BigEndian, byte(c))
				if err != nil {
					t.Errorf("binary.Write failed:%s", err)
					return
				}
			}

			packetbuf := buf.Bytes()
			packet := Packet(packetbuf)

			apid := packet.APID()
			if apid != 1 {
				t.Errorf("Wrong apid value: expected %d got %d", 1, apid)
			}

			seq := packet.SequenceCount()
			if seq != 2 {
				t.Errorf("Wrong sequence count: expected %d got %d", 1, seq)
			}

			point1 := PointInfo{FieldType: S1, BitStart: 0, BitStop: 15, ByteOffset: uint(offset + 6), ByteSize: uint(len(v1))}
			v2, err2 := point1.GetValue(&packet)
			if err2 != nil {
				t.Error("error extracting point value")
			}

			if v1 != v2 {
				t.Errorf("values didn't match:%s:%s", v1, v2)
			}

			point3 := PointInfo{FieldType: URL, BitStart: 0, BitStop: 15, ByteOffset: uint(offset + 6), ByteSize: uint(len(v1))}
			v3, err3 := point3.GetValue(&packet)
			if err3 != nil {
				t.Error("error extracting point value")
			}

			if v1 != v3 {
				t.Errorf("values didn't match:%s:%s", v1, v3)
			}
		}
	}
}

func generateCCSDSHeader(apid int, seq int, datalen int) *bytes.Buffer {
	capacity := datalen + 6
	len := datalen - 1
	buf := make([]byte, 6, capacity)
	buf[0] = byte(((apid >> 8) & 0x7) | 0x8)
	buf[1] = byte(apid & 0xFF)
	buf[2] = byte(seq>>8) | 192
	buf[3] = byte(seq & 0xFF)
	buf[4] = byte(len >> 8)
	buf[5] = byte(len & 0xFF)
	return bytes.NewBuffer(buf)
}

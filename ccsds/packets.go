package ccsds

import (
	"fmt"
	"io"
	"os"
)

// A Packet is a byte slice
type Packet []byte

// APID returns the CCSDS application ID contained from the header of a packet (a []byte)
func (packet Packet) APID() int {
	return (int(0x7&packet[0]) << 8) + int(packet[1])
}

// StreamID returns the flight software stream ID contained within a packet (a []byte)
// This is the APID with a higher bit set (I think)
func (packet Packet) StreamID() int {
	return int(packet[0])<<8 + int(packet[1])
}

// SequenceCount returns the CCSDS packet sequence counter from the header of a Packet (a []byte)
func (packet Packet) SequenceCount() int {
	return (0x3FFF & (int(packet[2]) << 8)) | int(packet[3])
}

// Length returns the CCSDS packet length field from the header of a Packet (a []byte).  This is
// packet data field length - 1 or the total packet length - 7
func (packet Packet) Length() int {
	return (int(packet[4]) << 8) + int(packet[5])
}

// Time42 returns the CCSDS packet secondary header, which is typically used to hold a packet timestamp
func (packet Packet) Time42() int64 {
	if 8 == (8 & packet[0]) {
		return (int64(packet[6]) << 40) | (int64(packet[7]) << 32) | (int64(packet[8]) << 24) | (int64(packet[9]) << 16) | (int64(packet[10]) << 8) | int64(packet[11])
	}
	return 0
}

// A PacketIterator generates a sequence of packets, calling a function on each
type PacketIterator interface {
	Iterate(f func(p Packet))
}

// PacketFile a binary file containing a sequence of CCSDS packets without any headers.
// It implements PacketIterator
type PacketFile struct {
	Filename string
}

// Iterate reads a packet file, splits into packets and passes each packet to a callback.
// This creates and reuses a byte slice for all packets.  If the callback needs to pass the packet
// to something else, it needs to copy it
func (source PacketFile) Iterate(f func(p Packet)) error {
	file, err := os.Open(source.Filename)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	pktbuf := make(Packet, 65536+7)
	pktptr, err := 0, nil

	for err == nil {
		// Read packet header
		pktptr = 0
		toread := 6
		for toread > 0 {
			var bytesRead, err = file.Read(pktbuf[pktptr : pktptr+toread])
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			toread = toread - bytesRead
			pktptr = pktptr + bytesRead
		}

		if toread > 0 {
			return fmt.Errorf("file ends with partial packet in header: %s", source.Filename)
		}

		// Read the packet body
		var len = pktbuf.Length()
		if len > 65535 {
			filepos, _ := file.Seek(0, 1)
			return fmt.Errorf("packet length out of bounds:%d:%s:%d", len, source.Filename, filepos)
		}
		toread = len + 1
		for toread > 0 {
			var bytesRead, err = file.Read(pktbuf[pktptr : pktptr+toread])
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			toread = toread - bytesRead
			pktptr = pktptr + bytesRead
		}

		if toread > 0 {
			return fmt.Errorf("file ends with partial packet in body: %s", source.Filename)
		}

		// Do the callback
		f(pktbuf)
	}
	return nil
}

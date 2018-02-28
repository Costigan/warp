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

// ReadPacketsCallback reads from a byte stream, identifies CCSDS packet boundaries and passes each packet to a callback
func ReadPacketsCallback(stream io.Reader, callback func(p *Packet)) error {
	return readPacketsInner(stream, make(Packet, 65536+7), callback)
}

// ReadPacketsChannel reads from a byte stream, identifies CCSDS packet boundaries and passes each packet to a channel
func ReadPacketsChannel(stream io.Reader, channel chan *Packet) error {
	return readPacketsInner(stream, make(Packet, 65536+7), func(p *Packet) { channel <- p })
}

func readPacketsInner(stream io.Reader, pktbuf Packet, callback func(p *Packet)) error {
	pktptr, err, totalBytesRead := 0, error(nil), 0
	for err == nil {
		// Read packet header
		pktptr = 0
		toread := 6
		for toread > 0 {
			var bytesRead, err = stream.Read(pktbuf[pktptr : pktptr+toread])
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
			return fmt.Errorf("stream ends with partial packet in the header")
		}

		// Read the packet body
		toread = pktbuf.Length() + 1
		packetLength := toread + 6
		for toread > 0 {
			var bytesRead, err = stream.Read(pktbuf[pktptr : pktptr+toread])
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
			return fmt.Errorf("stream ends with partial packet in the packet body.  Packet length was %d.  Total bytes read was %d", packetLength, totalBytesRead+(packetLength-toread))
		}

		// Do the callback
		callback(&pktbuf)

		totalBytesRead = totalBytesRead + packetLength
	}
	return nil
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
func (source PacketFile) Iterate(callback func(p *Packet)) error {
	file, err := os.Open(source.Filename)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	err = readPacketsInner(file, make(Packet, 65536+7), callback)
	if err != nil {
		return fmt.Errorf("%s: filename=%s", err.Error(), source.Filename)
	}
	return nil
}

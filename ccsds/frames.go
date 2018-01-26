package ccsds

// VCDULength ...
const VCDULength = 1115

// PrimaryHeaderFixedLength ...
const PrimaryHeaderFixedLength int = 6

// FrameErrorControlFieldLength ...
const FrameErrorControlFieldLength int = 0

// VCDUHeaderLength ...
const VCDUHeaderLength int = PrimaryHeaderFixedLength + FrameErrorControlFieldLength

// MPDUStart ...
const MPDUStart int = VCDUHeaderLength + 2

// VCDUTrailerLength ...
const VCDUTrailerLength int = 6

// VCDUDataLength ...
const VCDUDataLength int = VCDULength - VCDUHeaderLength - VCDUTrailerLength

// MPDUPacketZoneLength ...
const MPDUPacketZoneLength int = VCDUDataLength - 2

// FirstHeaderPointerOverflow ...
const FirstHeaderPointerOverflow int = 0x7FF

// MPDUEnd ...
const MPDUEnd int = MPDUStart + MPDUPacketZoneLength

// A Frame is a byte slice
type Frame []byte

// FrameCount returns the virtual channel frame count (wraps at 2^24)
func (frame Frame) FrameCount() int {
	return (int(frame[2]) << 16) | (int(frame[3]) << 8) | int(frame[4])
}

// VirtualChannel returns the virtual channel number [0-511]
func (frame Frame) VirtualChannel() int {
	return int(0x3F & frame[1])
}

// FirstHeaderPointer returns the offset of first packet within the transfer frame data field.
// Assumes there is no Frame Header Error Control field
func (frame Frame) FirstHeaderPointer() int {
	return (int(0x7&frame[VCDUHeaderLength]) << 8) + int(frame[VCDUHeaderLength+1])
}

// SpacecraftID returns the spacecraft id field (8 bits)
func (frame Frame) SpacecraftID() int {
	return (int(0x3F&frame[0]) << 2) | (int(0xC0&frame[1]) >> 6)
}

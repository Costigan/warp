package ccsds

import (
	"fmt"
	"time"
)

// Epoch defines what a system clock value of 0 corresponds to
var Epoch = time.Date(2000, time.January, 1, 12, 0, 0, 0, time.UTC)

const subsecondsToNanoseconds float64 = 1000000000.0 / 64000.0

/*
func Time42ToDuration(time42 int64) time.Duration {
	seconds := time.Duration(time42 >> 16)
	subseconds := float64(time42 & 0xFFFF)
	ticks := seconds*time.Second + time.Duration(subseconds*SubsecondsToNanoseconds)
	return ticks
}*/

// Time42ToDuration converts a time42 telemetry value (uint64) to a go Duration
func Time42ToDuration(time42 uint64) time.Duration {
	const tickResolution int64 = 10000000
	const doubleTickResolution float64 = 10000000
	seconds := int64(time42 >> 16)
	coarseTicks := seconds * tickResolution
	subseconds := int64(time42 & 0xFFFF)
	ffine := float64(subseconds) / 65536.0
	fineTicks := int64(ffine * doubleTickResolution)
	ticks := coarseTicks + fineTicks
	nanoseconds := ticks * 100
	return time.Duration(nanoseconds)
}

// Time42ToITOS returns the timestamp as a human-readable string
func Time42ToITOS(time42 uint64) string {
	dur := Time42ToDuration(time42)
	t := Epoch.Add(dur)
	return ITOSFormat(t)
}

// ITOSFormat converts a time to a string similar to the way ITOS formats it
func ITOSFormat(t time.Time) string {
	return fmt.Sprintf("%02d-%03d-%02d:%02d:%02d.%03d", t.Year()-2000, t.YearDay(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1000000)
}

// Time42ToSeconds extracts the seconds portion of a timestamp
func Time42ToSeconds(time42 uint64) uint32 {
	return uint32(time42 >> 16)
}

// Time42ToSubseconds extracts the subseconds portion of a timestamp
func Time42ToSubseconds(time42 uint64) uint16 {
	return uint16(time42 & 0xFFFF)
}

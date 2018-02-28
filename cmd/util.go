// Copyright © 2018 NAME HERE <EMAIL ADDRESS>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"log"
	"os/user"
	"path/filepath"
	"time"

	"github.com/Costigan/warp/ccsds"
)

func MapOverFiles(patterns []string, callback func(filename string)) {
	for _, basePattern := range patterns {
		pat := basePattern
		if pat[:2] == "~/" {
			usr, _ := user.Current()
			dir := usr.HomeDir
			pat = filepath.Join(dir, pat[2:])
		}
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(".", pat)
		}
		matches, err := filepath.Glob(pat)
		if err != nil {
			log.Printf("error expanding file pattern %s: %v\n", pat, err)
			continue
		}
		for _, fname := range matches {
			callback(fname)
		}
	}
}

//
// generatePackets
//

// MapOverPacketFiles generates a stream of packets and sends them using a callback
func MapOverPacketFiles(args []string, callback func(p *ccsds.Packet)) {
	MapOverFiles(args, func (fname string) {
		pktfile := ccsds.PacketFile{Filename: fname}
		pktfile.Iterate(func(p *ccsds.Packet) {
			len := p.Length() + 7
			buf := make([]byte, len)
			copy(buf, *p)
			callback(p)
		})
	})
}

// StreamPacketFiles generates a stream of packets and sends them to a channel
func StreamPacketFiles(args []string, channel chan *ccsds.Packet) {
	MapOverPacketFiles(args, func(p *ccsds.Packet) {
		channel <- p
	})
	close(channel)
}

// MapOverPacketFilesBPS generates a stream of packets and sends them via a callback, slowing the calls
// to a given bits-per-second
func MapOverPacketFilesBPS(bps int, args []string, callback func(p *ccsds.Packet)) {
	var totalBits int64
	startTime := time.Now()
	targetTime := startTime
	MapOverFiles(args, func (fname string) {
		pktfile := ccsds.PacketFile{Filename: fname}
		pktfile.Iterate(func(p *ccsds.Packet) {
			len := p.Length() + 7
			buf := make([]byte, len)
			copy(buf, *p)

			// Limit the playback rate
			sleepDelta := targetTime.Sub(time.Now())
			time.Sleep(sleepDelta)
			totalBits += 8 * int64(p.Length() + 7)
			targetSecondsDelay := float64(totalBits) / float64(bps)
			targetNanoSecondsDelay := int64(targetSecondsDelay * float64(time.Second))
			targetTime = startTime.Add(time.Duration(targetNanoSecondsDelay))

			callback(p)
		})
	})
}

// StreamPacketFilesBPS generates a stream of packets and sends them to a stream, slowing the calls
// to a given bits-per-second
func StreamPacketFilesBPS(bps int, args []string, channel chan *ccsds.Packet) {
	MapOverPacketFilesBPS(bps, args, func(p *ccsds.Packet) {
		channel <- p
	})
	close(channel)
}

// Sigh

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

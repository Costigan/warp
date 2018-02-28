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

//
// generatePackets
//

// PacketFileCallback generates a stream of packets and sends them using a callback
func PacketFileCallback(args []string, callback func(p *ccsds.Packet)) {
	for _, basePattern := range args {
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
			pktfile := ccsds.PacketFile{Filename: fname}
			pktfile.Iterate(func(p *ccsds.Packet) {
				len := p.Length() + 7
				buf := make([]byte, len)
				copy(buf, *p)
				callback(p)
			})
		}
	}
}

// PacketFileChannel generates a stream of packets and sends them to a channel
func PacketFileChannel(args []string, channel chan *ccsds.Packet) {
	PacketFileCallback(args, func(p *ccsds.Packet) {
		channel <- p
	})
	close(channel)
}

// PacketFileCallbackBPS generates a stream of packets and sends them via a callback, slowing the calls
// to a given bits-per-second
func PacketFileCallbackBPS(bps int, args []string, callback func(p *ccsds.Packet)) {
	var totalBits int64
	startTime := time.Now()
	targetTime := startTime
	for _, basePattern := range args {
		pat := basePattern
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(".", pat)
		}
		matches, err := filepath.Glob(pat)
		if err != nil {
			log.Printf("error expanding file pattern %s: %v\n", pat, err)
			continue
		}

		for _, fname := range matches {
			pktfile := ccsds.PacketFile{Filename: fname}
			pktfile.Iterate(func(p *ccsds.Packet) {
				len := p.Length() + 7
				buf := make([]byte, len)
				copy(buf, *p)

				// Insert the governer
				time.Sleep(targetTime.Sub(time.Now()))
				totalBits += int64(p.Length() + 7)
				targetTime = startTime.Add(time.Duration(float64(totalBits) / float64(bps)))

				callback(p)
			})
		}
	}
}

// PacketFileChannelBPS generates a stream of packets and sends them to a stream, slowing the calls
// to a given bits-per-second
func PacketFileChannelBPS(bps int, args []string, channel chan *ccsds.Packet) {
	PacketFileCallbackBPS(bps, args, func(p *ccsds.Packet) {
		channel <- p
	})
	close(channel)
}

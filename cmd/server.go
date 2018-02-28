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
	"path/filepath"
	"time"

	"github.com/Costigan/warp/ccsds"
	"github.com/Costigan/warp/server"
	"github.com/spf13/cobra"
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Serve data from a single session telemetry session",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		channel := make(chan *ccsds.Packet, 300)

		var serv server.Server

		// Start the server first
		go func() {
			serv = server.Server{
				Host:        "",
				Port:        8000,
				PacketChan: channel,
				StaticFiles: "../../../../../projects/warp_data/dist/"}
			serv.Run()
			stopRequest = true
		}()

		// Wait for it to start
		time.Sleep(2 * time.Second)

		// Start sending packets
		generatePackets(channel, args)
	},
}

var stopRequest bool = false

var sessionDir string
var dictionaryPath string
var inStreamSpec string
var inFormat string

var inFiles string
var bitsPerSecond int

func init() {
	rootCmd.AddCommand(serverCmd)

	serverCmd.Flags().StringVar(&sessionDir, "session", "", "A session name or a path to a directory containing the session")
	serverCmd.Flags().StringVar(&dictionaryPath, "dictionary", "", "If provided, overrides the dictionary contained in the session")
	serverCmd.Flags().StringVar(&inStreamSpec, "in", "", "Where telemetry will come from.  Defaults to files on the command line")
	serverCmd.Flags().StringVar(&inFormat, "format-in", "packet", "The format of incomming telemetry.  Defaults to raw CCSDS packets")

	serverCmd.Flags().IntVar(&bitsPerSecond, "bps", 0, "Limit playback to bits per second")
}

//
// Generate packets
//

func generatePackets(channel chan *ccsds.Packet, args []string) {
	packetFileCallbackBPSStopRequest(&stopRequest, bitsPerSecond, args, func(p *ccsds.Packet) {
		channel<-p
	})
}

func packetFileCallbackBPSStopRequest(stop *bool, bps int, args []string, callback func(p *ccsds.Packet)) {
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

				if *stop {
					return
				}

				len := p.Length() + 7
				buf := make([]byte, len)
				copy(buf, *p)

				// Insert the governer
				sleepDelta := targetTime.Sub(time.Now())
				//fmt.Printf("targetTime=%v sleepDelta=%d\n", targetTime, int64(sleepDelta))
				time.Sleep(sleepDelta)
				totalBits += 8 * int64(p.Length() + 7)
				targetSecondsDelay := float64(totalBits) / float64(bps)
				targetNanoSecondsDelay := int64(targetSecondsDelay * float64(time.Second))
				targetTime = startTime.Add(time.Duration(targetNanoSecondsDelay))

				if 816 == p.APID() {
					log.Printf("Sending EPSIO_BIT\n")
				}

				callback(p)
			})
		}
	}
}

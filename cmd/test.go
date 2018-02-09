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
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/Costigan/warp/ccsds"
	"github.com/spf13/cobra"
)

// testCmd represents the test command
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Used to exercise program features",
	Long:  `What this command does changes over time as new functionality is implemented and tested.`,
	Run: func(cmd *cobra.Command, args []string) {
		test5(args)
	},
}

func init() {
	rootCmd.AddCommand(testCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// testCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// testCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func test1(args []string) {
	var filename string
	if len(args) < 1 {
		filename = "C:/RP/data/test_data/pktfile.1"
	} else {
		filename = args[0]
	}
	fmt.Println("filename=", filename)
	pktfile := ccsds.PacketFile{Filename: filename}
	pktfile.Iterate(func(p ccsds.Packet) {
		fmt.Printf("apid=%d len=%d\n", p.APID(), p.Length())
	})
}

func test2(args []string) {
	filename := "C:/git/github/warp-server-legacy/src/StaticFiles/rp.dictionary.json.gz"
	dictionary, err := ccsds.LoadDictionary(filename)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("There are %d packets in %s", len((*dictionary).Packets), filename)
}

func test3(args []string) {
	var v1 float32 = 1.0
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, v1)
	if err != nil {
		fmt.Println("binary.Write failed:", err)
	}

	packetbuf := buf.Bytes()
	packet := ccsds.Packet(packetbuf)

	point := ccsds.PointInfo{FieldType: ccsds.F1234, BitStart: 0, BitStop: 32, ByteOffset: 0, ByteSize: 4}
	v2, err2 := point.GetValue(&packet)
	if err2 != nil {
		fmt.Println("error when extracting value")
		return
	}

	if v1 != v2 {
		fmt.Printf("values didn't match:%f:%f", v1, v2)
		return
	}
}

func test4(args []string) {
	cases := []string{"a", "ab", "abc", "abcd"}
	for offset := 0; offset < 12; offset++ {
		for _, v1 := range cases {
			buf := generateCCSDSHeader(1, 2, len(v1))
			for j := 0; j < offset; j++ {
				binary.Write(buf, binary.BigEndian, byte(0)) // ignoring error
			}
			for _, c := range v1 {
				err := binary.Write(buf, binary.BigEndian, byte(c))
				if err != nil {
					fmt.Printf("binary.Write failed:%s", err)
					return
				}
			}

			packetbuf := buf.Bytes()
			packet := ccsds.Packet(packetbuf)

			point := ccsds.PointInfo{FieldType: ccsds.S1, BitStart: 0, BitStop: 15, ByteOffset: uint(offset + 6), ByteSize: uint(len(v1))}
			v2, err2 := point.GetValue(&packet)
			if err2 != nil {
				fmt.Printf("error extracting point value")
				return
			}

			if v1 != v2 {
				fmt.Printf("values didn't match:%s:%s", v1, v2)
				return
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

func test5(args []string) {
	dictionaryFilename := "C:/git/github/warp-server-legacy/src/StaticFiles/rp.dictionary.json.gz"
	dictionary, err := ccsds.LoadDictionary(dictionaryFilename)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("There are %d packets in %s", len((*dictionary).Packets), dictionaryFilename)

	packetFilename := "C:/RP/data/test_data/pktfile.1"
	fmt.Printf("Reading packet filename %s\r\n", packetFilename)
	pktfile := ccsds.PacketFile{Filename: packetFilename}
	pktfile.Iterate(func(p ccsds.Packet) {
		apid := p.APID()
		fmt.Printf("apid=%d len=%d\r\n", apid, p.Length())
		packetInfo := (*dictionary).PacketLookup[apid]
		if packetInfo == nil {
			return
		}
		for _, pt := range packetInfo.Points {
			v, err := pt.GetValue(&p)
			if err != nil {
				fmt.Printf("    Error extracting %s\n", pt.ID)
				continue
			}
			fmt.Printf("    %s = %v\r\n", pt.ID, v)
		}
	})
}

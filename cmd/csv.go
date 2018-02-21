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
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/Costigan/warp/ccsds"
	"github.com/spf13/cobra"
)

// csvCmd represents the csv command
var csvCmd = &cobra.Command{
	Use:   "csv",
	Short: "Generate CSV files from CCSDS packet or frame files",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return errors.New("requires at least one arg")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		generateCsvFiles(cmd, args)
	},
}

var dictionary_filename string
var csv_path string
var packets_bool bool
var frames_bool bool
var recursive bool
var filepat string

func init() {
	rootCmd.AddCommand(csvCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// csvCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// csvCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

	csvCmd.Flags().StringVarP(&dictionary_filename, "dictionary", "d", "./dictionary.json.gz", "Path of dictionary file")
	csvCmd.MarkFlagRequired("dictionary")
	csvCmd.Flags().StringVarP(&csv_path, "outdir", "", "./csv", "Path of dictionary file")
	csvCmd.MarkFlagRequired("outdir")
	csvCmd.Flags().BoolVarP(&packets_bool, "packets", "p", true, "use if the files contain ccsds packets")
	csvCmd.Flags().BoolVarP(&frames_bool, "frames", "f", false, "use if the files contain ccsds frames")
	csvCmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "search inside directories for packet or frame files")
	csvCmd.Flags().StringVar(&filepat, "pattern", "", "search for files matching a regular expression")
}

func generateCsvFiles(cmd *cobra.Command, args []string) {
	if Verbose {
		fmt.Printf("dictionary=%v\n", dictionary_filename)
		fmt.Printf("outdir    =%v\n", csv_path)
		fmt.Printf("recursive =%v\n", recursive)
		fmt.Printf("pattern   =%v\n", filepat)
		fmt.Printf("packets   =%v\n", packets_bool)
		fmt.Printf("frames    =%v\n", frames_bool)
		for i := 0; i < len(args); i++ {
			fmt.Printf(" arg[%d]=%s\n", i, args[i])
		}
	}

	// Check that the outdir exists or create it
	err := os.MkdirAll(csv_path, os.ModeDir | 0770)
	if err != nil {
		fmt.Printf("An error occurred while creating the output directory(%s): %v\n", csv_path, err);
		fmt.Println("Aborting...")
		return
	}

	// Load the dictionary
	dictionary, err := ccsds.LoadDictionary(dictionary_filename)
	if err != nil {
		fmt.Printf("An error occurred reading the dictionary %s: %v\n", dictionary_filename, err)
		return
	}

	writerMap := WriterMap{theMap: map[int]*csvWriter{}, maxOpen: 20}
	apidErrors := make(map[int]bool)

	channel := make(chan ccsds.Packet, 20)
	go generatePackets(channel, args)

	// This part of the program receives packets from generatePackets()
	var packet_count int = 0
	for pkt := range channel {
		packet_count++
		apid := pkt.APID()

		packetInfo := (*dictionary).PacketLookup[apid]
		if packetInfo == nil {
			_, ok := apidErrors[apid]
			if ok {
				continue
			}
			fmt.Printf("APID %d was seen but no matching packet found in the dictionary\n", apid)
			apidErrors[apid] = true
			continue
		}

		writer, ok := writerMap.get(apid)
		if !ok {
			filename := filepath.Join(csv_path, packetInfo.Name+".csv")
			writer = &csvWriter{theMap: &writerMap, apid: apid, filename: filename, buffer: bytes.NewBuffer(make([]byte, 0, 2048))}
			writerMap.put(apid, writer)

			// Create or truncate the file
			if f, err := os.Create(writer.filename); err == nil {
				f.Close()
			} else {
				// TODO: This isn't right yet. It doesn't stop the sender, just the receiver.  The sender will hang when the channel's
				// buffer fills up
				fmt.Printf("An error occurred creating %s: %v\n", writer.filename, err)
				fmt.Println("Aborting ...")
				writerMap.closeAll()
				return
			}

			buf := writer.buffer
			// Write the first line
			for i, pt := range packetInfo.Points {
				if i > 0 {
					fmt.Fprint(buf, ",")
				}
				fmt.Fprint(buf, pt.Name)
			}
			fmt.Fprintf(buf, "\n")
			writer.flush()
		}

		buf := writer.buffer
		for i, pt := range packetInfo.Points {
			v, err := pt.GetValue(&pkt)
			if err != nil {
				fmt.Printf("    Error extracting %s\n", pt.ID)
				continue
			}
			if i > 0 {
				fmt.Fprint(buf, ",")
			}
			fmt.Fprintf(buf, "%v", v)
		}
		fmt.Fprintf(buf, "\n")
		writer.flushMaybe()
	}

	writerMap.closeAll()

	fmt.Printf("%d packets were processed.\n", packet_count)
}

//
// Writer Map
//

type WriterMap struct {
	theMap  map[int]*csvWriter
	maxOpen int
}

func (m *WriterMap) get(apid int) (*csvWriter, bool) {
	v, ok := m.theMap[apid]
	return v, ok
}

func (m *WriterMap) put(apid int, w *csvWriter) {
	m.theMap[apid] = w
}

func (m *WriterMap) getLeastRecentlyUsed() *csvWriter {
	openCount := 0
	for _, writer := range m.theMap {
		if writer.file != nil {
			openCount++
		}
	}
	if openCount < m.maxOpen {
		return nil
	}

	// Gotta close one
	age := math.MaxInt64
	var oldest *csvWriter
	for _, writer := range m.theMap {
		if writer.file != nil && writer.age < age {
			age = writer.age
			oldest = writer
		}
	}
	return oldest
}

func (m *WriterMap) closeAll() {
	for _, writer := range m.theMap {
		writer.close()
	}
}

//
// csvWriter
//

type csvWriter struct {
	apid      int
	file      *os.File
	filename  string
	buffer    *bytes.Buffer
	age       int
	threshold int
	theMap    *WriterMap
}

func (writer *csvWriter) open() error {
	file, err := os.OpenFile(writer.filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, os.ModeAppend)
	writer.file = file
	return err
}

func (writer *csvWriter) flushMaybe() {
	if writer.buffer.Len() > writer.threshold {
		writer.flush()
	}
}

func (writer *csvWriter) flush() {
	if len(writer.buffer.Bytes()) < 1 {
		return
	}
	if writer.file == nil {
		writerToClose := writer.theMap.getLeastRecentlyUsed()
		if writerToClose != nil && writerToClose != writer {
			writerToClose.close()
		}
		file, err := os.OpenFile(writer.filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, os.ModeAppend)
		if err != nil {
			fmt.Printf("error while opening %s: %v\n", writer.filename, err)
			return
		}
		writer.file = file
	}
	_, err2 := writer.buffer.WriteTo(writer.file)
	if err2 != nil {
		fmt.Printf("error while writing to %s: %v\n", writer.filename, err2)
		return
	}
	writer.buffer.Reset() // if err2 is nil, then n must have been the buffer's length
}

func (writer *csvWriter) close() {
	writer.flush()
	if writer.file != nil {
		err3 := writer.file.Close()
		if err3 != nil {
			fmt.Printf("error while closing %s: %v\n", writer.filename, err3)
		}
		writer.file = nil
	}
}

//
// generatePackets
//

func generatePackets(c chan ccsds.Packet, args []string) {
	for _, base_pattern := range args {
		pat := base_pattern
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(".", pat)
		}
		matches, err := filepath.Glob(pat)
		if err != nil {
			fmt.Printf("error expanding file pattern %s: %v\n", pat, err)
			continue
		}

		for _, fname := range matches {
			pktfile := ccsds.PacketFile{Filename: fname}
			pktfile.Iterate(func(p ccsds.Packet) {
				len := p.Length() + 7
				buf := make([]byte, len)
				copy(buf, p)
				c <- buf
			})
		}
	}
	close(c)
}

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
		test2(args)
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

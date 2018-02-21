// Copyright © 2018 NAME HERE <EMAIL ADDRESS>
//
// Licensed under the Apache License, 2.0 (the "License");
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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Costigan/warp/ccsds"
	"github.com/Costigan/warp/server"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

// testCmd represents the test command
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Used to exercise program features",
	Long:  `What this command does changes over time as new functionality is implemented and tested.`,
	Run: func(cmd *cobra.Command, args []string) {
		test6(cmd, args)
	},
}

var test_ws_decom bool
var test_ws_history bool
var test_rest_history bool
var test_rest_dictionary bool

var server_host string = "localhost"
var server_port int = 8000

var session_name = "demo"

func init() {
	rootCmd.AddCommand(testCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// testCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// testCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

	// Consider adding a help flag here to override the default.  The default takes the -h option, which I'd like to use for host

	testCmd.Flags().BoolVar(&test_ws_decom, "ws-decom", false, "Test websocket subscription and decom service")
	testCmd.Flags().BoolVar(&test_ws_history, "ws-history", false, "Test websocket history service")
	testCmd.Flags().BoolVar(&test_rest_history, "rest-history", false, "Test REST history service")
	testCmd.Flags().BoolVar(&test_rest_dictionary, "rest-dictionary", false, "Test REST dictionary service")

	testCmd.Flags().StringVar(&server_host, "host", "localhost", "Hostname where a warp server will be running")
	testCmd.Flags().IntVar(&server_port, "port", 8000, "Port where a warp server will be running")
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
	filename := "/home/mshirley/rp.dictionary.json.gz"
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
	dictionaryFilename := "/home/mshirley/rp.dictionary.json.gz"
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

var netClient = &http.Client{Timeout: time.Second * 10}

func test6(cmd *cobra.Command, args []string) {
	fmt.Printf("test_ws_decom=%v\r\n", test_ws_decom)
	fmt.Printf("test_ws_history=%v\r\n", test_ws_history)
	fmt.Printf("test_rest_history=%v\r\n", test_rest_history)
	fmt.Printf("test_rest_dictionary=%v\r\n", test_rest_dictionary)
	fmt.Printf("server_port=%v\r\n", server_port)

	dictionary_prefix = "http://" + server_host + ":" + fmt.Sprintf("%d", server_port) + "/dictionary/" + session_name

	websocket_prefix = "ws://" + server_host + ":" + fmt.Sprintf("%d", server_port) + "/realtime/"

	if test_rest_dictionary {
		testRestDictionary()
	}
	if test_ws_decom {
		testWebsocketDecom(1)
	}
}

//
// testing the dictionary
//

var dictionary_prefix string
var websocket_prefix string

func testRestDictionary() {
	root_bytes, ok := FetchURL(dictionary_prefix, "/root")
	if !ok {
		fmt.Printf("error fetching the dictionary root.")
		return
	}

	var dict server.DictionaryRootResponse
	if err := json.Unmarshal(root_bytes, &dict); err != nil {
		fmt.Printf("error unmarshalling response to dictionary root: %v", err)
		return
	}

	//fmt.Println("Received valid json from dictionary root")
	//if bytes, err2 := json.MarshalIndent(dict, "", "    "); err2 == nil {
	//	fmt.Println(string(bytes))
	//} else {
	//	fmt.Printf("error unmarshalling response to dictionary root: %v", err2)
	//}

	// fetch each packet by id
	var response_map = map[string][]byte{}
	lock := sync.RWMutex{}
	var wg sync.WaitGroup
	wg.Add(len(dict.Packets))
	for _, pkt := range dict.Packets {
		pkt := pkt
		go func() {
			defer wg.Done()
			pkt_bytes, ok2 := FetchURL(dictionary_prefix, "/id/"+pkt.ID)
			if !ok2 {
				fmt.Printf("error fetching dictionary packet: %v", pkt.ID)
				return
			}
			lock.Lock()
			defer lock.Unlock()
			response_map[pkt.ID] = pkt_bytes
		}()
	}
	wg.Wait()

	// decomm all responses
	var pkt_struct server.PacketDictionaryResponse
	for pkt_id, pkt_bytes := range response_map {
		if err := json.Unmarshal(pkt_bytes, &pkt_struct); err != nil {
			fmt.Printf("error unmarshalling dictionary %s packet response: %v", pkt_id, err)
		} else {
			fmt.Printf("correct unmarshalling of %s\r\n", pkt_id)
		}
	}
}

func FetchURL(prefix string, path string) ([]byte, bool) {
	r, err := netClient.Get(prefix + path)
	if err != nil {
		fmt.Printf("error fetching %s: %v", path, err)
		return nil, false
	}
	defer r.Body.Close()
	body, err2 := ioutil.ReadAll(r.Body)
	if err2 != nil {
		fmt.Printf("error fetching %s: %v\r\n", path, err2)
		return nil, false
	}
	return body, true
}

//
// Testing the websocket interface to decom
//

const (
	writeWait      = 10 * time.Second    // Time allowed to write a message to the peer.
	pongWait       = 60 * time.Second    // Time allowed to read the next pong message from the peer.
	pingPeriod     = (pongWait * 9) / 10 // Send pings to peer with this period. Must be less than pongWait.
	maxMessageSize = 512                 // Maximum message size allowed from peer.
)

type websocketTester struct {
	target url.URL
	conn   *websocket.Conn
}

func testWebsocketDecom(count int) {
	fmt.Printf("in testWebsocketDecom\r\n")
	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("%s:%d", "localhost", server_port), Path: "/realtime"}
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		tester := websocketTester{target: u}
		fmt.Printf("created tester %d\r\n", i)
		go func() { tester.run(&wg) }()
	}
	wg.Wait()
	fmt.Printf("Exiting testWebsocketDecom\r\n")
}

func (tester *websocketTester) run(wg *sync.WaitGroup) {
	fmt.Printf("connecting to %s\r\n", tester.target.String())

	c, _, err := websocket.DefaultDialer.Dial(tester.target.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	tester.conn = c

	fmt.Printf("Connected.  Sending pings ...\r\n")

	for i := 0; i < 10; i++ {
		fmt.Printf("Sending ping %d...\r\n", i)
		if !tester.sendJSON(server.PingRequest{Request: "ping", Token: "t1"}) {
			fmt.Printf("Error sending ping\r\n")
			return
		}
		_, bytes, err := tester.conn.ReadMessage()
		if err != nil {
			fmt.Printf("Error receiving ping reply: %v", err)
			return
		}
		fmt.Printf("  received ping reply %d: %s\r\n", i, string(bytes))
	}
}

func (tester *websocketTester) sendJSON(msg interface{}) bool {
	if bytes, err := json.Marshal(msg); err == nil {
		return tester.send(bytes)
	} else {
		log.Printf("Error preparing json for a message: %s", msg)
		return false
	}
}

func (tester *websocketTester) send(msg []byte) bool {
	if err := tester.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		fmt.Printf("Error writing websocket message: %v", err)
		if err1 := tester.conn.Close(); err1 != nil {
			fmt.Printf("Error closing websocket connection: %v", err1)
		}
		return false
	}
	return true
}

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

var doTestWebsocketDecom bool
var doTestWebsocketHistory bool
var doTestRestHistory bool
var doTestRestDictionary bool

var serverHost = "localhost"
var serverPort = 8000

var sessionName = "demo"

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

	testCmd.Flags().BoolVar(&doTestWebsocketDecom, "ws-decom", false, "Test websocket subscription and decom service")
	testCmd.Flags().BoolVar(&doTestWebsocketHistory, "ws-history", false, "Test websocket history service")
	testCmd.Flags().BoolVar(&doTestRestHistory, "rest-history", false, "Test REST history service")
	testCmd.Flags().BoolVar(&doTestRestDictionary, "rest-dictionary", false, "Test REST dictionary service")

	testCmd.Flags().StringVar(&serverHost, "host", "localhost", "Hostname where a warp server will be running")
	testCmd.Flags().IntVar(&serverPort, "port", 8000, "Port where a warp server will be running")
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
		packetInfo := (*dictionary).PacketAPIDLookup[apid]
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
	fmt.Printf("doTestWebsocketDecom=%v\r\n", doTestWebsocketDecom)
	fmt.Printf("doTestWebsocketHistory=%v\r\n", doTestWebsocketHistory)
	fmt.Printf("doTestRestHistory=%v\r\n", doTestRestHistory)
	fmt.Printf("doTestRestDictionary=%v\r\n", doTestRestDictionary)
	fmt.Printf("serverPort=%v\r\n", serverPort)

	dictionaryPrefix = "http://" + serverHost + ":" + fmt.Sprintf("%d", serverPort) + "/dictionary/" + sessionName

	websocketPrefix = "ws://" + serverHost + ":" + fmt.Sprintf("%d", serverPort) + "/realtime/"

	if doTestRestDictionary {
		testRestDictionary()
	}
	if doTestWebsocketDecom {
		testWebsocketDecom(1)
	}
}

//
// testing the dictionary
//

var dictionaryPrefix string
var websocketPrefix string

func testRestDictionary() {
	rootBytes, ok := fetchURL(dictionaryPrefix, "/root")
	if !ok {
		fmt.Printf("error fetching the dictionary root.")
		return
	}

	var dict server.DictionaryRootResponse
	if err := json.Unmarshal(rootBytes, &dict); err != nil {
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
	var responseMap = map[string][]byte{}
	lock := sync.RWMutex{}
	var wg sync.WaitGroup
	wg.Add(len(dict.Packets))
	for _, pkt := range dict.Packets {
		pkt := pkt
		go func() {
			defer wg.Done()
			pktBytes, ok2 := fetchURL(dictionaryPrefix, "/id/"+pkt.ID)
			if !ok2 {
				fmt.Printf("error fetching dictionary packet: %v", pkt.ID)
				return
			}
			lock.Lock()
			defer lock.Unlock()
			responseMap[pkt.ID] = pktBytes
		}()
	}
	wg.Wait()

	// decomm all responses
	var pktStruct server.PacketDictionaryResponse
	for pktID, pktBytes := range responseMap {
		if err := json.Unmarshal(pktBytes, &pktStruct); err != nil {
			fmt.Printf("error unmarshalling dictionary %s packet response: %v", pktID, err)
		} else {
			fmt.Printf("correct unmarshalling of %s\r\n", pktID)
		}
	}
}

func fetchURL(prefix string, path string) ([]byte, bool) {
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
	fmt.Printf("in doTestWebsocketDecom\r\n")
	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("%s:%d", "localhost", serverPort), Path: "/realtime"}
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		tester := websocketTester{target: u}
		fmt.Printf("created tester %d\r\n", i)
		go func() { tester.run(&wg) }()
	}
	wg.Wait()
	fmt.Printf("Exiting doTestWebsocketDecom\r\n")
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
	}
	log.Printf("Error preparing json for a message: %s", msg)
	return false
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

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

var doTestWebsocketPings bool
var doTestWebsocketSubscriptions bool
var doTestWebsocketDecom bool
var doTestWebsocketHistory bool
var doTestRestHistory bool
var doTestRestDictionary bool

var workerCount int
var workerCycles int

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

	testCmd.Flags().BoolVar(&doTestWebsocketPings, "ws-ping", false, "Test websocket pings")
	testCmd.Flags().BoolVar(&doTestWebsocketSubscriptions, "ws-sub", false, "Test websocket subscriptions (add & remove subscriptions)")
	testCmd.Flags().BoolVar(&doTestWebsocketDecom, "ws-decom", false, "Test websocket subscriptions and decom")
	testCmd.Flags().BoolVar(&doTestWebsocketHistory, "ws-history", false, "Test websocket history")
	testCmd.Flags().BoolVar(&doTestRestHistory, "rest-history", false, "Test REST history endpoints")
	testCmd.Flags().BoolVar(&doTestRestDictionary, "rest-dictionary", false, "Test REST dictionary endpoints")

	testCmd.Flags().IntVar(&workerCount, "workers", 1, "Number of workers testing in parallel")
	testCmd.Flags().IntVar(&workerCycles, "cycles", 1, "Number of cycles of testing each worker should do (details depend on the test)")

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
	fmt.Printf("Reading packet filename %s\n", packetFilename)
	pktfile := ccsds.PacketFile{Filename: packetFilename}
	pktfile.Iterate(func(p ccsds.Packet) {
		apid := p.APID()
		fmt.Printf("apid=%d len=%d\n", apid, p.Length())
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
			fmt.Printf("    %s = %v\n", pt.ID, v)
		}
	})
}

var netClient = &http.Client{Timeout: time.Second * 10}

func test6(cmd *cobra.Command, args []string) {
	fmt.Printf("doTestWebsocketSubscriptions=%v\n", doTestWebsocketSubscriptions)
	fmt.Printf("doTestWebsocketDecom=%v\n", doTestWebsocketDecom)
	fmt.Printf("doTestWebsocketHistory=%v\n", doTestWebsocketHistory)
	fmt.Printf("doTestRestHistory=%v\n", doTestRestHistory)
	fmt.Printf("doTestRestDictionary=%v\n", doTestRestDictionary)
	fmt.Printf("serverPort=%v\n", serverPort)

	dictionaryPrefix = "http://" + serverHost + ":" + fmt.Sprintf("%d", serverPort) + "/dictionary/" + sessionName

	websocketPrefix = "ws://" + serverHost + ":" + fmt.Sprintf("%d", serverPort) + "/realtime/"

	if doTestRestDictionary {
		testRestDictionary()
	}
	if doTestWebsocketDecom || doTestWebsocketSubscriptions {
		testWebsockets()
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
			fmt.Printf("correct unmarshalling of %s\n", pktID)
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
		fmt.Printf("error fetching %s: %v\n", path, err2)
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

type websocketWorker struct {
	id     int
	target url.URL
	conn   *websocket.Conn
}

func testWebsockets() {
	fmt.Printf("in doTestWebsocketDecom\n")
	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("%s:%d", "localhost", serverPort), Path: "/realtime/"}
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		tester := websocketWorker{id: i, target: u}
		fmt.Printf("created tester %d\n", i)
		go func() { tester.run(&wg) }()
	}
	wg.Wait()
	fmt.Printf("Exiting doTestWebsockets\n")
}

func (worker *websocketWorker) run(wg *sync.WaitGroup) {
	fmt.Printf("connecting to %s\n", worker.target.String())

	c, resp, err := websocket.DefaultDialer.Dial(worker.target.String(), nil)
	if err == websocket.ErrBadHandshake {
		log.Printf("handshake failed with status %d", resp.StatusCode)
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		log.Printf("body: %v", buf.String())
		log.Fatal("Exiting.")
	}
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer func() {
		c.Close()
		defer wg.Done()
	}()

	worker.conn = c

	if doTestWebsocketPings {
		fmt.Printf("Connected.  Sending pings ...\n")

		for i := 0; i < workerCycles; i++ {
			fmt.Printf("worker %d: Sending ping %d...\n", worker.id, i)
			if !worker.sendJSON(server.PingRequest{Request: "ping", Token: "t1"}) {
				fmt.Printf("Error sending ping\n")
				return
			}
			_, bytes, err := worker.conn.ReadMessage()
			if err != nil {
				fmt.Printf("Worker %d: Error receiving ping reply: %v", worker.id, err)
				return
			}
			fmt.Printf("Worker %d: Received ping reply %d: %s\n", worker.id, i, string(bytes))
		}
	}
}

func (worker *websocketWorker) sendJSON(msg interface{}) bool {
	if bytes, err := json.Marshal(msg); err == nil {
		return worker.send(bytes)
	}
	log.Printf("Error preparing json for a message: %s", msg)
	return false
}

func (worker *websocketWorker) send(msg []byte) bool {
	if err := worker.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		fmt.Printf("Error writing websocket message: %v", err)
		if err1 := worker.conn.Close(); err1 != nil {
			fmt.Printf("Error closing websocket connection: %v", err1)
		}
		return false
	}
	return true
}

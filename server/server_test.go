package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Costigan/warp/ccsds"
	"github.com/gorilla/websocket"
)

//
// Constants
//

const serverPort int = 8000
const serverWebsocketURL string = "ws://localhost:8000/realtime/"
const serverDictionaryIDPrefix string = "http://localhost:8000/dictionary/demo/id/"
const serverDictionaryRoot string = "http://localhost:8000/dictionary/demo/root"

const bypassWithRunningServer bool = false

//
// Global State
//

var dictionary *ccsds.TelemetryDictionary

//
// TestBitArray
//

func TestBitArray(t *testing.T) {
	b := NewBitArray(100)

	for i := 0; i < 100; i++ {
		b.SetBit(i)

		if !b.GetBit(i) || b.GetBit(i+1) {
			t.Errorf("Unexpected value while filling bit array at iteration %d", i)
		}
		if i+1 != b.BitCount() {
			t.Errorf("At iteration %d the BitCount was %d", i, b.BitCount())
		}
	}

	for i := 99; i >= 0; i-- {
		if !b.GetBit(i) {
			t.Errorf("expected bit %d set, but it wasn't", i)
		}
		b.ClearBit(i)
		if (i > 0 && !b.GetBit(i-1)) || b.GetBit(i) || b.GetBit(i+1) {
			t.Errorf("expected value while emptying bit array at iteration %d.  i-1=%v i=%v i+1=%v", i, b.GetBit(i-1), b.GetBit(i), b.GetBit(i+1))
		}
		if i != b.BitCount() {
			t.Errorf("At iteration %d the BitCount was %d", i, b.BitCount())
		}
	}
}

//
// TestNoop (starts and stops a server instance)
//

func TestNoop(t *testing.T) {
	withRunningServer(t, serverPort, func(server *Server) {})
}

//
// TestSingleServer
// Starts a server then runs a sequence of tests
//

func TestSingleServer(t *testing.T) {
	withRunningServer(t, serverPort, func(server *Server) {
		testPing(t, server)
		testDictionaryResponse(t, server)
		testSingleSubscriber(t, server)
		testMultipleSubscribers(t, server)
	})
}

func testPing(t *testing.T, server *Server) {
	t.Log("Opening connection to server now")
	u, _ := url.Parse(serverWebsocketURL)
	c, ok := getWebsocketConnection(t, *u)
	if !ok {
		return
	}
	if !localSendJSON(t, c, GenericRequest{Request: "ping", Token: "t1"}) {
		t.Errorf("Error sending ping\n")
		return
	}
	_, bytes, err := c.ReadMessage()
	if err != nil {
		t.Errorf("Error receiving ping reply: %v", err)
		return
	}
	var msg GenericResponse
	err = json.Unmarshal(bytes, &msg)
	if err != nil {
		t.Errorf("Error unmarshalling ping reply: %v", err)
	}
}

func testDictionaryResponse(t *testing.T, server *Server) {
	loadDictionaryMaybe(t)

	// Get the dictionary
	var dictResponse DictionaryRootResponse
	ok := getRESTResponse(t, serverDictionaryRoot, &dictResponse)
	if !ok {
		return
	}

	packets := dictResponse.Packets
	if len(packets) != 139 {
		t.Errorf("Expected 139 packets.  Got %d", len(packets))
	}
}

func testSingleSubscriber(t *testing.T, server *Server) {
	loadDictionaryMaybe(t)

	u, _ := url.Parse(serverWebsocketURL)
	c, ok := getWebsocketConnection(t, *u)
	if !ok {
		return
	}
	sub := &Subscriber{conn: c, server: server, t: t}
	sub.TestSubscriptionsSubscribeShuffled(0)
}

func testMultipleSubscribers(t *testing.T, server *Server) {
	loadDictionaryMaybe(t)

	const subscriberCount int = 3 // can be run with a larger number

	wg := sync.WaitGroup{}
	wg.Add(subscriberCount)

	for i := 0; i < subscriberCount; i++ {
		u, _ := url.Parse(serverWebsocketURL)
		c, ok := getWebsocketConnection(t, *u)
		if !ok {
			t.Fatalf("Failed to establish websocket connection for subscriber # %d", i)
		}
		sub := &Subscriber{conn: c, server: server, t: t}
		go func() {
			sub.TestSubscriptionsSubscribeShuffled(int64(i))
			wg.Done()
		}()
	}
	wg.Wait()
}

//
// TestDecom1
//

func TestDecom1(t *testing.T) {
	loadDictionaryMaybe(t)
	dict := dictionary

	packetFilename := filepath.Join("../testdata", "pktfile.1")
	pktfile := ccsds.PacketFile{Filename: packetFilename}
	count := 0
	pktfile.Iterate(func(p *ccsds.Packet) {
		apid := p.APID()
		packets, ok := dict.GetPacketsByAPID(apid)
		if !ok {
			return
		}
		count++
		if count > 2 {
			return
		}
		for _, packet := range packets {
			msg := DecomPacket(p, packet.Points)

			for i, ch := range msg {
				if ch == 0 {
					fmt.Printf("zero at index %d\n", i)
				}
			}

			var response DataEventTemplate
			if err := json.Unmarshal(msg, &response); err == nil {
				for _, pt := range packet.Points {
					if v1, err := pt.GetValue(p); err == nil {
						if m2, ok := response.Values[pt.ID]; ok {
							if v1 != m2.Value {
								if !equalCarefully(v1, m2.Value) {
									t.Errorf("Decom values differ v1=%v v2=%v", v1, m2.Value)
								}
							}
						} else {
							t.Errorf("No value for %s found in decom event", pt.ID)
						}
					} else {
						t.Errorf("An error occurred while getting %s's value: %v", pt.ID, err)
					}
				}
			} else {
				t.Errorf("An error occurred unmarshalling a decom event: %v.  The response was %v", err, string(msg))
			}
		}
	})
}

func equalCarefully(a, b interface{}) bool {
	switch a1 := a.(type) {
	case int:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return int64(a1) == int64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case int8:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return int64(a1) == int64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case int16:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return int64(a1) == int64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case int32:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return int64(a1) == int64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case int64:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return int64(a1) == int64(b1) || uint64(a1) == uint64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case uint:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return uint64(a1) == uint64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case uint8:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return int64(a1) == int64(b1) || uint64(a1) == uint64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case uint16:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return int64(a1) == int64(b1) || uint64(a1) == uint64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case uint32:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case uint64:
			return int64(a1) == int64(b1) || uint64(a1) == uint64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case uint64:
		switch b1 := b.(type) {
		case int:
			return int64(a1) == int64(b1)
		case int8:
			return int64(a1) == int64(b1)
		case int16:
			return int64(a1) == int64(b1)
		case int32:
			return int64(a1) == int64(b1)
		case int64:
			return int64(a1) == int64(b1)
		case uint:
			return int64(a1) == int64(b1)
		case byte:
			return int64(a1) == int64(b1)
		case uint16:
			return int64(a1) == int64(b1)
		case uint32:
			return int64(a1) == int64(b1)
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float64(a1) == float64(b1)
		case uint64:
			return int64(a1) == int64(b1) || uint64(a1) == uint64(b1)
		default:
			return false
		}
	case float32:
		switch b1 := b.(type) {
		case float32:
			return float32(a1) == float32(b1)
		case float64:
			return float32(a1) == float32(b1)
		case int:
			return float32(a1) == float32(b1)
		case uint:
			return float32(a1) == float32(b1)
		case int16:
			return float32(a1) == float32(b1)
		case int32:
			return float32(a1) == float32(b1)
		case int64:
			return float32(a1) == float32(b1)
		case uint8:
			return float32(a1) == float32(b1)
		case uint16:
			return float32(a1) == float32(b1)
		case uint32:
			return float32(a1) == float32(b1)
		case uint64:
			return float32(a1) == float32(b1)
		default:
			return false
		}
	case float64:
		switch b1 := b.(type) {
		case float32:
			return float64(a1) == float64(b1)
		case float64:
			return float64(a1) == float64(b1)
		case int:
			return float64(a1) == float64(b1)
		case uint:
			return float64(a1) == float64(b1)
		case int16:
			return float64(a1) == float64(b1)
		case int32:
			return float64(a1) == float64(b1)
		case int64:
			return float64(a1) == float64(b1)
		case uint8:
			return float64(a1) == float64(b1)
		case uint16:
			return float64(a1) == float64(b1)
		case uint32:
			return float64(a1) == float64(b1)
		case uint64:
			return float64(a1) == float64(b1)
		default:
			return false
		}
	case string:
		switch b1 := b.(type) {
		case string:
			return a1 == b1
		default:
			return false
		}
	default:
		return false
	}
}

func withRunningServer(t *testing.T, port int, f func(server *Server)) error {
	server := Server{
		Host:        "",
		Port:        serverPort,
		StaticFiles: "../../../../../projects/warp_data/dist/"}

	if bypassWithRunningServer {
		f(&server)
		return nil
	} else {
		wg := sync.WaitGroup{}
		wg.Add(1)

		// Start the server
		go func() {
			server.Run()
			wg.Done()
		}()

		time.Sleep(3 * time.Second)

		// Run the test in this goroutine
		f(&server)

		// Now, we're done
		server.handleShutdown(nil, nil)
		wg.Wait()
		return nil
	}
}

func getWebsocketConnection(t *testing.T, u url.URL) (*websocket.Conn, bool) {
	c, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err == websocket.ErrBadHandshake {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		t.Errorf("handshake failed with status %d, body: %v", resp.StatusCode, buf.String())
		return nil, false
	}
	if err != nil {
		t.Errorf("websocket creation failed: %s", err.Error())
		return nil, false
	}
	return c, true
}

func localSendJSON(t *testing.T, conn *websocket.Conn, msg interface{}) bool {
	if bytes, err := json.Marshal(msg); err == nil {
		return localSend(t, conn, bytes)
	}
	t.Errorf("Error preparing json for a message: %s", msg)
	return false
}

func localSend(t *testing.T, conn *websocket.Conn, msg []byte) bool {
	if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		t.Errorf("Error writing websocket message: %v", err)
		if err1 := conn.Close(); err1 != nil {
			t.Errorf("Error closing websocket connection: %v", err1)
		}
		return false
	}
	return true
}

//
// Support constants, structs and functions
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

func getRESTResponse(t *testing.T, to string, from interface{}) bool {
	// Get the dictionary
	r, err := http.Get(to)
	if err != nil {
		t.Errorf("An error occurred when sending the REST request: %v", err)
		return false
	}
	defer r.Body.Close()
	contents, err := ioutil.ReadAll(r.Body)
	if err != nil {
		t.Errorf("An error occurred when reading the response stream: %v", err)
		return false
	}
	err = json.Unmarshal(contents, from)
	if err != nil {
		t.Errorf("An error occurred unmarshalling an REST response: %v.  The response was %v", err, string(contents))
		return false
	}
	return true
}

func loadDictionaryMaybe(t *testing.T) {
	if dictionary != nil {
		return
	}
	// For now, we're reading the same dictionary that the server is using
	filename := "/home/mshirley/rp.dictionary.json.gz"
	var err error
	dictionary, err = ccsds.LoadDictionary(filename)
	if err != nil {
		t.Errorf("An error occurred loading %s: %v", filename, err)
		t.Fail()
	}

	// Look for duplicate apids
	//	apids := make(map[int]int, len(dictionary.Packets))
	//	for _, pkt := range dictionary.Packets {
	//		apids[pkt.APID] = 1 + apids[pkt.APID]
	//	}
	//	for apid, count := range apids {
	//		if count > 1 {
	//			lst := make([]string, 0, count)
	//			for _, pkt := range dictionary.Packets {
	//				if pkt.APID == apid {
	//					lst = append(lst, pkt.ID)
	//				}
	//			}
	//			fmt.Printf("apid=%d count=%d lst=%v\n", apid, count, lst)
	//		}
	//	}

}

func getShuffledPoints(t *testing.T, r *rand.Rand) []string {
	loadDictionaryMaybe(t)
	// Get all points and shuffle then randomly (but repeatably due to seed above)
	allPoints := getAllPointNames(getAllPoints(dictionary))
	r.Shuffle(len(allPoints), func(i, j int) {
		allPoints[i], allPoints[j] = allPoints[j], allPoints[i]
	})
	return allPoints
}

//
// Subscriber
//

type Subscriber struct {
	server *Server
	conn   *websocket.Conn
	t      *testing.T
}

type subscriptionMap map[string]bool

func (s *Subscriber) TestSubscriptionsSubscribeShuffled(seed int64) {
	randomSource := rand.NewSource(seed)
	random := rand.New(randomSource)

	shuffledIDs := getShuffledPoints(s.t, random)

	// Test subscription for a bogus point
	to := SubscribeRequest{Request: "subscribe", Token: 1, IDs: []string{"bogus.point"}}
	var from SubscribeResponse
	if ok := s.getWebsocketResponse(&to, &from); ok {
		if len(from.BadIDs) != 1 || from.BadIDs[0] != "bogus.point" {
			s.t.Errorf("Subscribed to a non-existant point.  Expected that point to be in response.BadIDs.")
		}
	}

	// Now, the main show
	subscribed := make(subscriptionMap, 100)
	const maxAddsAtOneTime int = 20
	const maxDeletionsAtOneTime float32 = 0.8
	const cycles int = 1

	for i := 0; i < cycles; i++ {

		ids := shuffledIDs // copy

		for len(ids) > 0 {
			toAddCount := rand.Intn(maxAddsAtOneTime)
			if toAddCount > 0 {
				var toAdd []string
				toAdd, ids = popStrings(toAddCount, ids)

				//				s.t.Log(fmt.Sprintf("Subscribing n=%d lst=%v\n", len(toAdd), toAdd))
				//				fmt.Printf("%d subscribing count=%d\n", seed, len(toAdd))

				time.Sleep(1 * time.Millisecond)
				var to interface{}
				to = SubscribeRequest{Request: "subscribe", Token: 1, IDs: toAdd}
				var from1 SubscribeResponse
				if ok := s.getWebsocketResponse(&to, &from1); ok {
					if from1.BadIDs != nil && len(from1.BadIDs) != 0 {
						s.t.Errorf("Subscribed valid ids but got badIDs response: %v", from1.BadIDs)
					}
				} else {
					return
				}

				// Add to the local list of subscriptions
				for _, id := range toAdd {
					subscribed[id] = true
				}

				//				fmt.Printf("%d asking for report\n", seed)

				// Fetch and check report
				time.Sleep(1 * time.Millisecond)
				to = GenericRequest{Request: "report-subscriptions", Token: 1}
				var from2 ReportSubscriptionsResponse
				if ok := s.getWebsocketResponse(&to, &from2); ok {
					if !checkCurrentSubscriptions(s.t, from2.IDs, subscribed) {
						return
					}
				} else {
					return
				}

				//				s.t.Log(fmt.Sprintf("Reported n=%d lst=%v\n", len(from2.IDs), from2.IDs))
				//				s.t.Log(fmt.Sprintf("local    n=%d lst=%v\n", len(subscribed), subscribed.getKeys()))

				//				fmt.Printf("%d removing locals\n", seed)

				// Unsubscribe
				toRemoveCount := int(float32(len(subscribed)) * maxDeletionsAtOneTime)
				if toRemoveCount <= 0 {
					continue
				}
				toRemove := make([]string, 0, toRemoveCount)
				for k := range subscribed {
					toRemove = append(toRemove, k)
					if len(toRemove) >= toRemoveCount {
						break
					}
				}

				// Update the local subscriptions (first, in this case)
				for _, id := range toRemove {
					if _, ok := subscribed[id]; !ok {
						s.t.Errorf("Planning to delete %s but its not in the local subscriptions", id)
					}
					delete(subscribed, id)
				}

				//				fmt.Printf("%d unsubscribing count=%d\n", seed, len(toRemove))

				// Unsubscribe
				time.Sleep(1 * time.Millisecond)
				to = SubscribeRequest{Request: "unsubscribe", Token: 1, IDs: toRemove}
				var from3 SubscribeResponse
				if ok := s.getWebsocketResponse(&to, &from3); ok {
					if from3.BadIDs != nil && len(from3.BadIDs) != 0 {
						s.t.Errorf("Unsubscribed valid ids but got badIDs response.")
					}
				} else {
					return
				}

				//				fmt.Printf("%d fetching report after unsubscribe\n", seed)

				// Fetch and check report
				time.Sleep(1 * time.Millisecond)
				to = GenericRequest{Request: "report-subscriptions", Token: 1}
				var from4 ReportSubscriptionsResponse
				if ok := s.getWebsocketResponse(&to, &from4); ok {
					if !checkCurrentSubscriptions(s.t, from4.IDs, subscribed) {
						return
					}
				} else {
					return
				}

				//				fmt.Printf("%d after report fetch\n", seed)
			}
		}
	}
}

func getAllPointNames(points []*ccsds.PointInfo) []string {
	r := make([]string, len(points))
	for i, pt := range points {
		r[i] = pt.ID
	}
	return r
}

func getAllPoints(d *ccsds.TelemetryDictionary) []*ccsds.PointInfo {
	// Count points
	count := 0
	for _, pkt := range d.Packets {
		count += len(pkt.Points)
	}
	r := make([]*ccsds.PointInfo, count)
	ptr := 0
	for _, pkt := range d.Packets {
		for _, pt := range pkt.Points {
			r[ptr] = pt
			ptr++
		}
	}
	return r
}

func popStrings(n int, list []string) ([]string, []string) {
	if n >= len(list) {
		return list, list[0:0]
	}
	return list[0:n], list[n:]
}

func (m subscriptionMap) getKeys() []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	return r
}

func checkCurrentSubscriptions(t *testing.T, reported []string, local subscriptionMap) bool {
	if reported == nil {
		t.Errorf("reported ids == nil")
		return false
	}
	if len(reported) != len(local) {
		t.Log(fmt.Printf("reported n=%d ids=%v\n", len(reported), reported))
		t.Log(fmt.Printf("local    n=%d ids=%v\n", len(local), local))
		t.Errorf("reported subscription ids length %d != local subscriptions length %d", len(reported), len(local))
		return false
	}
	for _, id := range reported {
		if !local[id] {
			t.Errorf("reported subscription %s not found in local map", id)
			return false
		}
	}
	return true
}

func (s *Subscriber) getWebsocketResponse(to interface{}, from interface{}) bool {
	bytes, err := json.Marshal(to)
	if err != nil {
		s.t.Errorf("An error occurred while marshaling a websocket message to send: %v", err)
		return false
	}

	err = s.conn.WriteMessage(websocket.TextMessage, bytes)
	if err != nil {
		s.t.Errorf("An error occurred while writing a websocket message: %v.  The message was %s", err, string(bytes))
		return false
	}

	_, bytes, err = s.conn.ReadMessage()
	if err != nil {
		s.t.Errorf("Error receiving websocket response: %v", err)
		return false
	}

	err = json.Unmarshal(bytes, from)
	if err != nil {
		s.t.Errorf("Error unmarshalling websocket reply: %v", err)
		return false
	}
	return true
}

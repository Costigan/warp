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

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/bits"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/Costigan/warp/ccsds"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

//
// Server
//

// Server handles realtime and history connections to multiple clients
type Server struct {
	// Configuration
	Host string
	Port int

	StaticFiles       string // Location of static files
	DictionaryPrefix  string
	WebsocketPrefix   string
	HistoryPrefix     string
	PersistancePrefix string

	// State
	Session *Session

	// Internal state
	clients             *map[*websocket.Conn]*Client // immutable, updated by handleSubscriptions()
	packetDispatchTable [2048]*apidDispatch          // values in slots are immutable, nil means no subscriptions, updated by handleSubscriptions()

	// Channels
	packetChan chan ccsds.Packet // incoming packets

	addClientChan                 chan *Client
	removeClientChan              chan *Client
	updateClientSubscriptionsChan chan *updateClientSubscriptionsMsg // add/remove subscriptions
	rebuildApidDispatch           chan map[int]bool

	StopRequest chan os.Signal
}

// Run runs a web server
func (server *Server) Run() {
	// Prepare defaults
	if server.Port == 0 {
		server.Port = 8000
	}
	// The default server.Host is ""
	if server.DictionaryPrefix == "" {
		server.DictionaryPrefix = "/dictionary"
	}
	if server.WebsocketPrefix == "" {
		server.WebsocketPrefix = "/realtime/"
	}
	if server.HistoryPrefix == "" {
		server.HistoryPrefix = "/history"
	}
	if server.PersistancePrefix == "" {
		server.PersistancePrefix = "/couch"
	}

	// Initialize internal state

	// Initialize channels
	server.clients = &map[*websocket.Conn]*Client{}
	server.packetChan = make(chan ccsds.Packet, 300)
	server.addClientChan = make(chan *Client, 20)
	server.removeClientChan = make(chan *Client, 20)
	server.updateClientSubscriptionsChan = make(chan *updateClientSubscriptionsMsg, 20)
	server.rebuildApidDispatch = make(chan map[int]bool, 20)

	// For now, build in the session name
	server.Session = &Session{Name: "demo"}
	if err := server.Session.loadDictionary(); err != nil {
		fmt.Println(err)
		return
	}

	router := mux.NewRouter()

	// REST (order matters)
	dictionarySubrouter := router.PathPrefix(server.DictionaryPrefix).Subrouter()

	dictionarySubrouter.HandleFunc("/{session}/id/{id}", func(w http.ResponseWriter, r *http.Request) { handleDictionaryGetID(server, w, r) }).Methods("GET")
	dictionarySubrouter.HandleFunc("/{session}/root", func(w http.ResponseWriter, r *http.Request) { handleDictionaryRoot(server, w, r) }).Methods("GET")
	dictionarySubrouter.HandleFunc("/{session}", func(w http.ResponseWriter, r *http.Request) { handleWholeDictionary(server, w, r) }).Methods("GET")

	//	router.HandleFunc("/history", handleHTTP).Methods("GET")

	router.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		handleHistory(server, w, r)
	}).Methods("GET")

	router.HandleFunc("/couch", handleCouch)
	router.HandleFunc("/couch/{rest:.*}", handleCouch)

	router.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		server.handleReport(w, r)
	}).Methods("GET")

	router.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		server.handleShutdown(w, r)
	}).Methods("GET")

	// WebSocket
	router.HandleFunc(server.WebsocketPrefix, func(w http.ResponseWriter, req *http.Request) {
		server.serveWS(w, req)
	})

	// Files
	router.PathPrefix("/").Handler(http.StripPrefix("/", http.FileServer(http.Dir(server.StaticFiles))))

	// add/remove clients, update subscriptions
	go server.handleSubscriptions()

	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)
	h := &http.Server{Addr: addr, Handler: router}

	// Receive interrupts and shut down gracefully
	server.StopRequest = make(chan os.Signal, 2)
	signal.Notify(server.StopRequest, os.Interrupt)

	// Run the server
	go func() {
		log.Printf("Listening on %s\n", addr)
		err := h.ListenAndServe()
		if err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-server.StopRequest
	log.Printf("Shutting down the server ...\n")
	h.Shutdown(context.Background())
	log.Printf("Server gracefully stopped.\n")
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 16384,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (server *Server) serveWS(w http.ResponseWriter, req *http.Request) {
	//	fmt.Println("in serveWS")
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Println(err)
		return
	}
	//	fmt.Println("in serveWS after upgrade")
	client := newClient(server, conn)
	server.addClientChan <- client
}

//
// Handle Subscriptions
//

// All management of subscriptions is centralized here.  The
// datastructures are contained on the server and client objects and
// don't allow concurrent access.
//
// The implementation goals are:
// 1. The code path that decom's and distributes telemetry can't be
//    blocked while dispatch tables are updated
// 2. Reasonably efficient (don't rebuild everything every time any
//    subscription changes)
// 3. Simplicity reduces bugs
//
// The dispatch table ...

func (server *Server) handleSubscriptions() {
	dict := server.Session.Dictionary
	for {
		select {

		case client := <-server.addClientChan:
			// add a client
			oldClientMap := *server.clients
			newClientMap := make(map[*websocket.Conn]*Client)
			for oldconn, oldclient := range oldClientMap {
				newClientMap[oldconn] = oldclient
			}
			newClientMap[client.conn] = client
			server.clients = &newClientMap
			// No need to touch the dispatch table

			go client.writePump()
			go client.readPump()

		case client := <-server.removeClientChan:
			oldConn := client.conn
			client.conn = nil
			// attempt to close the connection
			if oldConn != nil {
				err := oldConn.Close()
				if err != nil {
					fmt.Printf("removing client: error closing connection: %v", err.Error())
				}
			}

			// remove a client; rebuild dispatch table
			oldClientMap := *server.clients
			newClientMap := make(map[*websocket.Conn]*Client)
			for oldconn, oldclient := range oldClientMap {
				if oldclient != client {
					newClientMap[oldconn] = oldclient
				}
			}
			server.clients = &newClientMap

			// Update all apid subscriptions this client had
			apids := make(map[int]bool)
			for apid := range client.subscriptions {
				apids[apid] = true
			}
			server.rebuildApidDispatch <- apids

		case msg := <-server.updateClientSubscriptionsChan:
			// Process a subscription request from a client
			// Lookup the ids

			//DEBUG
			fmt.Printf("server: (un)subscribe n=%d, msg.ids=%v\n", len(msg.ids), msg.ids)

			points, badIDs := lookupSubscriptionIds(dict, msg.ids)
			var apids map[int]bool

			if len(points) > 0 {
				//DEBUG
				fmt.Printf("Processing %d subscription ids\n", len(points))

				// There are some points to process
				newSubscriptions := copyClientSubscriptions(msg.client.subscriptions)
				apids = make(map[int]bool) // keep track of all apids touched
				for _, pt := range points {
					apids[pt.APID] = true
					bits, ok := newSubscriptions[pt.APID]
					if !ok {
						pkt, _ := dict.GetPacketByAPID(pt.APID)
						bits = NewBitArray(len(pkt.Points))
						newSubscriptions[pt.APID] = bits
					}
					if msg.isAdd {
						bits.SetBit(pt.SeqInPacket)
					} else {
						bits.ClearBit(pt.SeqInPacket)
					}
				}
				msg.client.subscriptions = newSubscriptions
				server.rebuildApidDispatch <- apids

				//DEBUG
				fmt.Printf("Reporting subscription counts\n")
				for apid := range apids {
					pkt, ok := dict.GetPacketByAPID(apid)
					if !ok {
						fmt.Printf("Error in reporting apid=%d\n", apid)
					}
					subs := newSubscriptions[apid]
					count := subs.BitCount()
					fmt.Printf("apid=%d name=%s BitCount=%d\n", apid, pkt.Name, count)
				}

			}

			//DEBUG
			fmt.Printf("apids touched: %v", apids)

			// Generate a response to the client
			root := make(map[string]interface{})
			if msg.isAdd {
				root["response"] = "subscribe"
			} else {
				root["response"] = "unsubscribe"
			}
			root["token"] = msg.token
			if len(badIDs) > 0 {
				root["status"] = "error"
				root["bad_ids"] = badIDs
			} else {
				root["status"] = "success"
			}
			sendJSON(root, msg.client)

		case apids := <-server.rebuildApidDispatch:

			for apid := range apids {
				pkt, ok := dict.GetPacketByAPID(apid)
				if !ok {
					continue
				}
				mask := NewBitArray(len(pkt.Points))
				clients := make([]*Client, 20)
				for _, client := range *server.clients {
					clientSubscriptions := client.subscriptions[apid]
					if clientSubscriptions != nil && !clientSubscriptions.IsZero() {
						mask.OrInto(*clientSubscriptions)
						clients = append(clients, client)
					}
				}
				if mask.IsZero() {
					// No subscriptions for this apid
					server.packetDispatchTable[apid] = nil
				} else {
					// Build the apidDispatch
					points := make([]*ccsds.PointInfo, len(pkt.Points))
					for i, point := range pkt.Points {
						if mask.GetBit(i) {
							points = append(points, point)
						}
					}
					// Atomic update
					server.packetDispatchTable[apid] = &apidDispatch{clients: clients, points: points}
				}
			}
		}
	}
}

func copyClientSubscriptions(subscriptions map[int]*BitArray) map[int]*BitArray {
	newSubscriptions := make(map[int]*BitArray, len(subscriptions))
	for k, v := range subscriptions {
		newSubscriptions[k] = v.Copy()
	}
	return newSubscriptions
}

func lookupSubscriptionIds(dict *ccsds.TelemetryDictionary, ids []string) ([]*ccsds.PointInfo, []string) {
	// The way the returned values are used, it doesn't matter if there are duplicate ids in the input, so I won't try to filter them out
	points := make([]*ccsds.PointInfo, 0, len(ids))
	badIDs := make([]string, 0, 10)
	for _, id := range ids {
		if strings.Contains(id, ".") {
			if pt, ok := dict.GetPointByID(id); ok {
				points = append(points, pt)
			} else {
				badIDs = append(badIDs, id)
			}
		} else {
			if pi, ok := dict.GetPacketByID(id); ok {
				for _, pt := range pi.Points {
					points = append(points, pt)
				}
			} else {
				badIDs = append(badIDs, id)
			}
		}
	}
	return points, badIDs
}

// One of these will be stored in each element of the decom dispatch
// table These are function (won't be modified), only rebuilt.  The
// entries in the dispatch table can be changed as atomic operations

type apidDispatch struct {
	clients []*Client
	points  []*ccsds.PointInfo
}

//
// Realtime Packet Decomm
//

func (server *Server) packetPump() {
	packetChan := server.packetChan
	for {
		pkt := <-packetChan
		msg := decomPacket(pkt, server.packetDispatchTable) // Refetch the table every time
		fmt.Println(string(msg))
	}
}

func decomPacket(pkt ccsds.Packet, dispatchTable [2048]*apidDispatch) []byte {
	return make([]byte, 0)
}

//
// HandleHistory
//

func handleHistory(server *Server, w http.ResponseWriter, r *http.Request) {
	fmt.Printf("history: req=%v\n", r.URL)
	prepareHeader(w, r)
	json.NewEncoder(w).Encode(RestErrorResponse{Error: "SessionNotFound", Message: "Session not found"})
}

//
// HandleReport
//

func (server *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	clients := *server.clients
	connections := make([]ReportWebsocketConnection, len(clients))
	for conn, client := range clients {
		ids := client.getSubscriptionIDs()
		connections = append(connections, ReportWebsocketConnection{Address: conn.RemoteAddr().String(), SubscriptionCount: len(ids), IDs: ids})
	}

	response := ReportTemplate{Version: "0.1", Session: *server.Session, Connections: connections, ConnectionCount: len(connections)}
	prepareHeader(w, r)
	json.NewEncoder(w).Encode(response)
}

//
// HandleShutdown
//

func (server *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	server.StopRequest <- &FakeInterrupt{}
}

// FakeInterrupt is for mocking the server shutdown message
type FakeInterrupt struct{}

// String is needed to match an interrupt's interface
func (f *FakeInterrupt) String() string { return "fake interrupt" }

// Signal is needed to match an interrupt's interface
func (f FakeInterrupt) Signal() {}

////////////////////////////////////////////////////////////////////////
// Client
////////////////////////////////////////////////////////////////////////

// Client is the middleman between the websocket connection and the server
type Client struct {
	server        *Server
	conn          *websocket.Conn
	msgChan       chan []byte       // Client receives msgs from channel and sends to the websocket connection
	subscriptions map[int]*BitArray // immutable
}

func newClient(server *Server, conn *websocket.Conn) *Client {
	return &Client{
		server:        server,
		conn:          conn,
		msgChan:       make(chan []byte, 32),
		subscriptions: make(map[int]*BitArray),
	}
}

//
// Read Pump
//

func (client *Client) readPump() {
	for {
		messageType, p, err := client.conn.ReadMessage()
		if messageType == websocket.CloseMessage {
			requestRemoveClient(client)
			log.Printf("websocket: %s closed", client.conn.RemoteAddr().String())
			return
		} else if err != nil {
			oldConn := client.conn
			requestRemoveClient(client)
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived, websocket.CloseAbnormalClosure) {
				log.Printf("websocket(%s) closed unexpectedly: %v", client.conn.RemoteAddr().String(), err.Error())
			} else {
				log.Printf("websocket: %s closed", oldConn.RemoteAddr().String())
			}
			return
		} else if messageType != websocket.TextMessage {
			oldConn := client.conn
			requestRemoveClient(client)
			log.Printf("websocket(%s) received a non-text message of type %d", oldConn.RemoteAddr().String(), messageType)
			return
		}

		var msg interface{}
		err = json.Unmarshal(p, &msg)
		if err != nil {
			log.Printf("websocket(%s) received a non-json message: %s", client.conn.RemoteAddr().String(), string(p))
			continue
		}

		msgObject, ok := msg.(map[string]interface{})
		if !ok {
			log.Printf("websocket(%s) received a json message that was not an object: %s", client.conn.RemoteAddr().String(), string(p))
			continue
		}

		msgVerb, ok := msgObject["request"].(string)
		if !ok {
			log.Printf("websocket(%s) received a json message object with no request verb: %s", client.conn.RemoteAddr().String(), string(p))
			continue
		}
		msgToken := msgObject["token"]

		var err1, err2 error
		switch msgVerb {
		case "ping":
			var msg GenericRequest
			err1 = json.Unmarshal(p, &msg)
			if err1 == nil {
				err2 = client.handlePing(&msg)
			}
		case "subscribe":
			var msg SubscribeRequest
			err1 = json.Unmarshal(p, &msg)
			if err1 == nil {
				err2 = client.handleSubscribe(&msg)
			}
		case "unsubscribe":
			var msg UnsubscribeRequest
			err1 = json.Unmarshal(p, &msg)
			if err1 == nil {
				err2 = client.handleUnsubscribe(&msg)
			}
		case "report-subscriptions":
			client.handleReportSubscriptions()
		default:
			err1 = fmt.Errorf("websocket(%s) received a request(%s) with no handler: %s", client.conn.RemoteAddr().String(), msgVerb, string(p))
		}

		if err1 != nil {
			log.Printf("websocket(%s) error parsing %s request: %v", client.conn.RemoteAddr().String(), msgVerb, err1)
			sendJSON(ErrorResponse{Response: msgVerb, Token: msgToken, Error: err1.Error()}, client)
		} else if err2 != nil {
			log.Printf("websocket(%s) error processing %s request: %v", client.conn.RemoteAddr().String(), msgVerb, err2)
			sendJSON(ErrorResponse{Response: msgVerb, Token: msgToken, Error: err2.Error()}, client)
		}
	}
}

//
// Write Pump
//

func (client *Client) writePump() {
	for msg := range client.msgChan {
		c := client.conn
		if c == nil {
			continue
		}
		err := c.WriteMessage(websocket.TextMessage, msg)
		if err == websocket.ErrCloseSent {
			requestRemoveClient(client)
			return
		}
		if err != nil {
			log.Printf("websocket(%s) error on write: %v", client.conn.RemoteAddr().String(), err)
			requestRemoveClient(client)
			return
		}
	}
	// Drop the bytes on the floor here.  Later, send back to the server
}

func requestRemoveClient(client *Client) {
	client.conn = nil
	client.server.removeClientChan <- client
}

//
// Message Handlers
//

func (client *Client) handlePing(r *GenericRequest) error {
	sendJSON(GenericResponse{Response: "ping", Token: r.Token}, client)
	return nil
}

func (client *Client) handleSubscribe(r *SubscribeRequest) error {
	client.server.updateClientSubscriptionsChan <- &updateClientSubscriptionsMsg{isAdd: true, ids: r.IDs, client: client, token: r.Token}
	return nil
}

func (client *Client) handleUnsubscribe(r *UnsubscribeRequest) error {
	client.server.updateClientSubscriptionsChan <- &updateClientSubscriptionsMsg{isAdd: false, ids: r.IDs, client: client, token: r.Token}
	return nil
}

func (client *Client) handleReportSubscriptions() {
	sendJSON(ReportSubscriptionsResponse{Response: "report-subscriptions", IDs: client.getSubscriptionIDs()}, client)
}

func (client *Client) getSubscriptionIDs() []string {
	ids := make([]string, 0, 256)
	dict := client.server.Session.Dictionary
	subscriptions := client.subscriptions
	for apid, bits := range subscriptions {
		if pkt, ok := dict.GetPacketByAPID(apid); ok {
			for i, pt := range pkt.Points {
				if bits.GetBit(i) {
					ids = append(ids, pt.ID)
				}
			}
		}
	}
	return ids
}

//
// Message Helper Functions
//

// send a message to one or more clients
func send(msg []byte, clients ...*Client) {
	for i := 0; i < len(clients); i++ {
		clients[i].msgChan <- msg
	}
}

// sendJSON to one or more clients
func sendJSON(msg interface{}, clients ...*Client) {
	if len(clients) < 1 {
		return
	}
	if bytes, err := json.Marshal(msg); err == nil {
		send(bytes, clients...)
	} else {
		log.Printf("Error preparing json for a message: %s", msg)
	}
}

//
// Public Websocket Message Templates
//

// GenericRequest is a message template.  Also used as a minimal request
type GenericRequest struct {
	Request string      `json:"request"`
	Token   interface{} `json:"token"`
}

// GenericResponse is a message template
type GenericResponse struct {
	Response string      `json:"response"`
	Token    interface{} `json:"token"`
}

// SubscribeRequest is a message template
type SubscribeRequest struct {
	Request string      `json:"request"`
	Token   interface{} `json:"token"`
	IDs     []string    `json:"ids"`
}

// SubscribeResponse is a message template
type SubscribeResponse struct {
	Response string      `json:"response"`
	Token    interface{} `json:"token"`
	Status   string      `json:"status"`
	BadIDs   []string    `json:"bad_ids"`
}

// UnsubscribeRequest is a message template
type UnsubscribeRequest struct {
	Request string      `json:"request"`
	Token   interface{} `json:"token"`
	IDs     []string    `json:"ids"`
}

// UnsubscribeResponse is a message template
type UnsubscribeResponse struct {
	Response string      `json:"response"`
	Token    interface{} `json:"token"`
	Status   string      `json:"status"`
	BadIDs   []string    `json:"bad_ids,omitempty"`
}

// ErrorResponse is a generic message template
type ErrorResponse struct {
	Response string      `json:"response"`
	Token    interface{} `json:"token"`
	Error    string      `json:"error"`
}

// ReportSubscriptionsResponse is a generic message template
type ReportSubscriptionsResponse struct {
	Response string   `json:"response"`
	IDs      []string `json:"ids"`
}

//
// Public REST Message Templates
//

// RestErrorResponse is a message template
type RestErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

//
// Internal Message Templates
//

type updateClientSubscriptionsMsg struct {
	client *Client
	isAdd  bool
	token  interface{}
	ids    []string
}

// clientMsg contains a single message for one or more clients.
type clientMsg struct {
	clients []*Client
	bytes   []byte
}

////////////////////////////////////////////////////////////////////////
// Session
////////////////////////////////////////////////////////////////////////

// Session holds session information.  Note that a warp process can host only a single session
type Session struct {
	Name           string                     `json:"name"`
	Dictionary     *ccsds.TelemetryDictionary `json:"-"`
	DictionaryRoot *DictionaryRootResponse    `json:"-"`
}

func (session *Session) loadDictionary() error {
	// Simplified, for now
	filename := "/home/mshirley/rp.dictionary.json.gz"
	dictionary, err := ccsds.LoadDictionary(filename)
	if err != nil {
		return err
	}
	session.Dictionary = dictionary
	session.DictionaryRoot = makeDictionaryRoot(dictionary)
	fmt.Printf("There are %d packets in %s\r\n", len(dictionary.Packets), filename)
	fmt.Printf("There are %d packets in the root\r\n", len(session.DictionaryRoot.Packets))
	return nil
}

////////////////////////////////////////////////////////////////////////
// REST Handlers
////////////////////////////////////////////////////////////////////////

func handleHTTP(w http.ResponseWriter, req *http.Request) {
	fmt.Printf("handleHTTP %s\r\n", req.URL)
	//	resp, err := http.DefaultTransport.RoundTrip(req)

	remoteURL := "http://localhost:41401" + req.URL.Path
	fmt.Printf("remoteURL=%s\r\n", remoteURL)
	resp, err := http.DefaultClient.Get(remoteURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func handleCouch(w http.ResponseWriter, req *http.Request) {
	splits := strings.Split(req.URL.Path, string(os.PathSeparator))
	remoteURL := "http://localhost:5984/" + filepath.Join(splits[2:]...)
	//	fmt.Printf("couch: req=%v remote=%v\n", req.URL, remoteURL)
	resp, err := http.DefaultClient.Get(remoteURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func handleWholeDictionary(server *Server, w http.ResponseWriter, r *http.Request) {
	prepareHeader(w, r)
}

func handleDictionaryRoot(server *Server, w http.ResponseWriter, r *http.Request) {
	//	fmt.Println("in handleDictionaryRoot")
	prepareHeader(w, r)
	json.NewEncoder(w).Encode(server.Session.DictionaryRoot)
}

func handleDictionaryGetID(server *Server, w http.ResponseWriter, r *http.Request) {
	prepareHeader(w, r)
	vars := mux.Vars(r)
	id := vars["id"]
	//	fmt.Printf("in handleDictionaryGetID: id=%s\r\n", id)
	if strings.Contains(id, ".") {
		// We're asking for a point
		if pt, ok := server.Session.Dictionary.GetPointByID(id); ok {
			writePointJSON(w, pt, server.Session.Dictionary)
		} else {
			http.Error(w, fmt.Sprintf("can't find point %s", id), 404)
		}
	} else {
		// We're asking for a packet
		if pkt, ok := server.Session.Dictionary.GetPacketByID(id); ok {
			writePacketJSON(w, pkt, server.Session)
		} else {
			http.NotFound(w, r)
		}
	}
}

func prepareHeader(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Add("Content-Type", "application/json")
}

// Sample output for a packet.  The points array is a list json objects like the point sample below
// {
//   "response": "list_points",
//   "session": "demo",
//   "packet": "EPSIO_BIT",
//   "points": []
// }

func writePacketJSON(w http.ResponseWriter, pkt *ccsds.PacketInfo, session *Session) {
	fmt.Fprint(w, `{"response":"list_points","session":"`)
	fmt.Fprint(w, session.Name)
	fmt.Fprint(w, `","packet":"`)
	fmt.Fprint(w, pkt.ID)
	fmt.Fprint(w, `","points":[`)
	for i, pt := range pkt.Points {
		if i > 0 {
			fmt.Fprint(w, `,`)
		}
		writePointJSON(w, pt, session.Dictionary)
	}
	fmt.Fprint(w, `]}`)
}

// Sample output for a single point
// {
//   "name": "prop_log4",
//   "key": "EPSIO_BIT.prop_log4",
//   "values": [
//     {
//       "key": "utc",
//       "source": "timestamp",
//       "name": "Timestamp",
//       "format": "utc",
//       "hints": {
//         "x": 1
//       }
//     },
//     {
//       "key": "value",
//       "name": "Value",
//       "hints": {
//         "y": 1
//       },
//       "format": "enum",
//       "enumerations": [
//         {
//           "value": 0,
//           "string": "OFF"
//         },
//         {
//           "value": 1,
//           "string": "ON"
//         }
//       ]
//     }
//   ]
// }

func writePointJSON(w http.ResponseWriter, pt *ccsds.PointInfo, dict *ccsds.TelemetryDictionary) {
	typestring := dict.GetPointType(pt)
	fmt.Fprint(w, `{"name":"`)
	fmt.Fprint(w, pt.Name)
	fmt.Fprint(w, `","key":"`)
	fmt.Fprint(w, pt.ID)
	fmt.Fprint(w, `", "values": [{"key":"utc","source":"timestamp","name":"Timestamp","format":"utc","hints":{"domain":1}},{"key":"value","name":"Value","hints":{"range":1},"format":"`)
	fmt.Fprint(w, typestring)
	fmt.Fprint(w, `"}]}`)
}

//
// WebSocket Handlers
//

var dictionaryRootJSON DictionaryRootResponse

func makeDictionaryRoot(dictionary *ccsds.TelemetryDictionary) *DictionaryRootResponse {
	packets := make([]PacketJSON, 0, len(dictionary.Packets))
	for _, p := range dictionary.Packets {
		p1 := PacketJSON{ID: p.ID, Name: p.Name, APID: p.APID, Description: p.Documentation}
		packets = append(packets, p1)
	}
	return &DictionaryRootResponse{Response: "list_packets", Session: "demo", Packets: packets}
}

//
// Templates
//

// DictionaryRootResponse is a message template
type DictionaryRootResponse struct {
	Response string       `json:"response"`
	Session  string       `json:"session"`
	Packets  []PacketJSON `json:"packets"`
}

// PacketJSON is part of a message template
type PacketJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	APID        int    `json:"apid"`
	Description string `json:"description"`
}

// PointJSON is part of a message template
type PointJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	APID        int    `json:"apid"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Units       string `json:"units"`
	Conversion  string `json:"conversion"`
}

// PacketDictionaryResponse is part of a message template
type PacketDictionaryResponse struct {
	Response string       `json:"response"`
	Session  string       `json:"session"`
	Packet   string       `json:"packet"`
	Packets  []PointJSON2 `json:"points"`
}

// PointJSON2 is part of a message template
type PointJSON2 struct {
	Name   string                `json:"name"`
	Key    string                `json:"key"`
	Values []PointValuesTemplate `json:"values"`
}

// PointValuesTemplate is part of a message template
type PointValuesTemplate struct {
	Key    string      `json:"key"`
	Source string      `json:"source"`
	Name   string      `json:"name"`
	Format string      `json:"format"`
	Hints  interface{} `json:"hints"`
}

// ReportTemplate is part of a message template
type ReportTemplate struct {
	Version         string                      `json:"version"`
	Session         Session                     `json:"session"`
	Connections     []ReportWebsocketConnection `json:"connections"`
	ConnectionCount int                         `json:"connection_count"`
}

// ReportWebsocketConnection is part of a message template
type ReportWebsocketConnection struct {
	Address           string   `json:"address"`
	SubscriptionCount int      `json:"subscription_count"`
	IDs               []string `json:"ids"`
}

////////////////////////////////////////////////////////////////////////
// Utilities
////////////////////////////////////////////////////////////////////////

type byID []*ccsds.PointInfo

func (l byID) Len() int           { return len(l) }
func (l byID) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }
func (l byID) Less(i, j int) bool { return (*l[i]).ID < (l[j]).ID }

//
// Bit Array
//

// BitArray implements a set using a bit array.  It only includes operations needed by the server
type BitArray []uint64

// NewBitArray returns a new BitArray object
func NewBitArray(count int) *BitArray {
	if count < 0 {
		r := BitArray(make([]uint64, 0))
		return &r
	}
	r := BitArray(make([]uint64, 1+count/64))
	return &r
}

// SetBit sets the bit at pos to 1
func (b BitArray) SetBit(pos int) error {
	cell, bitpos := b.getPosition(pos)
	if cell < 0 || cell >= len(b) {
		return fmt.Errorf("bit position out-of-range: %d", pos)
	}
	b[cell] = b[cell] | (1 << bitpos)
	return nil
}

// ClearBit sets the bit at pos to 0
func (b BitArray) ClearBit(pos int) error {
	cell, bitpos := b.getPosition(pos)
	if cell < 0 || cell >= len(b) {
		return fmt.Errorf("bit position out-of-range: %d", pos)
	}
	b[cell] = b[cell] & (^(1 << bitpos))
	return nil
}

// GetBit returns the value of the bit as true/false.  If pos is out-of-range, the returned value is false
func (b BitArray) GetBit(pos int) bool {
	cell, bitpos := b.getPosition(pos)
	if cell < 0 || cell >= len(b) {
		return false
	}
	if (b[cell] & (1 << bitpos)) == 0 {
		return false
	}
	return true
}

// OrInto modifies the receiving BitArray, or'ing its values with the other bit array
func (b BitArray) OrInto(o BitArray) {
	max := len(b)
	if len(o) < max {
		max = len(o)
	}
	for i := 0; i < max; i++ {
		b[i] = b[i] | o[i]
	}
}

// IsZero returns true if all bits in this BitArray are 0, else false
func (b BitArray) IsZero() bool {
	for i := 0; i < len(b); i++ {
		if b[i] != 0 {
			return false
		}
	}
	return true
}

// Copy returns a copy of this bit array
func (b BitArray) Copy() *BitArray {
	r := BitArray(make([]uint64, len(b)))
	copy(r, b)
	return &r
}

func (b BitArray) getPosition(pos int) (int, uint) {
	return pos / 64, uint(pos) % 64
}

// BitCount returns the number of bits set
func (b BitArray) BitCount() int {
	count := 0
	for _, l := range b {
		count += bits.OnesCount64(l)
	}
	return count
}

func (b BitArray) elt(i int) uint64 {
	return b[i]
}

func (b BitArray) len() int {
	return len(b)
}

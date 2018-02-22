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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	//	"net/url"
	"sort"
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
	clients             map[*websocket.Conn]*Client
	packetDispatchTable *([][]*ccsds.PointInfo)

	// Channels
	packetChan       chan ccsds.Packet           // incoming packets
	subscriptionChan chan *subscriptionUpdateMsg // request to goroutine that manages subscriptions
}

// Run runs a web server
func (server *Server) Run() {
	// Prepare defaults
	if server.Port == 0 {
		server.Port = 8000
	}
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

	// initialize internal state
	server.clients = map[*websocket.Conn]*Client{}
	server.packetChan = make(chan ccsds.Packet)
	server.subscriptionChan = make(chan *subscriptionUpdateMsg)
	server.packetDispatchTable = makeEmptyPacketDispatchTable()

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

	router.HandleFunc("/couch", handleCouch)
	router.HandleFunc("/couch/{rest:.*}", handleCouch)

	// WebSocket
	router.HandleFunc(server.WebsocketPrefix, func(w http.ResponseWriter, req *http.Request) {
		server.serveWS(w, req)
	})

	// Files
	router.PathPrefix("/").Handler(http.StripPrefix("/", http.FileServer(http.Dir(server.StaticFiles))))

	go server.handleSubscriptions()

	listenString := fmt.Sprintf("%s:%d", server.Host, server.Port)
	fmt.Printf("listenString=%s\r\n", listenString)

	log.Fatal(http.ListenAndServe(listenString, router))
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 16384,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (server *Server) serveWS(w http.ResponseWriter, req *http.Request) {
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Println(err)
		return
	}

	client := newClient(server, conn)
	server.clients[conn] = client
	go client.writePump()
	go client.readPump()
}

func (server *Server) removeClient(client *Client) {
	//TODO: This should do something with the channels
	delete(server.clients, client.conn)
}

func (server *Server) handleSubscriptions() {
	d := server.Session.Dictionary
	for {
		msg, ok := <-server.subscriptionChan
		if !ok {
			break
		}
		switch msg.verb {
		case addSubscription:
			badIDs := modifyClientSubscription(d, msg)
			// Build the response to the client
			root := make(map[string]interface{})
			root["response"] = "subscribe"
			root["token"] = msg.token
			if len(badIDs) > 0 {
				root["status"] = "error"
				root["bad_ids"] = badIDs
			} else {
				root["status"] = "success"
			}
			sendJSON(root, msg.client)
			server.updateDispatch() // Rebuild the subscription dispatch table
		case deleteSubscription:
			badIDs := modifyClientSubscription(d, msg)
			// Build the response to the client
			root := make(map[string]interface{})
			root["response"] = "unsubscribe"
			root["token"] = msg.token
			if len(badIDs) > 0 {
				root["status"] = "error"
				root["bad_ids"] = badIDs
			} else {
				root["status"] = "success"
			}
			sendJSON(root, msg.client)
			server.updateDispatch() // Rebuild the subscription dispatch table
		}
	}
}

func modifyClientSubscription(d *ccsds.TelemetryDictionary, msg *subscriptionUpdateMsg) []string {
	subscriptions := msg.client.subscriptions
	badIDs := make([]string, 0, 8)
	for _, id := range msg.ids {
		if strings.Contains(id, ".") {
			if pt, ok := d.GetPointByID(id); ok {
				subscriptions[pt] = true
			} else {
				badIDs = append(badIDs, id)
			}
		} else {
			if pi, ok := d.GetPacketByID(id); ok {
				for _, pt := range pi.Points {
					subscriptions[pt] = true
				}
			} else {
				badIDs = append(badIDs, id)
			}
		}
	}
	return badIDs
}

//
// Packet Decom
//

func (server *Server) updateDispatch() {
	// Collect all PointInfos from all client subscription tables.  Eliminate duplicates
	m := make(map[*ccsds.PointInfo]bool)
	for _, client := range server.clients {
		for pointInfo := range client.subscriptions {
			m[pointInfo] = true
		}
	}

	// Convert the unique PointInfos to a list, then sort
	points := make(byID, 0, 128)
	for pointInfo := range m {
		points = append(points, pointInfo)
	}
	sort.Sort(byID(points))

	table := makeEmptyPacketDispatchTable()
	for _, pointInfo := range points {
		(*table)[pointInfo.APID] = append((*table)[pointInfo.APID], pointInfo)
	}

	// Now, update the dispatch table.  This is a single assignment and is atomic
	server.packetDispatchTable = table
}

type byID []*ccsds.PointInfo

func (l byID) Len() int           { return len(l) }
func (l byID) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }
func (l byID) Less(i, j int) bool { return (*l[i]).ID < (l[j]).ID }

func makeEmptyPacketDispatchTable() *([][]*ccsds.PointInfo) {
	table := make([][]*ccsds.PointInfo, 2048, 2048)
	for i := 0; i < 2048; i++ {
		table[i] = make([]*ccsds.PointInfo, 0, 8)
	}
	return &table
}

////////////////////////////////////////////////////////////////////////
// Client
////////////////////////////////////////////////////////////////////////

// Client is the middleman between the websocket connection and the server
type Client struct {
	server        *Server
	conn          *websocket.Conn
	msgChan       chan clientMsg
	subscriptions map[*ccsds.PointInfo]bool
}

func newClient(server *Server, conn *websocket.Conn) *Client {
	return &Client{
		server:        server,
		conn:          conn,
		msgChan:       make(chan clientMsg),
		subscriptions: make(map[*ccsds.PointInfo]bool),
	}
}

//
// Read Pump
//

func (client *Client) readPump() {
	for {
		messageType, p, err := client.conn.ReadMessage()
		if err != nil {
			log.Println(err)
			return
		}
		if messageType != websocket.TextMessage {
			log.Printf("websocket(%s) received a non-text message", client.conn.RemoteAddr().String())
			client.conn.Close()
			delete(client.server.clients, client.conn)
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
			var msg PingRequest
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
		default:
			err1 = fmt.Errorf("websocket(%s) received a request(%s) with no handler: %s", client.conn.RemoteAddr().String(), msgVerb, string(p))
		}

		if err1 != nil {
			log.Printf("websocket(%s) error parsing %s request: %v", client.conn.RemoteAddr().String(), msgVerb, err1)
			sendJSON(ErrorResponse{Response: msgVerb, Token: msgToken, Error: err.Error()}, client)
		} else if err2 != nil {
			log.Printf("websocket(%s) error processing %s request: %v", client.conn.RemoteAddr().String(), msgVerb, err2)
			sendJSON(ErrorResponse{Response: msgVerb, Token: msgToken, Error: err.Error()}, client)
		}
	}
}

//
// Write Pump
//

func (client *Client) writePump() {
	for {
		msg := <-client.msgChan
		err := client.conn.WriteMessage(websocket.TextMessage, msg.bytes)
		if err != nil {
			log.Printf("websocket(%s) error on write: %v", client.conn.RemoteAddr().String(), err)
			client.server.removeClient(client)
			return
		}
		if len(msg.clients) > 0 {
			msg.clients[0].msgChan <- clientMsg{clients: msg.clients[1:], bytes: msg.bytes}
		}
		// Drop the bytes on the floor here.  Later, send back to the server
	}
}

//
// Message Handlers
//

func (client *Client) handlePing(r *PingRequest) error {
	sendJSON(PingResponse{Response: "ping", Token: r.Token}, client)
	return nil
}

func (client *Client) handleSubscribe(r *SubscribeRequest) error {
	client.server.subscriptionChan <- &subscriptionUpdateMsg{verb: addSubscription, ids: r.IDs, client: client, token: r.Token}
	return nil
}

func (client *Client) handleUnsubscribe(r *UnsubscribeRequest) error {
	client.server.subscriptionChan <- &subscriptionUpdateMsg{verb: deleteSubscription, ids: r.IDs, client: client, token: r.Token}
	return nil
}

//
// Message Helper Functions
//

// send a message to one or more clients
func send(msg []byte, clients ...*Client) {
	if len(clients) > 0 {
		clients[0].msgChan <- clientMsg{bytes: msg, clients: clients[1:]}
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
// Public Message Templates
//

// PingRequest is a message template.  Also used as a minimal request
type PingRequest struct {
	Request string      `json:"request"`
	Token   interface{} `json:"token"`
}

// PingResponse is a message template
type PingResponse struct {
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
	BadIDs   []string    `json:"bad_ids"`
}

// ErrorResponse is a generic message template
type ErrorResponse struct {
	Response string      `json:"response"`
	Token    interface{} `json:"token"`
	Error    string      `json:"error"`
}

//
// Internal Message Templates
//

type subscriptionUpdateMsg struct {
	client *Client
	verb   int
	token  interface{}
	ids    []string
}

const (
	addSubscription    = int(iota)
	deleteSubscription = int(iota)
)

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
	Name           string
	Dictionary     *ccsds.TelemetryDictionary
	DictionaryRoot *DictionaryRootResponse
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
	fmt.Printf("couch: req=%v remote=%v\n", req.URL, remoteURL)
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
	fmt.Printf("in handleDictionaryGetID: id=%s\r\n", id)
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

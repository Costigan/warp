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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	//	"net/url"
	"strings"

	"github.com/Costigan/warp/ccsds"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	//	"github.com/koding/websocketproxy"
)

//
// Server
//

// WarpServer handles realtime and history connections to multiple clients
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
	clients map[*websocket.Conn]*Client

	// Channels
	packet_chan       chan ccsds.Packet         // incomming packets
	subscription_chan chan subscription_request // request to goroutine that manages subscriptions
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
		server.WebsocketPrefix = "/realtime"
	}
	if server.HistoryPrefix == "" {
		server.HistoryPrefix = "/history"
	}
	if server.PersistancePrefix == "" {
		server.PersistancePrefix = "/couch"
	}

	// initialize internal state
	server.clients = map[*websocket.Conn]*Client{}
	server.packet_chan = make(chan ccsds.Packet)
	server.subscription_chan = make(chan subscription_request)


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

	//dictionarySubrouter.HandleFunc("/{session}/id/{id}", handleHTTP).Methods("GET")
	//dictionarySubrouter.HandleFunc("/{session}/root", handleHTTP).Methods("GET")
	//dictionarySubrouter.HandleFunc("/{session}", handleHTTP).Methods("GET")

	//	router.HandleFunc("/history", handleHTTP).Methods("GET")

	router.HandleFunc(server.PersistancePrefix, handleCouch2)

	// WebSocket
	router.HandleFunc(server.WebsocketPrefix, func(w http.ResponseWriter, req *http.Request) {
		server.serveWS(w, req)
	})

	// Files
	router.PathPrefix("/").Handler(http.StripPrefix("/", http.FileServer(http.Dir(server.StaticFiles))))

	listenString := fmt.Sprintf("%s:%d", server.Host, server.Port)
	fmt.Printf("listenString=%s\r\n", listenString)

	log.Fatal(http.ListenAndServe(listenString, router))
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 16384,
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

func (server *Server) remove_client(client *Client) {
	//TODO: This should do something with the channels
	delete(server.clients, client.conn)
}

////////////////////////////////////////////////////////////////////////
// Client
////////////////////////////////////////////////////////////////////////

// Client is the middleman between the websocket connection and the server
type Client struct {
	server        *Server
	conn          *websocket.Conn
	msg_chan      chan client_msg
	subscriptions map[*ccsds.PacketInfo][]*ccsds.PointInfo
}

func newClient(server *Server, conn *websocket.Conn) *Client {
	return &Client{
		server:        server,
		conn:          conn,
		msg_chan:      make(chan client_msg),
		subscriptions: make(map[*ccsds.PacketInfo][]*ccsds.PointInfo)}
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

		msg_object, ok1 := msg.(map[string]interface{})
		if !ok1 {
			log.Printf("websocket(%s) received a json message that was not an object: %s", client.conn.RemoteAddr().String(), string(p))
			continue
		}

		msg_verb, ok2 := msg_object["request"].(string)
		if !ok2 {
			log.Printf("websocket(%s) received a json message object with no request verb: %s", client.conn.RemoteAddr().String(), string(p))
			continue
		}

		msg_token, ok3 := msg_object["token"].(string)
		if !ok3 {
			log.Printf("websocket(%s) received a json message object with no token: %s", client.conn.RemoteAddr().String(), string(p))
			continue
		}

		var handler_err error
		switch msg_verb {
		case "ping":
			handler_err = client.handlePing(msg_verb, msg_token, msg_object)
		case "subscribe":
			handler_err = client.handleSubscribe(msg_verb, msg_token, msg_object)
		case "unsubscribe":
			handler_err = client.handleUnsubscribe(msg_verb, msg_token, msg_object)
		default:
			log.Printf("websocket(%s) received a request(%s) with no handler: %s", client.conn.RemoteAddr().String(), msg_verb, string(p))
		}

		if handler_err != nil {
			log.Printf("websocket(%s) error processing verb(%s): %s", client.conn.RemoteAddr().String(), msg_verb, err)
			sendJSON(errorResponse{response: msg_verb, token: msg_token, error: err.Error()}, client)
			continue
		}
	}
}

//
// Write Pump
//

func (client *Client) writePump() {
	for {
		msg := <-client.msg_chan
		err := client.conn.WriteMessage(websocket.TextMessage, msg.bytes)
		if err != nil {
			log.Printf("websocket(%s) error on write:", client.conn.RemoteAddr().String(), err)
			client.server.remove_client(client)
			return
		}
		if len(msg.clients) > 0 {
			msg.clients[0].msg_chan <- client_msg{clients: msg.clients[1:], bytes: msg.bytes}
		}
		// Drop the bytes on the floor here.  Later, send back to the server
	}
}

//
// Message Handlers
//

func (client *Client) handlePing(verb string, token string, msg map[string]interface{}) error {
	sendJSON(PingResponse{Response: "ping", Token: token}, client)
	return nil
}

func (client *Client) handleSubscribe(verb string, token string, msg map[string]interface{}) error {
	if ids, ok := msg["ids"].([]string); ok {
		client.server.subscription_chan <- subscription_request{verb: addSubscription, ids: ids, client: client}
		return nil
	}
	return errors.New("couldn't find ids property")
}

func (client *Client) handleUnsubscribe(verb string, token string, msg map[string]interface{}) error {
	if ids, ok := msg["ids"].([]string); ok {
		client.server.subscription_chan <- subscription_request{verb: deleteSubscription, ids: ids, client: client}
		return nil
	}
	return errors.New("couldn't find ids property")
}

//
// Message Helper Functions
//

// send a message to one or more clients
func send(msg []byte, clients ...*Client) {
	if len(clients) > 0 {
		clients[0].msg_chan <- client_msg{bytes: msg, clients: clients[1:]}
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
// External Message Templates
//

type errorResponse struct {
	response string
	token    string
	error    string
}

type PingRequest struct {
	Request string `json:"request"`
	Token   string `json:"token"`
}

type PingResponse struct {
	Response string `json:"response"`
	Token    string `json:"token"`
}

//
// Internal Message Templates
//

type subscription_request struct {
	client *Client
	verb   int
	ids    []string
}

const (
	addSubscription    = int(iota)
	deleteSubscription = int(iota)
)

// client_msg contains a single message for one or more clients.
type client_msg struct {
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

func handleCouch2(w http.ResponseWriter, req *http.Request) {
	fmt.Println("***here***")
}

func handleCouch(w http.ResponseWriter, req *http.Request) {
	fmt.Printf("handleCouch %s\r\n", req.URL)
	remoteURL := "http://localhost:5984" + req.URL.Path
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

type DictionaryRootResponse struct {
	Response string       `json:"response"`
	Session  string       `json:"session"`
	Packets  []PacketJSON `json:"packets"`
}

type PacketJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	APID        int    `json:"apid"`
	Description string `json:"description"`
}

type PointJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	APID        int    `json:"apid"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Units       string `json:"units"`
	Conversion  string `json:"conversion"`
}

type PacketDictionaryResponse struct {
	Response string       `json:"response"`
	Session  string       `json:"session"`
	Packet   string       `json:"packet"`
	Packets  []PointJSON2 `json:"points"`
}

type PointJSON2 struct {
	Name   string                `json:"name"`
	Key    string                `json:"key"`
	Values []PointValuesTemplate `json:"values"`
}

type PointValuesTemplate struct {
	Key    string      `json:"key"`
	Source string      `json:"source"`
	Name   string      `json:"name"`
	Format string      `json:"format"`
	Hints  interface{} `json:"hints"`
}

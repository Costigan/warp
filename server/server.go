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
	"net/url"
	"strings"

	"github.com/Costigan/warp/ccsds"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/koding/websocketproxy"
)

//
// WarpServer
//

// WarpServer handles realtime and history connections to multiple clients
type WarpServer struct {
	Host string
	Port int

	StaticFiles string

	// registered warp clients
	clients map[*Client]bool

	// Inbound packets from the realtime stream
	realtimePackets chan ccsds.Packet

	SessionName string // ignored for now
	Session     *WarpSession
}

// Server is the object holding most server state
var Server *WarpServer

// Run runs a web server
func (server *WarpServer) Run() {

	// Ugh.  How are you supposed to pass state to the handlers?  Should I use closures?
	Server = server

	// For now, build in the session name
	server.Session = &WarpSession{Name: "demo"}
	if err := server.Session.loadDictionary(); err != nil {
		fmt.Println(err)
		return
	}

	router := mux.NewRouter()

	// REST (order matters)
	dictionarySubrouter := router.PathPrefix("/dictionary").Subrouter()

	//	dictionarySubrouter.HandleFunc("/{session}/id/{id}", handleDictionaryGetID).Methods("GET")
	//	dictionarySubrouter.HandleFunc("/{session}/root", handleDictionaryRoot).Methods("GET")
	//	dictionarySubrouter.HandleFunc("/{session}", handleWholeDictionary).Methods("GET")

	dictionarySubrouter.HandleFunc("/{session}/id/{id}", handleHTTP).Methods("GET")
	dictionarySubrouter.HandleFunc("/{session}/root", handleHTTP).Methods("GET")
	dictionarySubrouter.HandleFunc("/{session}", handleHTTP).Methods("GET")

	//	router.HandleFunc("/history", handleHTTP).Methods("GET")

	// WebSocket
	//	router.HandleFunc("/realtime/", handleRealtime).Methods("GET")

	proxyURL := "ws://:41402"
	u, err := url.Parse(proxyURL)
	if err != nil {
		log.Fatal(fmt.Sprintf("Error parsing url(%v):%v", proxyURL, err))
	}
	router.PathPrefix("/realtime").Handler(websocketproxy.NewProxy(u))

	// Files
	router.PathPrefix("/").Handler(http.StripPrefix("/", http.FileServer(http.Dir(server.StaticFiles))))

	// Initialization
	//	if server.Host == "" {
	//		server.Host = "localhost"
	//	}
	if server.Port == 0 {
		server.Port = 8000
	}

	listenString := fmt.Sprintf("%s:%d", server.Host, server.Port)
	fmt.Printf("listenString=%s\r\n", listenString)

	log.Fatal(http.ListenAndServe(listenString, router))
}

//
// WarpSession
//

// WarpSession holds session information.  Note that a warp process can host only a single session
type WarpSession struct {
	Name           string
	Dictionary     *ccsds.TelemetryDictionary
	DictionaryRoot *dictionaryJSON
}

func (session *WarpSession) loadDictionary() error {
	filename := "C:/git/github/warp-server-legacy/src/StaticFiles/rp.dictionary.json.gz"
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

//
// REST Handlers
//

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

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func handleWholeDictionary(w http.ResponseWriter, r *http.Request) {
	prepareHeader(w, r)
}

func handleDictionaryRoot(w http.ResponseWriter, r *http.Request) {
	fmt.Println("in handleDictionaryRoot")
	prepareHeader(w, r)
	json.NewEncoder(w).Encode(Server.Session.DictionaryRoot)
}

func handleDictionaryGetID(w http.ResponseWriter, r *http.Request) {
	prepareHeader(w, r)
	vars := mux.Vars(r)
	id := vars["id"]
	fmt.Printf("in handleDictionaryGetID: id=%s\r\n", id)
	if strings.Contains(id, ".") {
		// We're asking for a point
		if pt, ok := Server.Session.Dictionary.GetPointByID(id); ok {
			writePointJSON(w, pt, Server.Session.Dictionary)
		} else {
			http.Error(w, fmt.Sprintf("can't find point %s", id), 404)
		}
	} else {
		// We're asking for a packet
		if pkt, ok := Server.Session.Dictionary.GetPacketByID(id); ok {
			writePacketJSON(w, pkt, Server.Session)
		} else {
			http.NotFound(w, r)
		}
	}
}

func prepareHeader(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Add("Content-Type", "application/json")
}

// Sample output for a packet.  The points array is a list json objects like the point sample below
// {
//   "response": "list_points",
//   "session": "demo",
//   "packet": "EPSIO_BIT",
//   "points": []
// }

func writePacketJSON(w http.ResponseWriter, pkt *ccsds.PacketInfo, session *WarpSession) {
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
	fmt.Fprint(w,  pt.Name)
	fmt.Fprint(w, `","key":"`)
	fmt.Fprint(w, pt.ID)
	fmt.Fprint(w, `", "values": [{"key":"utc","source":"timestamp","name":"Timestamp","format":"utc","hints":{"x":1}},{"key":"value","name":"Value","hints":{"y":1},"format":"`)
	fmt.Fprint(w, typestring)
	fmt.Fprint(w, `"}]}`)
}

//
// WebSocket Handlers
//

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 16384,
}

func handleRealtime(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received /realtime request.  Upgrading ...")
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			log.Println(err)
			return
		}

		fmt.Println("Got a websocket message")

		if err := conn.WriteMessage(messageType, p); err != nil {
			log.Println(err)
			return
		}
	}
}

//
// Warp Server
//

// Client is the middleman between the websocket connection and the server
type Client struct {
	server *WarpServer
	conn   *websocket.Conn
	send   chan []byte
}

//
//
//

type dictionaryJSON struct {
	Response string       `json:"response"`
	Session  string       `json:"session"`
	Packets  []packetJSON `json:"packets"`
}

type packetJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	APID        int    `json:"apid"`
	Description string `json:"description"`
}

type pointJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	APID        int    `json:"apid"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Units       string `json:"units"`
	Conversion  string `json:"conversion"`
}

var dictionaryRootJSON dictionaryJSON

func makeDictionaryRoot(dictionary *ccsds.TelemetryDictionary) *dictionaryJSON {
	packets := make([]packetJSON, 0, len(dictionary.Packets))
	for _, p := range dictionary.Packets {
		p1 := packetJSON{ID: p.ID, Name: p.Name, APID: p.APID, Description: p.Documentation}
		packets = append(packets, p1)
	}
	return &dictionaryJSON{Response: "list_packets", Session: "demo", Packets: packets}
}

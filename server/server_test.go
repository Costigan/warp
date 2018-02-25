package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPing(t *testing.T) {
	withRunningServer(t, 8000, func(server *Server) {
		t.Log("Opening connection to server now")
		u := url.URL{Scheme: "ws", Host: fmt.Sprintf("%s:%d", "localhost", server.Port), Path: "/realtime/"}
		c, ok := getWebsocketConnection(t, u)
		if !ok {
			return
		}
		if !localSendJSON(t, c, PingRequest{Request: "ping", Token: "t1"}) {
			t.Errorf("Error sending ping\n")
			return
		}
		_, bytes, err := c.ReadMessage()
		if err != nil {
			t.Errorf("Error receiving ping reply: %v", err)
			return
		}
		var msg PingResponse
		err = json.Unmarshal(bytes, &msg)
		if err != nil {
			t.Errorf("Error unmarshalling ping reply: %v", err)
		}
	})
}

func withRunningServer(t *testing.T, port int, f func(server *Server)) error {
	wg := sync.WaitGroup{}
	wg.Add(1)

	// Start the server
	server := Server{
		Host:        "",
		Port:        8000,
		StaticFiles: "../../../../../projects/warp_data/dist/"}
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

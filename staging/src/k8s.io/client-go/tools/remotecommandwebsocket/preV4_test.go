package remotecommandwebsocket

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testServer struct {
	httpServer *http.Server
	t          *testing.T
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func TestPreV4Binary(t *testing.T) {
	t.Log("here")
	m := http.NewServeMux()

	s := &testServer{
		httpServer: &http.Server{Addr: "127.0.0.1:8765", Handler: m},
		t:          t,
	}

	m.HandleFunc("/wsbinary", s.wsBinary)

	go s.httpServer.ListenAndServe()

	time.Sleep(2 * time.Second)

	conn, resp, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8765/wsbinary", nil)

	if err != nil {
		panic(err)
	}

	t.Log(conn)
	t.Log(resp)

	stdinin, stdinout := io.Pipe()
	stdoutin, stdoutout := io.Pipe()
	stderrin, stderrout := io.Pipe()

	streamOptions := StreamOptions{
		Stdin:             stdinin,
		Stdout:            stdoutout,
		Stderr:            stderrout,
		Tty:               false,
		TerminalSizeQueue: nil,
	}

	streamer := newPreV4BinaryProtocol(streamOptions)
	go streamer.stream(conn)
}

func (s *testServer) wsBinary(w http.ResponseWriter, r *http.Request) {
	s.t.Log("in handler")

	ws, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		panic(err)
	}

	s.t.Log(ws)

	go s.httpServer.Shutdown(context.Background())
}

package remotecommandwebsocket

import (
	"context"
	"net/http"
	"testing"
)

type testServer struct {
	httpServer *http.Server
	t          *testing.T
}

func TestPreV4Binary(t *testing.T) {
	t.Log("here")
	m := http.NewServeMux()

	s := &testServer{
		httpServer: &http.Server{Addr: "127.0.0.1:8765", Handler: m},
		t:          t,
	}

	m.HandleFunc("/wsbinary", s.wsBinary)

	s.httpServer.ListenAndServe()
}

func (s *testServer) wsBinary(w http.ResponseWriter, r *http.Request) {
	s.t.Log("in handler")

	ws, err := upgrader.Upgrade(w, r, nil)

	go s.httpServer.Shutdown(context.Background())
}

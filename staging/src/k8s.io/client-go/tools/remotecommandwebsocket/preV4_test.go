package remotecommandwebsocket

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testServer struct {
	httpServer    *http.Server
	t             *testing.T
	wg            *sync.WaitGroup
	streamOptions *StreamOptions

	stdinPassed  bool
	stdoutPassed bool
	stderrPassed bool
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

const (
	stdinTestData  = "this is a \ntest\n"
	stdOutTestData = "this\nis\n from \r\n stdanard out \n"
	stdErrTestData = "this\nis\n \t\tfrom \r\n stdanard err \n"
)

func TestPreV4Binary(t *testing.T) {
	t.Log("here")
	m := http.NewServeMux()

	s := &testServer{
		httpServer:   &http.Server{Addr: "127.0.0.1:8765", Handler: m},
		t:            t,
		wg:           &sync.WaitGroup{},
		stderrPassed: false,
		stdinPassed:  false,
		stdoutPassed: false,
	}

	stdinin, stdinout := io.Pipe()
	_, stdoutout := io.Pipe()
	_, stderrout := io.Pipe()

	s.streamOptions = &StreamOptions{
		Stdin:             stdinin,
		Stdout:            stdoutout,
		Stderr:            stderrout,
		Tty:               false,
		TerminalSizeQueue: nil,
	}

	m.HandleFunc("/wsbinary", s.wsBinary)

	go s.httpServer.ListenAndServe()

	time.Sleep(2 * time.Second)

	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8765/wsbinary", nil)

	if err != nil {
		panic(err)
	}

	streamer := newPreV4BinaryProtocol(*s.streamOptions)
	go streamer.stream(conn)

	// write to standard in
	stdinbuf := bufio.NewWriter(stdinout)
	bytes, err := stdinbuf.Write([]byte(stdinTestData))
	t.Log(bytes)
	if err != nil {
		panic(err)
	}
	err = stdinbuf.Flush()
	if err != nil {
		panic(err)
	}

	if s.streamOptions.Stdout != nil || s.streamOptions.Stderr != nil {
		go s.writePump(conn)
	}

	s.wg.Wait()

	go s.httpServer.Shutdown(context.Background())

	if s.streamOptions.Stdin != nil && !s.stdinPassed {
		t.Fail()
	}

	if s.streamOptions.Stdout != nil && !s.stdoutPassed {
		t.Fail()
	}

	if s.streamOptions.Stderr != nil && !s.stderrPassed {
		t.Fail()
	}
}

func (s *testServer) wsBinary(w http.ResponseWriter, r *http.Request) {
	s.t.Log("in handler")

	ws, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		panic(err)
	}

	if s.streamOptions.Stdin != nil {
		go s.readPump(ws)
	}

	if s.streamOptions.Stdout != nil {
		s.wg.Add(1)
		var data []byte
		data = append(data, StreamStdOut)
		data = append(data, []byte(stdOutTestData)...)
		ws.WriteMessage(websocket.BinaryMessage, data)
	}

	if s.streamOptions.Stderr != nil {
		s.wg.Add(1)
		var data []byte
		data = append(data, StreamStdErr)
		data = append(data, []byte(stdErrTestData)...)
		ws.WriteMessage(websocket.BinaryMessage, data)
	}

	//go s.httpServer.Shutdown(context.Background())
}

func (s *testServer) writePump(ws *websocket.Conn) {
	//w.Write("here3\n")
	//defer ws.Close()
	//ws.SetReadLimit(maxMessageSize)
	//ws.SetReadDeadline(time.Now().Add(pongWait))
	//ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		//fmt.Println("waiting message")
		messageType, message, err := ws.ReadMessage()

		if messageType > 0 {
			if len(message) > 0 {
				if message[0] == StreamStdOut {
					//on the standard out stream, make sure the message matches
					mesageAsString := string(message[1:])
					if mesageAsString == stdOutTestData {
						s.stdoutPassed = true
						s.t.Log("Validated Standard Out")
					} else {
						s.t.Log("Invalid std out message '" + mesageAsString + "'")
						s.t.Fail()
					}

					s.wg.Done()

				} else if message[0] == StreamStdErr {
					//on the standard out stream, make sure the message matches
					mesageAsString := string(message[1:])
					if mesageAsString == stdErrTestData {
						s.stderrPassed = true
						s.t.Log("Validated Standard Err")
					} else {
						s.t.Log("Invalid std err message '" + mesageAsString + "'")
						s.t.Fail()
					}

					s.wg.Done()

				} else {
					s.t.Log("Unknown Steam : ")
				}
			} else {
				s.t.Log("Empty message")
			}

		}

		if err != nil {
			panic(err)
			break
		}
		/*message = append(message, '\n')
		if _, err := w.Write(message); err != nil {
			break
		}*/
	}
}

func (s *testServer) readPump(conn *websocket.Conn) {
	defer func() {
		s.wg.Done()
		//conn.Close()
	}()
	s.wg.Add(1)
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		fmt.Println("waiting for input")
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				panic(err)
			}
			break
		}

		// make sure the message starts with 0 (stdin)
		if message[0] != StreamStdIn {
			s.t.FailNow()
		}

		messageAfterStream := message[1:]
		messageAsString := string(messageAfterStream)

		// check the message didn't change
		if messageAsString != stdinTestData {
			s.t.FailNow()
		}

		s.stdinPassed = true

		s.t.Log(messageAsString)
		break
		//c.hub.broadcast <- message
	}
}

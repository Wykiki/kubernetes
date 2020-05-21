package remotecommandwebsocket

import (
	"bufio"
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

	wsClient *websocket.Conn
	wsServer *websocket.Conn

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

	m := http.NewServeMux()
	s := &testServer{
		httpServer:   &http.Server{Addr: "127.0.0.1:8765", Handler: m},
		t:            t,
		wg:           &sync.WaitGroup{},
		stderrPassed: false,
		stdinPassed:  false,
		stdoutPassed: false,
	}
	m.HandleFunc("/wsbinary", s.wsBinary)

	go s.httpServer.ListenAndServe()

	time.Sleep(2 * time.Second)

	runTestCase(t, true, true, true, s)
	runTestCase(t, true, true, false, s)
	runTestCase(t, true, false, true, s)
	/*runTestCase(t, false, true, true, s)
	runTestCase(t, false, false, true, s)
	runTestCase(t, true, false, false, s)
	runTestCase(t, false, true, false, s)
	runTestCase(t, false, false, false, s)*/

}

func runTestCase(t *testing.T, runStdIn bool, runStdOut bool, runStdErr bool, s *testServer) {
	t.Logf("Test Case - stdin : %t / stdout: %t / stederr : %t", runStdIn, runStdOut, runStdErr)

	doneChan := make(chan struct{}, 2)

	s.stdinPassed = false
	s.stdoutPassed = false
	s.stderrPassed = false

	var stdinin io.Reader
	var stdoutout, stderrout, stdinout io.Writer

	if runStdIn {
		stdinin, stdinout = io.Pipe()
	}

	if runStdOut {
		_, stdoutout = io.Pipe()
	}

	if runStdErr {
		_, stderrout = io.Pipe()
	}

	s.streamOptions = &StreamOptions{
		Stdin:             stdinin,
		Stdout:            stdoutout,
		Stderr:            stderrout,
		Tty:               false,
		TerminalSizeQueue: nil,
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8765/wsbinary", nil)
	s.wsClient = conn
	if err != nil {
		panic(err)
	}

	streamer := newPreV4BinaryProtocol(*s.streamOptions)
	go streamer.stream(conn)

	if s.streamOptions.Stdin != nil {
		// write to standard in
		stdinbuf := bufio.NewWriter(stdinout)
		_, err = stdinbuf.Write([]byte(stdinTestData))

		if err != nil {
			panic(err)
		}
		err = stdinbuf.Flush()
		if err != nil {
			panic(err)
		}
	}

	if s.streamOptions.Stdout != nil || s.streamOptions.Stderr != nil {
		go s.writePump(conn, doneChan)
	}

	//defer s.wsServer.Close()
	//defer s.wsClient.Close()

	t.Log("Waiting for wait group do be done")
	s.wg.Wait()
	t.Log("Waiting  done")

	if s.streamOptions.Stdin != nil && !s.stdinPassed {
		t.Log("Stdin not passed")
		t.Fail()
	}

	if s.streamOptions.Stdout != nil && !s.stdoutPassed {
		t.Log("Stdout not passed")
		t.Fail()
	}

	if s.streamOptions.Stderr != nil && !s.stderrPassed {
		t.Log("Stderr not passed")
		t.Fail()
	}

	t.Log("Ending test")

}

func (s *testServer) wsBinary(w http.ResponseWriter, r *http.Request) {

	ws, err := upgrader.Upgrade(w, r, nil)
	s.wsServer = ws
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

	//emptyMessage := make([]byte, 0)
	//ws.WriteMessage(WebSocketExitStream, emptyMessage)

}

func (s *testServer) writePump(ws *websocket.Conn, done chan struct{}) {

	s.t.Log("Starting new writePump")

	numMsgsWait := 0
	if s.streamOptions.Stdout != nil {
		numMsgsWait++
	}

	if s.streamOptions.Stderr != nil {
		numMsgsWait++
	}

	for {

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

					s.t.Logf("Num messages %d", numMsgsWait)

					numMsgsWait--
					if numMsgsWait == 0 {
						s.t.Log("Exiting Read")
						return
					}

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

					s.t.Logf("Num messages %d", numMsgsWait)

					numMsgsWait--
					if numMsgsWait == 0 {
						s.t.Log("Exiting Read")
						return
					}
				} else {
					s.t.Log("Unknown Steam : ")
				}
			} else {
				s.t.Log("Empty message")
			}

		}

		if err != nil {
			websocketErr, ok := err.(*websocket.CloseError)
			if ok {
				if websocketErr.Code == WebSocketExitStream {
					return
				} else {
					panic(err)
				}
			} else {
				//panic(err)
				return
			}
		}

	}

}

func (s *testServer) readPump(conn *websocket.Conn) {
	defer func() {
		s.wg.Done()

	}()
	s.wg.Add(1)
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
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

		s.t.Log("Std in passed")
		s.stdinPassed = true
		break

	}
}

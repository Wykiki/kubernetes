/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package remotecommandwebsocket

import (
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/apimachinery/pkg/util/runtime"
)

// streamProtocolV4 implements version 4 of the streaming protocol for attach
// and exec.
type streamProtocolV4 struct {
	StreamOptions

	binary bool

	errorStreamIn  *io.PipeReader
	errorStreamOut *io.PipeWriter

	remoteStdinIn  *io.PipeReader
	remoteStdinOut *io.PipeWriter

	remoteStdoutIn  *io.PipeReader
	remoteStdoutOut *io.PipeWriter

	remoteStderrIn  *io.PipeReader
	remoteStderrOut *io.PipeWriter

	resizeTerminalIn  *io.PipeReader
	resizeTerminalOut *io.PipeWriter
}

var _ streamProtocolHandler = &streamProtocolV4{}

func newBinaryV4(options StreamOptions) streamProtocolHandler {
	return &streamProtocolV4{
		StreamOptions: options,
		binary:        true,
	}
}

func newBase64V4(options StreamOptions) streamProtocolHandler {
	return &streamProtocolV4{
		StreamOptions: options,
		binary:        false,
	}
}

func (p *streamProtocolV4) copyStdin() {
	if p.Stdin != nil {
		var once sync.Once

		// copy from client's stdin to container's stdin
		go func() {
			defer runtime.HandleCrash()

			// if p.stdin is noninteractive, p.g. `echo abc | kubectl exec -i <pod> -- cat`, make sure
			// we close remoteStdin as soon as the copy from p.stdin to remoteStdin finishes. Otherwise
			// the executed command will remain running.
			defer once.Do(func() { p.remoteStdinIn.Close() })

			if _, err := io.Copy(p.remoteStdinOut, readerWrapper{p.Stdin}); err != nil {
				runtime.HandleError(err)
			}
		}()

		// read from remoteStdin until the stream is closed. this is essential to
		// be able to exit interactive sessions cleanly and not leak goroutines or
		// hang the client's terminal.
		//
		// TODO we aren't using go-dockerclient any more; revisit this to determine if it's still
		// required by engine-api.
		//
		// go-dockerclient's current hijack implementation
		// (https://github.com/fsouza/go-dockerclient/blob/89f3d56d93788dfe85f864a44f85d9738fca0670/client.go#L564)
		// waits for all three streams (stdin/stdout/stderr) to finish copying
		// before returning. When hijack finishes copying stdout/stderr, it calls
		// Close() on its side of remoteStdin, which allows this copy to complete.
		// When that happens, we must Close() on our side of remoteStdin, to
		// allow the copy in hijack to complete, and hijack to return.
		go func() {
			defer runtime.HandleCrash()
			defer once.Do(func() { p.remoteStdinIn.Close() })

			// this "copy" doesn't actually read anything - it's just here to wait for
			// the server to close remoteStdin.
			if _, err := io.Copy(ioutil.Discard, p.remoteStdinIn); err != nil {
				runtime.HandleError(err)
			}
		}()
	}
}

func (p *streamProtocolV4) copyStdout(wg *sync.WaitGroup) {
	if p.Stdout == nil {
		return
	}

	wg.Add(1)
	go func() {
		defer runtime.HandleCrash()
		defer wg.Done()

		if _, err := io.Copy(p.Stdout, p.remoteStdoutIn); err != nil {
			runtime.HandleError(err)
		}
	}()
}

func (p *streamProtocolV4) copyStderr(wg *sync.WaitGroup) {
	if p.Stderr == nil || p.Tty {
		return
	}

	wg.Add(1)
	go func() {
		defer runtime.HandleCrash()
		defer wg.Done()

		if _, err := io.Copy(p.Stderr, p.remoteStderrIn); err != nil {
			runtime.HandleError(err)
		}
	}()
}

func (p *streamProtocolV4) stream(conn *websocket.Conn) error {

	defer conn.Close()
	doneChan := make(chan struct{}, 2)

	// set up error stream
	p.errorStreamIn, p.errorStreamOut = io.Pipe()

	// set up stdin stream
	if p.Stdin != nil {
		p.remoteStdinIn, p.remoteStdinOut = io.Pipe()
	}

	// set up stdout stream
	if p.Stdout != nil {
		p.remoteStdoutIn, p.remoteStdoutOut = io.Pipe()
	}

	// set up stderr stream
	if p.Stderr != nil && !p.Tty {
		p.remoteStderrIn, p.remoteStderrOut = io.Pipe()
	}

	// set up resize stream
	if p.Tty {
		p.resizeTerminalIn, p.resizeTerminalOut = io.Pipe()
	}

	// now that all the streams have been created, proceed with reading & copying

	errorChan := watchErrorStream(p.errorStreamIn, &errorDecoderV4{})

	p.copyStdin()

	var wg sync.WaitGroup
	p.copyStdout(&wg)
	p.copyStderr(&wg)

	//start streaming to the api server
	p.pullFromWebSocket(conn, &wg)
	p.pushToWebSocket(conn, &wg)
	//p.ping(conn, doneChan)

	// we're waiting for stdout/stderr to finish copying
	wg.Wait()

	// waits for errorStream to finish reading with an error or nil

	// notify the ping function to stop
	doneChan <- struct{}{}

	return <-errorChan
}

// errorDecoderV4 interprets the error channel data as plain text.
type errorDecoderV4 struct{}

func (d *errorDecoderV4) decode(message []byte) error {
	return fmt.Errorf("error executing remote command: %s", message)
}

func (p *streamProtocolV4) pushToWebSocket(conn *websocket.Conn, wg *sync.WaitGroup) {
	if p.Stdin == nil {
		return
	}
	wg.Add(1)

	go func() {
		defer runtime.HandleCrash()
		defer wg.Done()

		buffer := make([]byte, 1024)

		for {

			numberOfBytesRead, err := p.StreamOptions.Stdin.Read(buffer)
			if err != nil {
				if err == io.EOF {
					return
				} else {
					runtime.HandleError(err)
				}
			}

			var data []byte

			if p.binary {
				data = make([]byte, numberOfBytesRead+1)
				copy(data[1:], buffer[:])
				data[0] = StreamStdIn
			} else {
				enc := base64.StdEncoding.EncodeToString(buffer[0:numberOfBytesRead])
				data = append([]byte{'0'}, []byte(enc)...)
			}

			err = conn.WriteMessage(websocket.BinaryMessage, data)
			if err != nil {
				runtime.HandleError(err)
			}

		}
	}()

}

func (p *streamProtocolV4) pullFromWebSocket(conn *websocket.Conn, wg *sync.WaitGroup) {
	wg.Add(1)

	go func() {
		defer runtime.HandleCrash()
		defer wg.Done()
		conn.SetReadLimit(maxMessageSize)
		conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
		buffer := make([]byte, 1024)
		for {
			messageType, message, err := conn.ReadMessage()

			if messageType > 0 {

				if p.binary {
					if len(message) > 0 {
						switch message[0] {
						case StreamStdOut:

							if _, err := p.remoteStdoutOut.Write(message[1:]); err != nil {
								runtime.HandleError(err)
							}
						case StreamStdErr:
							if _, err := p.remoteStderrOut.Write(message[1:]); err != nil {
								runtime.HandleError(err)
							}
						case StreamErr:
							if _, err := p.errorStreamOut.Write(message[1:]); err != nil {
								runtime.HandleError(err)
							}
						}
					}
				} else {
					if len(message) > 0 {
						numBytes, err := base64.StdEncoding.Decode(buffer, message[1:])
						if err != nil {
							runtime.HandleError(err)
						}

						switch message[0] {
						case Base64StreamStdOut:

							//fmt.Println(buffer)
							if _, err := p.remoteStdoutOut.Write(buffer[1:numBytes]); err != nil {
								runtime.HandleError(err)
							}
						case Base64StreamStdErr:
							if _, err := p.remoteStderrOut.Write(buffer[1:numBytes]); err != nil {
								runtime.HandleError(err)
							}
						case Base64StreamErr:
							if _, err := p.errorStreamOut.Write(buffer[1:numBytes]); err != nil {
								runtime.HandleError(err)
							}
						}
					}
				}

			}

			if err != nil {
				websocketErr, ok := err.(*websocket.CloseError)
				if ok {
					if websocketErr.Code == WebSocketExitStream {
						return
					} else {
						runtime.HandleError(err)
					}
				} else {
					runtime.HandleError(err)
				}
			}
		}
	}()
}

func (p *streamProtocolV4) ping(ws *websocket.Conn, done chan struct{}) {
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
					runtime.HandleError(err)
				}
			case <-done:
				return
			}
		}
	}()
}

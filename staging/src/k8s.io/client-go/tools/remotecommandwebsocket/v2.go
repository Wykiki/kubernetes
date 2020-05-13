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
	"fmt"
	"io"
	"io/ioutil"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/apimachinery/pkg/util/runtime"
)

// streamProtocolV2 implements version 2 of the streaming protocol for attach
// and exec. The original streaming protocol was metav1. As a result, this
// version is referred to as version 2, even though it is the first actual
// numbered version.
type streamProtocolV2 struct {
	StreamOptions

	errorStreamIn  *io.PipeReader
	errorStreamOut *io.PipeWriter

	remoteStdinIn  *io.PipeReader
	remoteStdinOut *io.PipeWriter

	remoteStdoutIn  *io.PipeReader
	remoteStdoutOut *io.PipeWriter

	remoteStderrIn  *io.PipeReader
	remoteStderrOut *io.PipeWriter
}

var _ streamProtocolHandler = &streamProtocolV2{}

func newStreamProtocolV2(options StreamOptions) streamProtocolHandler {
	return &streamProtocolV2{
		StreamOptions: options,
	}
}

func (p *streamProtocolV2) copyStdin() {
	if p.Stdin != nil {
		var once sync.Once

		// copy from client's stdin to container's stdin
		go func() {
			defer runtime.HandleCrash()

			// if p.stdin is noninteractive, p.g. `echo abc | kubectl exec -i <pod> -- cat`, make sure
			// we close remoteStdin as soon as the copy from p.stdin to remoteStdin finishes. Otherwise
			// the executed command will remain running.
			defer once.Do(func() { p.remoteStdin.Close() })

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
			defer once.Do(func() { p.remoteStdin.Close() })

			// this "copy" doesn't actually read anything - it's just here to wait for
			// the server to close remoteStdin.
			if _, err := io.Copy(ioutil.Discard, p.remoteStdinIn); err != nil {
				runtime.HandleError(err)
			}
		}()
	}
}

func (p *streamProtocolV2) copyStdout(wg *sync.WaitGroup) {
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

func (p *streamProtocolV2) copyStderr(wg *sync.WaitGroup) {
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

func (p *streamProtocolV2) stream(conn *websocket.Conn) error {
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

	// now that all the streams have been created, proceed with reading & copying

	errorChan := watchErrorStream(p.errorStream, &errorDecoderV2{})

	p.copyStdin()

	var wg sync.WaitGroup
	p.copyStdout(&wg)
	p.copyStderr(&wg)

	// we're waiting for stdout/stderr to finish copying
	wg.Wait()

	// waits for errorStream to finish reading with an error or nil
	return <-errorChan
}

// errorDecoderV2 interprets the error channel data as plain text.
type errorDecoderV2 struct{}

func (d *errorDecoderV2) decode(message []byte) error {
	return fmt.Errorf("error executing remote command: %s", message)
}

func (p *streamProtocolV1) pullFromWebSocket(conn *websocket.Conn, wg *sync.WaitGroup) {
	wg.Add(1)

	go func() {
		defer runtime.HandleCrash()
		defer wg.Done()
		conn.SetReadLimit(maxMessageSize)
		conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
		for {
			messageType, message, err := conn.ReadMessage()

			if messageType > 0 {

				switch message[0] {
				case StreamStdOut:

					if _, err := p.remoteStdoutOut.Write(message[1:]); err != nil {
						panic(err)
					}
				case StreamStdErr:
					if _, err := p.remoteStderrOut.Write(message[1:]); err != nil {
						panic(err)
					}
				case StreamErr:
					if _, err := p.errorStreamOut.Write(message[1:]); err != nil {
						panic(err)
					}
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
					panic(err)
				}
			}
		}
	}
}

func (p *streamProtocolV1) ping(ws *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
				panic(err)
			}
		case <-done:
			return
		}
	}
}

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
	"time"

	"github.com/gorilla/websocket"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 8192

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Time to wait before force close on connection.
	closeGracePeriod = 10 * time.Second
)

// streamProtocolV1 implements the first version of the streaming exec & attach
// protocol. This version has some bugs, such as not being able to detect when
// non-interactive stdin data has ended. See http://issues.k8s.io/13394 and
// http://issues.k8s.io/13395 for more details.
type streamProtocolV1 struct {
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

var _ streamProtocolHandler = &streamProtocolV1{}

func newStreamProtocolV1(options StreamOptions) streamProtocolHandler {
	return &streamProtocolV1{
		StreamOptions: options,
	}
}

func (p *streamProtocolV1) stream(conn *websocket.Conn) error {
	doneChan := make(chan struct{}, 2)
	errorChan := make(chan error)

	cp := func(s string, dst io.Writer, src io.Reader) {
		klog.V(6).Infof("Copying %s", s)
		defer klog.V(6).Infof("Done copying %s", s)
		if _, err := io.Copy(dst, src); err != nil && err != io.EOF {
			klog.Errorf("Error copying %s: %v", s, err)
		}
		if s == v1.StreamTypeStdout || s == v1.StreamTypeStderr {
			doneChan <- struct{}{}
		}
	}

	// set up all the streams first
	p.errorStreamIn, p.errorStreamOut = io.Pipe()

	//defer p.errorStreamIn.

	// Create all the streams first, then start the copy goroutines. The server doesn't start its copy
	// goroutines until it's received all of the streams. If the client creates the stdin stream and
	// immediately begins copying stdin data to the server, it's possible to overwhelm and wedge the
	// spdy frame handler in the server so that it is full of unprocessed frames. The frames aren't
	// getting processed because the server hasn't started its copying, and it won't do that until it
	// gets all the streams. By creating all the streams first, we ensure that the server is ready to
	// process data before the client starts sending any. See https://issues.k8s.io/16373 for more info.
	if p.Stdin != nil {
		p.remoteStdinIn, p.remoteStdinOut = io.Pipe()

		//defer p.remoteStdin.Reset()
	}

	if p.Stdout != nil {
		p.remoteStdoutIn, p.remoteStdoutOut = io.Pipe()

		//defer p.remoteStdout.Reset()
	}

	if p.Stderr != nil && !p.Tty {

		p.remoteStderrIn, p.remoteStderrOut = io.Pipe()

		//defer p.remoteStderr.Reset()
	}

	// now that all the streams have been created, proceed with reading & copying

	// always read from errorStream
	go func() {
		message, err := ioutil.ReadAll(p.errorStreamIn)
		if err != nil && err != io.EOF {
			errorChan <- fmt.Errorf("Error reading from error stream: %s", err)
			return
		}
		if len(message) > 0 {
			errorChan <- fmt.Errorf("Error executing remote command: %s", message)
			return
		}
	}()

	if p.Stdin != nil {
		// TODO this goroutine will never exit cleanly (the io.Copy never unblocks)
		// because stdin is not closed until the process exits. If we try to call
		// stdin.Close(), it returns no error but doesn't unblock the copy. It will
		// exit when the process exits, instead.
		go cp(v1.StreamTypeStdin, p.remoteStdinOut, readerWrapper{p.Stdin})
	}

	waitCount := 0
	completedStreams := 0

	if p.Stdout != nil {
		waitCount++
		go cp(v1.StreamTypeStdout, p.Stdout, p.remoteStdoutIn)
	}

	if p.Stderr != nil && !p.Tty {
		waitCount++
		go cp(v1.StreamTypeStderr, p.Stderr, p.remoteStderrIn)
	}

	// pipes are connected, begin handling messages
	go p.pullFromWebSocket(conn, doneChan)

Loop:
	for {
		select {
		case <-doneChan:
			completedStreams++
			if completedStreams == waitCount {
				break Loop
			}
		case err := <-errorChan:
			return err
		}
	}

	return nil
}

func (p *streamProtocolV1) pullFromWebSocket(conn *websocket.Conn, doneChan chan struct{}) {
	defer conn.Close()
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		messageType, message, err := conn.ReadMessage()

		if messageType > 0 {

			switch message[0] {
			case 1:

				if _, err := p.remoteStdoutOut.Write(message[1:]); err != nil {
					break
				}
			case 2:
				if _, err := p.remoteStderrOut.Write(message[1:]); err != nil {
					break
				}
			case 3:
				if _, err := p.errorStreamOut.Write(message[1:]); err != nil {
					break
				}
			}

		}

		if err != nil {
			doneChan <- struct{}{}
			return
		}
	}
}

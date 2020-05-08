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

package websocket

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/apimachinery/pkg/util/httpstream"
)

// RoundTripper stores dialer information and knows how
// to establish a connect to the remote websocket endpoint.  WebsocketRoundTripper
// implements the UpgradeRoundTripper interface.
type RoundTripper struct {
	http.RoundTripper
	//tlsConfig holds the TLS configuration settings to use when connecting
	//to the remote server.
	tlsConfig *tls.Config

	// websocket connection
	Conn *websocket.Conn

	// proxier knows which proxy to use given a request, defaults to http.ProxyFromEnvironment
	// Used primarily for mocking the proxy discovery in tests.
	proxier func(req *http.Request) (*url.URL, error)
}

// Connection holds the underlying websocket connection
type Connection struct {
	httpstream.Connection
	Conn *websocket.Conn
}

// CreateStream does nothing as httpstream.Stream is a SPDY function, not a websocket concept
func (connection *Connection) CreateStream(headers http.Header) (httpstream.Stream, error) {
	return nil, nil
}

// Close does nothing as httpstream.Stream is a SPDY function, not a websocket concept
func (connection *Connection) Close() error {
	return nil
}

// CloseChan does nothing as httpstream.Stream is a SPDY function, not a websocket concept
func (connection *Connection) CloseChan() <-chan bool {
	out := make(chan bool)
	return out
}

// SetIdleTimeout does nothing as httpstream.Stream is a SPDY function, not a websocket concept
func (connection *Connection) SetIdleTimeout(timeout time.Duration) {

}

// NewRoundTripper initializes the RoundTripper
func NewRoundTripper(tlsConfig *tls.Config) httpstream.UpgradeRoundTripper {
	return &RoundTripper{
		tlsConfig: tlsConfig,
	}
}

// NewConnection doesn't do anything right now
func (wsRoundTripper *RoundTripper) NewConnection(resp *http.Response) (httpstream.Connection, error) {
	return &Connection{Conn: wsRoundTripper.Conn}, nil
}

// RoundTrip connects to the remote websocket using the headers in the request and the TLS
// configuration from the config
func (wsRoundTripper *RoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {

	// set the protocol version directly on the dialer from the header
	protocolVersions := request.Header[httpstream.HeaderProtocolVersion]

	// there's no need for the headers for the protocol version anymore
	if protocolVersions != nil {
		request.Header.Del((httpstream.HeaderProtocolVersion))
	}

	// create a dialer
	// TODO: add proxy support
	dialer := websocket.Dialer{
		TLSClientConfig: wsRoundTripper.tlsConfig,
		Subprotocols:    protocolVersions,
	}

	wsCon, resp, err := dialer.Dial(request.URL.String(), request.Header)

	if err != nil {
		return nil, err
	}

	// for safe keeping
	wsRoundTripper.Conn = wsCon

	return resp, nil
}

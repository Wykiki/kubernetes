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
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
	"k8s.io/apimachinery/pkg/util/httpstream"
)

// RoundTripper stores dialer information and knows how
// to establish a connect to the remote websocket endpoint.  WebsocketRoundTripper
// implements the UpgradeRoundTripper interface.
type RoundTripper struct {
	//tlsConfig holds the TLS configuration settings to use when connecting
	//to the remote server.
	tlsConfig *tls.Config

	// websocket connection
	conn *websocket.Conn

	// knows how to dial the websocket server
	Dialer *websocket.Dialer

	// proxier knows which proxy to use given a request, defaults to http.ProxyFromEnvironment
	// Used primarily for mocking the proxy discovery in tests.
	proxier func(req *http.Request) (*url.URL, error)
}

func NewRoundTripper(tlsConfig *tls.Config) httpstream.UpgradeRoundTripper {
	return &RoundTripper{
		tlsConfig: tlsConfig,
	}
}

func (wsRoundTripper *RoundTripper) NewConnection(resp *http.Response) (httpstream.Connection, error) {
	return nil, nil
}

func (wsRoundTripper *RoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	fmt.Println(request.Header)
	return nil, nil
}

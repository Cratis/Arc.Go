// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// A tiny independent RFC fixture client exercises actual masked/fragmented wire
// frames, not the server's library encoder. It is not a production transport.
type wireWSClient struct {
	connection net.Conn
	reader     *bufio.Reader
}

func openWireWS(t *testing.T, server *httptest.Server, path string, headers http.Header) (*wireWSClient, *http.Response) {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest("GET", server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = http.Header{}
	}
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if err := r.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, r)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode == 101 && (response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" || response.Header.Get("Sec-WebSocket-Extensions") != "") {
		t.Fatal(response.Header)
	}
	return &wireWSClient{conn, reader}, response
}

func (c *wireWSClient) frame(t *testing.T, opcode byte, fin bool, body []byte) {
	t.Helper()
	first := opcode
	if fin {
		first |= 0x80
	}
	frame := []byte{first}
	switch {
	case len(body) < 126:
		frame = append(frame, 0x80|byte(len(body)))
	case len(body) <= 65535:
		frame = append(frame, 0x80|126, byte(len(body)>>8), byte(len(body)))
	default:
		t.Fatal("oversized test frame")
	}
	mask := [4]byte{1, 2, 3, 4}
	frame = append(frame, mask[:]...)
	for i, b := range body {
		frame = append(frame, b^mask[i%4])
	}
	if _, err := c.connection.Write(frame); err != nil {
		t.Fatal(err)
	}
}
func (c *wireWSClient) send(t *testing.T, message any) {
	t.Helper()
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	c.frame(t, 1, true, body)
}
func (c *wireWSClient) read(t *testing.T) (map[string]any, error) {
	t.Helper()
	var message []byte
	for {
		var head [2]byte
		if _, err := io.ReadFull(c.reader, head[:]); err != nil {
			return nil, err
		}
		if head[0]&0x70 != 0 || head[1]&0x80 != 0 {
			t.Fatal("compression/masking on server frame", head)
		}
		length := uint64(head[1] & 0x7f)
		if length == 126 {
			var b [2]byte
			if _, err := io.ReadFull(c.reader, b[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(b[:]))
		}
		if length == 127 {
			var b [8]byte
			if _, err := io.ReadFull(c.reader, b[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(b[:])
		}
		if length > 1<<20 {
			t.Fatal("unbounded test input", length)
		}
		body := make([]byte, int(length))
		if _, err := io.ReadFull(c.reader, body); err != nil {
			return nil, err
		}
		switch head[0] & 15 {
		case 8:
			c.frame(t, 8, true, body)
			return nil, io.EOF
		case 9:
			c.frame(t, 10, true, body)
			continue
		case 10:
			continue
		case 0, 1:
			message = append(message, body...)
		default:
			return nil, fmt.Errorf("unexpected server opcode %d", head[0]&15)
		}
		if head[0]&0x80 != 0 {
			break
		}
	}
	var result map[string]any
	if err := json.Unmarshal(message, &result); err != nil {
		return nil, err
	}
	return result, nil
}
func (c *wireWSClient) message(t *testing.T) map[string]any {
	t.Helper()
	message, err := c.read(t)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observables_test

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// This independent, bounded RFC fixture client is not a browser/JS client or a
// production WebSocket implementation. The runtime's dependency stays confined
// to internal/websockettransport, including in these contract tests.
type webSocket struct {
	conn   net.Conn
	reader *bufio.Reader
}

func openWebSocket(t *testing.T, endpoint string) *webSocket {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header = http.Header{
		"Connection": {"Upgrade"}, "Upgrade": {"websocket"},
		"Sec-Websocket-Version": {"13"}, "Sec-Websocket-Key": {"dGhlIHNhbXBsZSBub25jZQ=="},
	}
	if err := r.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, r)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" || response.Header.Get("Sec-WebSocket-Extensions") != "" {
		_ = response.Body.Close()
		t.Fatalf("WebSocket handshake: %d %v", response.StatusCode, response.Header)
	}
	return &webSocket{conn: conn, reader: reader}
}

func (c *webSocket) frame(opcode byte, body []byte) error {
	if len(body) > 65535 {
		return fmt.Errorf("test frame exceeds bound")
	}
	frame := []byte{0x80 | opcode}
	if len(body) < 126 {
		frame = append(frame, 0x80|byte(len(body)))
	} else {
		frame = append(frame, 0xfe, byte(len(body)>>8), byte(len(body)))
	}
	mask := []byte{1, 2, 3, 4}
	frame = append(frame, mask...)
	for i, value := range body {
		frame = append(frame, value^mask[i%4])
	}
	n, err := c.conn.Write(frame)
	if err == nil && n != len(frame) {
		return io.ErrShortWrite
	}
	return err
}

func (c *webSocket) send(t *testing.T, message any) {
	t.Helper()
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.frame(1, body); err != nil {
		t.Fatal(err)
	}
}

func (c *webSocket) read() (map[string]any, error) {
	var message []byte
	for {
		var header [2]byte
		if _, err := io.ReadFull(c.reader, header[:]); err != nil {
			return nil, err
		}
		if header[0]&0x70 != 0 || header[1]&0x80 != 0 {
			return nil, fmt.Errorf("server frame is compressed or masked")
		}
		length := uint64(header[1] & 0x7f)
		switch length {
		case 126:
			var size [2]byte
			if _, err := io.ReadFull(c.reader, size[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(size[:]))
		case 127:
			var size [8]byte
			if _, err := io.ReadFull(c.reader, size[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(size[:])
		}
		if length > 1<<20 || uint64(len(message))+length > 1<<20 {
			return nil, fmt.Errorf("server message exceeds test bound")
		}
		body := make([]byte, int(length))
		if _, err := io.ReadFull(c.reader, body); err != nil {
			return nil, err
		}
		switch header[0] & 15 {
		case 8:
			if err := c.frame(8, body); err != nil {
				return nil, err
			}
			return nil, io.EOF
		case 9:
			if err := c.frame(10, body); err != nil {
				return nil, err
			}
			continue
		case 10:
			continue
		case 0, 1:
			message = append(message, body...)
		default:
			return nil, fmt.Errorf("unexpected opcode %d", header[0]&15)
		}
		if header[0]&0x80 != 0 {
			return decodeMessage(message)
		}
	}
}

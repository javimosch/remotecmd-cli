package main

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/gorilla/websocket"
)

func emitProgress(event string, data map[string]interface{}) {
	progress := map[string]interface{}{
		"event": event,
		"data":  data,
	}
	jsonBytes, _ := json.Marshal(progress)
	fmt.Println(string(jsonBytes))
}

// wsDialer returns a WebSocket dialer tuned for high-throughput file transfers:
// - 1 MiB read/write buffers (default ~212 KB) for high-RTT links
// - TCP_NODELAY to disable Nagle's algorithm (reduces latency for small
//   JSON headers that precede each binary chunk)
func wsDialer() *websocket.Dialer {
	return &websocket.Dialer{
		ReadBufferSize:  1 << 20, // 1 MiB
		WriteBufferSize: 1 << 20, // 1 MiB
		NetDial: func(network, addr string) (net.Conn, error) {
			conn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				tcpConn.SetNoDelay(true)
				tcpConn.SetWriteBuffer(1 << 20) // 1 MiB
				tcpConn.SetReadBuffer(1 << 20)  // 1 MiB
			}
			return conn, nil
		},
	}
}

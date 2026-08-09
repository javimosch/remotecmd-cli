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

// chunkSize is the current file transfer chunk size (set at init from
// effectiveChunkSize). Used to size WebSocket write buffers to match.
var chunkSize = effectiveChunkSize()

// wsDialer returns a WebSocket dialer tuned for high-throughput file
// transfers. Based on benchmarking against expert references:
//
//   - 1 MiB read/write buffers: tested 64 KiB (slower, more system calls),
//     2 MiB (slower, too much memory per connection), 1 MiB is the sweet
//     spot (gorilla commit 856ca61: "Limit the buffer sizes to the maximum
//     expected message size").
//   - TCP_NODELAY disables Nagle's algorithm (reduces latency for small
//     JSON headers that precede each binary chunk).
//   - SO_SNDBUF/SO_RCVBUF set to 1 MiB: tested without setting them (let
//     kernel autotune) — was slower because autotuning starts small and
//     takes RTT*packets to grow. Explicit 1 MiB gives immediate full window.
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

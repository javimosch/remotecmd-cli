package main

import (
	"sync"

	"github.com/gorilla/websocket"
)

type relayClient struct {
	conn      *websocket.Conn
	name      string
	token     string
	mu        sync.Mutex
	chunkRoutes map[string]*chunkRoute // client transfer ID -> forwarding route
	lastBinaryRoute *chunkRoute // route for the next binary frame (BinaryChunk=true)
	// Async write queue for file chunk forwarding. When non-nil, binary
	// frames and chunk headers are sent through the queue instead of
	// synchronously, allowing the relay read loop to immediately read
	// the next frame from the client while the previous one is still
	// being written to the target.
	writeQueueMu sync.Mutex
	writeQueue   chan []byte
	writeErr     error // set by the writer goroutine on failure
	writeOnce    sync.Once
}

// startWriter launches a background goroutine that drains the write queue
// and writes frames to the connection in order. This decouples the relay's
// read loop from the target's write speed.
func (c *relayClient) startWriter() {
	c.writeOnce.Do(func() {
		c.writeQueue = make(chan []byte, 128) // buffer up to 128 frames
		go func() {
			for frame := range c.writeQueue {
				// First byte indicates frame type:
				// 0 = text (JSON), 1 = binary, 2 = header+binary pair
				c.mu.Lock()
				var err error
				if len(frame) > 0 && frame[0] == 2 {
					// Header+binary pair: [2][4-byte hl][header][binary]
					hl := int(frame[1])<<24 | int(frame[2])<<16 | int(frame[3])<<8 | int(frame[4])
					header := frame[5 : 5+hl]
					binary := frame[5+hl:]
					if err = c.conn.WriteMessage(websocket.TextMessage, header); err == nil {
						err = c.conn.WriteMessage(websocket.BinaryMessage, binary)
					}
				} else {
					msgType := websocket.TextMessage
					data := frame
					if len(frame) > 0 && frame[0] == 1 {
						msgType = websocket.BinaryMessage
						data = frame[1:]
					} else if len(frame) > 0 && frame[0] == 0 {
						data = frame[1:]
					}
					err = c.conn.WriteMessage(msgType, data)
				}
				c.mu.Unlock()
				if err != nil {
					c.writeErr = err
					// Drain remaining frames
					for range c.writeQueue {
					}
					return
				}
			}
		}()
	})
}

// closeWriter shuts down the async write queue. Called after the transfer
// result is received, meaning all chunks have been forwarded successfully.
func (c *relayClient) closeWriter() {
	c.writeQueueMu.Lock()
	q := c.writeQueue
	c.writeQueue = nil
	c.writeQueueMu.Unlock()
	if q != nil {
		close(q)
	}
}

func (c *relayClient) send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(msg)
}

// sendRawBytes sends a raw binary frame to the client (no JSON wrapping).
// Used for forwarding binary file chunk data. If an async write queue is
// active, enqueues the frame instead of blocking.
func (c *relayClient) sendRawBytes(data []byte) error {
	c.writeQueueMu.Lock()
	q := c.writeQueue
	c.writeQueueMu.Unlock()
	if q != nil {
		// Prepend 1 to indicate binary frame
		frame := make([]byte, len(data)+1)
		frame[0] = 1
		copy(frame[1:], data)
		select {
		case q <- frame:
			return c.writeErr
		default:
			// Queue full — fall back to synchronous send
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// sendRawJSON sends pre-marshaled JSON bytes as a text frame.
// Used for forwarding file chunk headers without re-serialization.
func (c *relayClient) sendRawJSON(data []byte) error {
	c.writeQueueMu.Lock()
	q := c.writeQueue
	c.writeQueueMu.Unlock()
	if q != nil {
		// Prepend 0 to indicate text frame
		frame := make([]byte, len(data)+1)
		frame[0] = 0
		copy(frame[1:], data)
		select {
		case q <- frame:
			return c.writeErr
		default:
			// Queue full — fall back to synchronous send
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// sendRawHeaderAndBinary atomically enqueues a JSON header followed by a
// binary frame. This prevents interleaving with other streams' chunks when
// multiple parallel streams share the same target's async write queue.
func (c *relayClient) sendRawHeaderAndBinary(header []byte, binary []byte) error {
	c.writeQueueMu.Lock()
	q := c.writeQueue
	c.writeQueueMu.Unlock()
	if q != nil {
		// Format: [2][4-byte header length][header...][binary...]
		hl := len(header)
		frame := make([]byte, 1+4+hl+len(binary))
		frame[0] = 2
		frame[1] = byte(hl >> 24)
		frame[2] = byte(hl >> 16)
		frame[3] = byte(hl >> 8)
		frame[4] = byte(hl)
		copy(frame[5:], header)
		copy(frame[5+hl:], binary)
		select {
		case q <- frame:
			return c.writeErr
		default:
			// Queue full — fall back to synchronous send
		}
	}
	// Synchronous: send header then binary
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, header); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.BinaryMessage, binary)
}

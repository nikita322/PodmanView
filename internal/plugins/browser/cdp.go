package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// cdpWriteTimeout bounds a single WebSocket write to the browser.
const cdpWriteTimeout = 10 * time.Second

// errCDPClosed is returned by calls made after the connection was closed.
var errCDPClosed = errors.New("devtools connection closed")

// cdpError is an error object returned by the DevTools protocol.
type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string {
	return fmt.Sprintf("devtools error %d: %s", e.Code, e.Message)
}

// cdpMessage is a single flattened-session DevTools protocol frame.
// Requests and responses use ID, events use Method (+ SessionID).
type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

// cdpHandler receives a DevTools event. Handlers run on the reader goroutine
// and therefore must return quickly.
type cdpHandler func(sessionID string, params json.RawMessage)

// handlerEntry is a registered handler with a stable identity for unregistering.
type handlerEntry struct {
	id int64
	fn cdpHandler
}

// cdpClient is a minimal Chrome DevTools Protocol client: JSON-RPC over a
// WebSocket with flat session multiplexing. No third-party wrappers involved.
type cdpClient struct {
	conn *websocket.Conn

	writeMu sync.Mutex

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan cdpMessage
	handlers map[string][]handlerEntry
	nextHid  int64

	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// dialCDP connects to a browser-level DevTools WebSocket endpoint.
func dialCDP(wsURL string, timeout time.Duration) (*cdpClient, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		ReadBufferSize:   1 << 16,
		WriteBufferSize:  1 << 16,
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("devtools dial %s: %w", wsURL, err)
	}
	conn.SetReadLimit(64 << 20)

	c := &cdpClient{
		conn:     conn,
		pending:  make(map[int64]chan cdpMessage),
		handlers: make(map[string][]handlerEntry),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// readLoop dispatches protocol frames until the connection dies.
func (c *cdpClient) readLoop() {
	defer c.close()
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.setCloseErr(err)
			return
		}
		var msg cdpMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch, ok := c.pending[msg.ID]
			if ok {
				delete(c.pending, msg.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- msg
			}
			continue
		}
		if msg.Method == "" {
			continue
		}
		c.mu.Lock()
		handlers := c.handlers[msg.Method]
		c.mu.Unlock()
		for _, entry := range handlers {
			invokeHandler(entry.fn, msg.SessionID, msg.Params)
		}
	}
}

// invokeHandler isolates the reader loop from panics in event handlers.
func invokeHandler(h cdpHandler, sessionID string, params json.RawMessage) {
	defer func() {
		_ = recover()
	}()
	h(sessionID, params)
}

// On registers an event handler and returns a function that unregisters it.
func (c *cdpClient) On(method string, handler cdpHandler) func() {
	c.mu.Lock()
	c.nextHid++
	id := c.nextHid
	c.handlers[method] = append(c.handlers[method], handlerEntry{id: id, fn: handler})
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			list := c.handlers[method]
			for i := range list {
				if list[i].id == id {
					c.handlers[method] = append(list[:i], list[i+1:]...)
					return
				}
			}
		})
	}
}

// Call performs a protocol call and decodes the result into out (when not nil).
func (c *cdpClient) Call(ctx context.Context, sessionID, method string, params, out any) error {
	raw, err := c.call(ctx, sessionID, method, params)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}

// call performs a protocol call and returns the raw result payload.
func (c *cdpClient) call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	var rawParams json.RawMessage
	if params != nil {
		buf, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("encode %s params: %w", method, err)
		}
		rawParams = buf
	}

	c.mu.Lock()
	if c.closeErr != nil {
		err := c.closeErr
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: %v", errCDPClosed, err)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan cdpMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	msg := cdpMessage{ID: id, Method: method, Params: rawParams, SessionID: sessionID}
	if err := c.write(ctx, msg); err != nil {
		c.discard(id)
		return nil, fmt.Errorf("send %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.discard(id)
		return nil, ctx.Err()
	case <-c.done:
		c.discard(id)
		return nil, errCDPClosed
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, resp.Error)
		}
		return resp.Result, nil
	}
}

// write sends a single frame with a deadline derived from the context.
func (c *cdpClient) write(ctx context.Context, msg cdpMessage) error {
	buf, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(cdpWriteTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, buf)
}

// discard drops a pending request entry.
func (c *cdpClient) discard(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// setCloseErr records why the connection died.
func (c *cdpClient) setCloseErr(err error) {
	c.mu.Lock()
	if c.closeErr == nil {
		c.closeErr = err
	}
	c.mu.Unlock()
}

// close terminates the underlying WebSocket exactly once.
func (c *cdpClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// Close shuts the client down (the reader loop finishes on its own).
func (c *cdpClient) Close() {
	c.mu.Lock()
	alreadyClosed := c.closeErr != nil
	c.mu.Unlock()
	if !alreadyClosed {
		c.setCloseErr(errCDPClosed)
	}
	c.close()
}

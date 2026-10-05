package mystiebel

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/evcc-io/evcc/util"
)

const (
	connectTimeout = 15 * time.Second
	rpcTimeout     = 10 * time.Second
)

// Client maintains a persistent WebSocket connection to the MyStiebel service.
type Client struct {
	mu  sync.Mutex
	log *util.Logger

	auth           *Auth
	installationID string
	clientUUID     string // auth client UUID, used as UUID field in setValues

	conn    *websocket.Conn
	pending sync.Map // map[int64]chan rpcResponse

	msgID atomic.Int64
}

// NewClient creates a new WebSocket client. Call Connect before use.
func NewClient(log *util.Logger, auth *Auth, installationID string) *Client {
	c := &Client{
		log:            log,
		auth:           auth,
		installationID: installationID,
		clientUUID:     auth.ClientID(),
	}
	// Start msgID from a random offset to reduce collision risk across restarts.
	c.msgID.Store(rand.Int64N(1_000_000) + 1)
	return c
}

func (c *Client) nextID() int64 {
	return c.msgID.Add(1)
}

// connect establishes a WebSocket connection and performs JSON-RPC login.
func (c *Client) connect(ctx context.Context) error {
	token, err := c.auth.Token(ctx)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, _, err := websocket.Dial(dialCtx, WSURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":          {"Bearer " + token},
			"X-SC-ClientApp-Name":    {AppName},
			"X-SC-ClientApp-Version": {AppVersion},
			"User-Agent":             {UserAgent},
		},
	})
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	c.conn = conn

	// Start reader goroutine.
	go c.readLoop()

	// JSON-RPC Login
	if err := c.login(ctx, token); err != nil {
		_ = conn.Close(websocket.StatusAbnormalClosure, "login failed")
		c.conn = nil
		return fmt.Errorf("ws login: %w", err)
	}

	c.log.DEBUG.Println("connected and logged in to MyStiebel WebSocket")
	return nil
}

// login sends the JSON-RPC Login message and waits for acknowledgement.
func (c *Client) login(ctx context.Context, token string) error {
	ch := make(chan rpcResponse, 1)
	c.pending.Store(int64(1), ch)
	defer c.pending.Delete(int64(1))

	msg := rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "Login",
		Params: loginParams{
			ClientID: c.installationID,
			JWT:      token,
		},
	}
	if err := c.writeJSON(ctx, msg); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		var ok bool
		if err := json.Unmarshal(resp.Result, &ok); err != nil || !ok {
			return fmt.Errorf("login rejected by server")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// readLoop reads WebSocket messages and routes them to pending callers.
func (c *Client) readLoop() {
	for {
		_, b, err := c.conn.Read(context.Background())
		if err != nil {
			c.log.TRACE.Println("ws read:", err)
			c.mu.Lock()
			c.conn = nil
			c.mu.Unlock()
			return
		}

		c.log.TRACE.Printf("ws recv: %s", b)

		var resp rpcResponse
		if err := json.Unmarshal(b, &resp); err != nil {
			c.log.WARN.Println("ws parse:", err)
			continue
		}

		if ch, ok := c.pending.Load(resp.ID); ok {
			ch.(chan rpcResponse) <- resp
		}
	}
}

// writeJSON marshals msg and writes it to the WebSocket connection.
func (c *Client) writeJSON(ctx context.Context, msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.log.TRACE.Printf("ws send: %s", b)
	return c.conn.Write(ctx, websocket.MessageText, b)
}

// ensureConnected makes sure the WebSocket is open, reconnecting if needed.
func (c *Client) ensureConnected(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}
	return c.connect(ctx)
}

// call sends a JSON-RPC request and returns the raw result.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	id := c.nextID()
	ch := make(chan rpcResponse, 1)
	c.pending.Store(id, ch)
	defer c.pending.Delete(id)

	msg := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	c.mu.Lock()
	err := c.writeJSON(ctx, msg)
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("ws write: %w", err)
	}

	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-rpcCtx.Done():
		return nil, fmt.Errorf("rpc timeout waiting for %s response", method)
	}
}

// GetValue reads a single register from the device.
func (c *Client) GetValue(ctx context.Context, register int64) (json.RawMessage, error) {
	raw, err := c.call(ctx, "getValues", getValuesParams{
		InstallationID: c.installationID,
		Fields:         []int64{register},
	})
	if err != nil {
		return nil, err
	}

	var result getValuesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse getValues result: %w", err)
	}

	for _, f := range result.Fields {
		if f.RegisterIndex == register {
			b, err := json.Marshal(f.DisplayValue)
			return b, err
		}
	}

	return nil, fmt.Errorf("register %d not in response", register)
}

// GetFloat reads a register value as a float64.
// The API returns displayValue as a JSON string (e.g. "53.5000"), so we handle both.
func (c *Client) GetFloat(ctx context.Context, register int64) (float64, error) {
	raw, err := c.GetValue(ctx, register)
	if err != nil {
		return 0, err
	}

	// Try numeric first.
	var v float64
	if err := json.Unmarshal(raw, &v); err == nil {
		return v, nil
	}

	// Fall back to quoted string.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("register %d: unexpected value format %s", register, raw)
	}
	v, err = strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("register %d: parse %q: %w", register, s, err)
	}
	return v, nil
}

// SetValue writes a value to a register.
func (c *Client) SetValue(ctx context.Context, register int64, value any) error {
	raw, err := c.call(ctx, "setValues", setValuesParams{
		InstallationID:          c.installationID,
		UUID:                    c.clientUUID,
		ListenWithValuesChanged: true,
		Fields:                  []fieldValue{{RegisterIndex: register, DisplayValue: value}},
	})
	if err == nil {
		c.log.TRACE.Printf("setValues result: %s", raw)
	}
	return err
}

// Close shuts down the WebSocket connection cleanly.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		_ = c.conn.Close(websocket.StatusNormalClosure, "closing")
		c.conn = nil
	}
}

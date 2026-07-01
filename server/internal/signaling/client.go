package signaling

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"lumen/internal/protocol"
)

// Connection tuning (contract §4.1 / §2.5, server-design §3.2).
const (
	handshakeTimeout = 5 * time.Second
	pingInterval     = 30 * time.Second
	pingTimeout      = 10 * time.Second
	writeTimeout     = 10 * time.Second
	sendBufferSize   = 64
	maxReadBytes     = 1 << 20 // 1 MiB per message (SDP can be large)
)

// Client is a single WebSocket connection session (one read pump, one write
// pump). Auth state is bound after handshake and updated in place on reauth.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan protocol.Envelope

	authed   atomic.Bool
	userID   string // internal user.id (set at handshake, then immutable)
	subject  string
	isOwner  bool
	tokenExp atomic.Int64 // current token exp (unix seconds)

	voiceChannelID atomic.Value // string; empty when not in a voice channel

	closeOnce sync.Once
	closed    chan struct{}
}

// newClient wraps an accepted connection.
func newClient(hub *Hub, conn *websocket.Conn) *Client {
	conn.SetReadLimit(maxReadBytes)
	c := &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan protocol.Envelope, sendBufferSize),
		closed: make(chan struct{}),
	}
	c.voiceChannelID.Store("")
	return c
}

// currentVoiceChannel returns the client's current voice channel id ("" if none).
func (c *Client) currentVoiceChannel() string {
	v, _ := c.voiceChannelID.Load().(string)
	return v
}

// setVoiceChannel records the client's current voice channel id.
func (c *Client) setVoiceChannel(id string) {
	c.voiceChannelID.Store(id)
}

// enqueue queues a message for the write pump. If the buffer is full (slow
// client) the connection is closed to prevent memory growth (contract §4.1).
func (c *Client) enqueue(msg protocol.Envelope) {
	select {
	case <-c.closed:
		return
	case c.send <- msg:
	default:
		// Slow client: drop the connection.
		c.closeConn()
	}
}

// sendNow writes a message synchronously, bypassing the send channel. Used for
// handshake failures and kick auth_error so the reason reaches the client
// before the socket closes.
func (c *Client) sendNow(msg protocol.Envelope) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_ = c.conn.Write(ctx, websocket.MessageText, raw)
}

// closeConn closes the connection once, unregisters from the hub, and converges
// voice state (multi-endpoint aware). Idempotent.
func (c *Client) closeConn() {
	c.closeOnce.Do(func() {
		close(c.closed)
		if c.authed.Load() {
			c.convergeVoiceOnClose()
			c.hub.unregister(c)
		}
		_ = c.conn.Close(websocket.StatusNormalClosure, "")
	})
}

// convergeVoiceOnClose handles the voice cleanup for a closing connection
// (loop-2). If this connection owns the user's voice in its channel AND the
// user has no other connection, the user leaves the room (PC close + user_left).
// Otherwise only this connection is cleaned up; the member stays (endpoint
// switch) and no user_left is broadcast.
func (c *Client) convergeVoiceOnClose() {
	channelID := c.currentVoiceChannel()
	if channelID == "" {
		return
	}
	// Is this connection the active voice connection for the user in this room?
	active, ok := c.hub.rooms.ActiveClientOf(channelID, c.userID)
	if !ok || active != any(c) {
		return // not the active endpoint; nothing to tear down
	}
	// Active endpoint: only leave if the user has no other live connection.
	if c.hub.UserConnCount(c.userID) > 1 {
		return // another endpoint remains; keep the member, no user_left
	}
	if c.hub.rooms.Leave(channelID, c.userID) {
		c.hub.UserLeft(channelID, c.userID)
	}
}

// writeLoop is the sole writer for the connection (WS requires serial writes).
// It drains the send channel and periodically pings for keepalive.
func (c *Client) writeLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.closed:
			return
		case msg := <-c.send:
			if !c.writeEnvelope(msg) {
				c.closeConn()
				return
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
			err := c.conn.Ping(ctx)
			cancel()
			if err != nil {
				c.closeConn()
				return
			}
		}
	}
}

// writeEnvelope marshals and writes one envelope with a write deadline.
func (c *Client) writeEnvelope(msg protocol.Envelope) bool {
	raw, err := json.Marshal(msg)
	if err != nil {
		return true // skip unencodable message, keep connection
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, raw) == nil
}

// readEnvelope reads and decodes one envelope with the given context.
func (c *Client) readEnvelope(ctx context.Context) (protocol.Envelope, error) {
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return protocol.Envelope{}, err
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return protocol.Envelope{}, err
	}
	return env, nil
}

package signaling

import (
	"net/http"

	"github.com/coder/websocket"
)

// Mount wraps base so that GET /ws upgrades to a WebSocket handled by the hub;
// all other paths fall through to base (server-design §6.2).
func Mount(base http.Handler, hub *Hub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			hub.ServeWS(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
}

// ServeWS upgrades an HTTP request to a WebSocket and runs the connection
// lifecycle. Authentication happens via the first-frame handshake, not here, so
// no bearer is read at upgrade time (contract §2.4). InsecureSkipVerify is set
// because the desktop client is not a browser bound by same-origin, and TLS /
// origin control is handled at the Traefik edge (server-design §6.6: no CORS).
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	client := newClient(h, conn)

	defer func() {
		if rec := recover(); rec != nil {
			h.logger.Error("ws panic recovered", "panic", rec)
			client.closeConn()
		}
	}()

	client.serve(r.Context())
}

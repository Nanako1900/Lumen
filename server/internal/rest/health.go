package rest

import "net/http"

// health is the public liveness probe (contract §3.4 端点 10). It is a pure
// liveness check and does not touch the DB (server-design §6.5 loop-7).
func health(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, http.StatusOK, map[string]string{"status": "ok"})
}

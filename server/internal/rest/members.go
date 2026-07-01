package rest

import (
	"net/http"

	"lumen/internal/auth"
	"lumen/internal/protocol"
	"lumen/internal/store"
)

// listMembers handles GET /api/v1/members (contract §3.4 端点 5): all users
// that ever logged in, ordered by display_name ascending, each with is_owner.
func listMembers(st store.Store, owners *auth.OwnerSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := st.ListUsers(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "获取成员失败")
			return
		}
		out := make([]protocol.User, 0, len(users))
		for _, u := range users {
			out = append(out, auth.ToDTO(u, owners))
		}
		writeOK(w, http.StatusOK, out)
	}
}

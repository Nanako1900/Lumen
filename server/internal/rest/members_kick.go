package rest

import (
	"errors"
	"net/http"
	"time"

	"lumen/internal/auth"
	"lumen/internal/signaling"
	"lumen/internal/store"
)

// defaultCooldownSeconds is applied when the kick body omits cooldown_seconds
// (contract §3.4 端点 9).
const defaultCooldownSeconds = 3600

// kickReq is the kick body (contract §3.4 端点 9). CooldownSeconds is a pointer
// so an omitted value (nil) defaults to 3600, while an explicit 0 means
// disconnect-only without a ban.
type kickReq struct {
	CooldownSeconds *int `json:"cooldown_seconds"`
}

// kickMember handles POST /api/v1/members/{userId}/kick ([v1] owner). Order
// (contract §3.4 端点 9, kick-2/kick-3/loop-5/data-2):
//  1. reject kicking self (400 VALIDATION_ERROR);
//  2. when cooldown>0, write kicked_until FIRST (close the reconnect race);
//  3. disconnect all connections (auth_error{KICKED} then close) + leave rooms;
//  4. user_left broadcast happens inside DisconnectUser.
func kickMember(st store.Store, owners *auth.OwnerSet, hub signaling.Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targetID := r.PathValue("userId")

		claims := auth.ClaimsFromContext(r.Context())
		if claims == nil {
			writeErr(w, http.StatusUnauthorized, "UNAUTHENTICATED", "缺少身份信息")
			return
		}
		caller, err := st.GetUserBySubject(r.Context(), claims.Subject)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "获取调用者失败")
			return
		}
		// Self-check: owner cannot kick themselves.
		if targetID == caller.ID {
			writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "不能踢出自己")
			return
		}

		if _, err := st.GetUserByID(r.Context(), targetID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "NOT_FOUND", "用户不存在")
				return
			}
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "踢人失败")
			return
		}

		var req kickReq
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "请求体无效")
			return
		}
		cooldown := defaultCooldownSeconds
		if req.CooldownSeconds != nil {
			cooldown = *req.CooldownSeconds
		}
		if cooldown < 0 {
			writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "cooldown_seconds 不能为负")
			return
		}

		// Write kicked_until FIRST when banning, to avoid a reconnect racing the
		// ban write (contract §3.4 端点 9 ordering).
		if cooldown > 0 {
			until := time.Now().UTC().Add(time.Duration(cooldown) * time.Second)
			if err := st.SetKickedUntil(r.Context(), targetID, until); err != nil {
				writeErr(w, http.StatusInternalServerError, "INTERNAL", "踢人失败")
				return
			}
		}

		// Disconnect (sends auth_error{KICKED} then closes) + remove from rooms
		// + broadcast user_left (all inside DisconnectUser).
		hub.DisconnectUser(targetID, "KICKED")
		writeOK(w, http.StatusOK, nil)
	}
}

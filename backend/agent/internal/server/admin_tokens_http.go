package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type gatewayAdminTokenCreateRequest struct {
	User    string `json:"user"`
	Name    string `json:"name,omitempty"`
	Expires string `json:"expires,omitempty"`
}

func (h *GatewayHub) HandleAdminTokens(w http.ResponseWriter, r *http.Request) {
	h.auditAdminMutation("/v1/admin/tokens", h.handleAdminTokens)(w, r)
}

func (h *GatewayHub) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		tokens, err := h.store.ListTokens(TokenKindUser)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"tokens": tokens})
		return
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if err := h.store.RevokeToken(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "token not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"id": id, "revoked": true})
		return
	case http.MethodPost:
		// 继续向下执行创建逻辑
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req gatewayAdminTokenCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid token request", http.StatusBadRequest)
		return
	}
	user, err := h.store.FindUserByName(req.User)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	createReq := CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: req.Name}
	if strings.TrimSpace(req.Expires) != "" {
		ttl, err := time.ParseDuration(strings.TrimSpace(req.Expires))
		if err != nil {
			http.Error(w, "invalid expires duration", http.StatusBadRequest)
			return
		}
		if ttl <= 0 {
			http.Error(w, "expires duration must be positive", http.StatusBadRequest)
			return
		}
		createReq.ExpiresAt = time.Now().UTC().Add(ttl)
	}
	token, err := h.store.CreateToken(createReq)
	if err != nil {
		http.Error(w, "failed to create token", http.StatusInternalServerError)
		return
	}
	writeJSONHTTP(w, gatewayLoginResponse{
		Token:     token.Plaintext,
		TokenID:   token.ID,
		TokenType: string(TokenKindUser),
		Username:  user.Name,
		Name:      token.Name,
		IssuedAt:  tokenTimePtr(token.CreatedAt),
		CreatedAt: tokenTimePtr(token.CreatedAt),
		ExpiresAt: tokenTimePtr(token.ExpiresAt),
	})
}

package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

type gatewayLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
}

type gatewayLoginResponse struct {
	Token     string     `json:"token"`
	TokenID   string     `json:"token_id"`
	TokenType string     `json:"token_type"`
	Username  string     `json:"username"`
	Name      string     `json:"name,omitempty"`
	IssuedAt  *time.Time `json:"issued_at,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type gatewayCurrentTokenResponse struct {
	TokenID   string     `json:"token_id"`
	UserID    string     `json:"user_id"`
	Username  string     `json:"username"`
	IsAdmin   bool       `json:"is_admin"`
	Name      string     `json:"name"`
	IssuedAt  time.Time  `json:"issued_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (h *GatewayHub) HandleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req gatewayLoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid login request", http.StatusBadRequest)
		return
	}
	now := time.Now()
	clientIP := loginClientIP(r)
	if forwarded := consoleForwardedIP(r, h.opts.ConsoleForwardSecret, now); forwarded != "" {
		clientIP = forwarded
	}
	if h.loginFailures.limited(clientIP, req.Username, now, h.opts.LoginFailureLimit, h.opts.LoginFailureWindow) {
		w.Header().Set("Retry-After", "60")
		writeDoopsError(w, http.StatusTooManyRequests, "", doopsErrorResponse{
			Error:      "rate_limited",
			Code:       "gateway_login_rate_limited",
			AuthDomain: "gateway",
			Message:    "Gateway password login is temporarily unavailable for this client",
			NextAction: "Wait and retry, or use an existing Gateway user token",
		})
		return
	}
	user, err := h.store.VerifyUserPassword(req.Username, req.Password)
	if err != nil {
		if !errors.Is(err, ErrUnauthorized) {
			writeGatewayAuthError(w, `Bearer realm="doops-gateway"`, err)
			return
		}
		h.loginFailures.record(clientIP, req.Username, now, h.opts.LoginFailureWindow)
		writeDoopsError(w, http.StatusUnauthorized, "", doopsErrorResponse{
			Error:      "unauthorized",
			Code:       "gateway_login_invalid_credentials",
			AuthDomain: "gateway",
			Message:    "Gateway username or password was rejected",
			NextAction: "Verify the Gateway username/password or provide a valid Gateway user token",
		})
		return
	}
	// A successful password login clears this pair's failure streak so one
	// earlier typo does not leave the account one mistake from a lock.
	h.loginFailures.reset(clientIP, req.Username, now)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "password login"
	}
	token, err := h.store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    user.ID,
		Name:      name,
		ExpiresAt: time.Now().UTC().Add(h.opts.LoginTokenTTL),
		IssuedVia: tokenIssuedViaPasswordLogin,
	})
	if err != nil {
		http.Error(w, "failed to create token", http.StatusInternalServerError)
		return
	}
	if revoked, err := h.store.RevokeExcessPasswordLoginTokens(user.ID, token.ID, passwordLoginTokenCap, time.Now().UTC()); err != nil {
		log.Printf("[gateway] failed to cap password-login tokens for user %s: %v", user.ID, err)
	} else if revoked > 0 {
		log.Printf("[gateway] revoked %d older password-login token(s) for user %s", revoked, user.ID)
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

func (h *GatewayHub) HandleAuthMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	info, isAdmin, err := h.store.CurrentUserTokenInfo(auth)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	writeJSONHTTP(w, gatewayCurrentTokenResponse{
		TokenID: info.ID, UserID: info.UserID, Username: info.UserName, IsAdmin: isAdmin, Name: info.Name,
		IssuedAt: info.CreatedAt, ExpiresAt: info.ExpiresAt,
	})
}

func tokenTimePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func (h *GatewayHub) authenticateUser(r *http.Request) (TokenAuth, error) {
	return authenticateGatewayUser(h.store, h.opts, r)
}

func (h *ClientService) authenticateUser(r *http.Request) (TokenAuth, error) {
	return authenticateGatewayUser(h.store, h.opts, r)
}

func authenticateGatewayUser(store *GatewayStore, opts GatewayHubOptions, r *http.Request) (TokenAuth, error) {
	token := bearerToken(r)
	if token == "" && strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return TokenAuth{}, authFailure(AuthFailureInvalid)
	}
	return verifyAndRenewUserToken(store, opts, token)
}

func verifyAndRenewUserToken(store *GatewayStore, opts GatewayHubOptions, token string) (TokenAuth, error) {
	auth, err := store.VerifyUserToken(token)
	if err != nil {
		return auth, err
	}
	if err := store.MaybeRenewPasswordLoginToken(auth, opts.LoginTokenTTL, opts.LoginTokenMaxLifetime, time.Now().UTC()); err != nil {
		logPasswordLoginRenewalError(auth.TokenID, err)
	}
	return auth, nil
}

func (h *ClientService) writeUserAuthError(w http.ResponseWriter, _ *http.Request, err error) {
	writeGatewayAuthError(w, `Bearer realm="doops-gateway"`, err)
}

func (h *GatewayHub) writeUserAuthError(w http.ResponseWriter, _ *http.Request, err error) {
	writeGatewayAuthError(w, `Bearer realm="doops-gateway"`, err)
}

func bearerToken(r *http.Request) string {
	token := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return strings.TrimSpace(token[len("bearer "):])
	}
	return ""
}

func writeGatewayAuthError(w http.ResponseWriter, challenge string, authErr error) {
	response := gatewayAuthErrorResponse(authErr)
	status := http.StatusUnauthorized
	if response.Code == "gateway_auth_unavailable" {
		status = http.StatusServiceUnavailable
		response.Error = "service_unavailable"
		challenge = ""
	}
	writeDoopsError(w, status, challenge, response)
}

func authFailureCode(err error) string {
	var failure *TokenAuthError
	if errors.As(err, &failure) {
		switch failure.Reason {
		case AuthFailureMissing:
			return "gateway_auth_missing"
		case AuthFailureExpired:
			return "gateway_auth_expired"
		case AuthFailureRevoked:
			return "gateway_auth_revoked"
		case AuthFailureUserDisabled:
			return "gateway_auth_user_disabled"
		case AuthFailureInvalidExpiry:
			return "gateway_auth_invalid_expiry"
		case AuthFailureInvalid:
			return "gateway_auth_invalid"
		}
	}
	return "gateway_auth_unavailable"
}

func gatewayAuthErrorResponse(err error) doopsErrorResponse {
	code := authFailureCode(err)
	message := "Gateway authentication could not be verified"
	nextAction := "Retry the request; if the Gateway is unavailable, inspect Gateway health and logs"
	if code != "gateway_auth_unavailable" {
		nextAction = "Run doops login --gateway <gateway-url> for this Gateway; do not replace the Gateway token with a managed credential or business API key"
	}
	switch code {
	case "gateway_auth_missing":
		message = "Gateway user token is missing"
	case "gateway_auth_invalid":
		message = "Gateway user token is invalid"
	case "gateway_auth_expired":
		message = "Gateway user token has expired"
	case "gateway_auth_revoked":
		message = "Gateway user token was revoked"
	case "gateway_auth_user_disabled":
		message = "Gateway user account is disabled"
		nextAction = "Contact a Gateway administrator to enable this account"
	case "gateway_auth_invalid_expiry":
		message = "Gateway user token has invalid expiry metadata"
		nextAction = "Contact a Gateway administrator to repair the token expiry metadata and reissue the token"
	}
	return doopsErrorResponse{
		Error:      "unauthorized",
		Code:       code,
		AuthDomain: "gateway",
		Message:    message,
		NextAction: nextAction,
	}
}

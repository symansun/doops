package server

import (
	"encoding/json"
	"net/http"
)

type doopsErrorResponse struct {
	Error      string `json:"error"`
	Code       string `json:"code"`
	AuthDomain string `json:"auth_domain,omitempty"`
	Message    string `json:"message"`
	NextAction string `json:"next_action,omitempty"`
}

func writeDoopsError(w http.ResponseWriter, status int, challenge string, response doopsErrorResponse) {
	if challenge != "" {
		w.Header().Set("WWW-Authenticate", challenge)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func writeJSONHTTP(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

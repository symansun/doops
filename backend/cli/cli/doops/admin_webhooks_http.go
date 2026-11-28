package main

import (
	"encoding/json"
	"net/url"
	"strconv"
)

func GatewayAdminWebhooksList(gateway, token string) ([]byte, error) {
	return gatewayAdminRequest("GET", gateway, token, "/v1/admin/webhooks", nil, nil)
}

func GatewayAdminWebhookGet(gateway, token, id string) ([]byte, error) {
	return gatewayAdminRequest("GET", gateway, token, adminWebhookEndpointPath(id), nil, nil)
}

func GatewayAdminWebhookCreate(gateway, token string, payload map[string]any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return gatewayAdminRequest("POST", gateway, token, "/v1/admin/webhooks", nil, body)
}

func GatewayAdminWebhookUpdate(gateway, token, id string, payload map[string]any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return gatewayAdminRequest("PATCH", gateway, token, adminWebhookEndpointPath(id), nil, body)
}

func GatewayAdminWebhookDelete(gateway, token, id string) ([]byte, error) {
	return gatewayAdminRequest("DELETE", gateway, token, adminWebhookEndpointPath(id), nil, nil)
}

func GatewayAdminWebhookRotateSecret(gateway, token, id string) ([]byte, error) {
	return gatewayAdminRequest("POST", gateway, token, adminWebhookEndpointPath(id, "rotate-secret"), nil, []byte("{}"))
}

func GatewayAdminWebhookTest(gateway, token, id string) ([]byte, error) {
	return gatewayAdminRequest("POST", gateway, token, adminWebhookEndpointPath(id, "test"), nil, []byte("{}"))
}

func GatewayAdminWebhookDeliveries(gateway, token, id string, limit int) ([]byte, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return gatewayAdminRequest("GET", gateway, token, adminWebhookEndpointPath(id, "deliveries"), query, nil)
}

func adminWebhookEndpointPath(id string, suffix ...string) string {
	path := "/v1/admin/webhooks/" + url.PathEscape(id)
	for _, s := range suffix {
		path += "/" + s
	}
	return path
}

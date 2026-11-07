package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func gatewayAdminRequest(method, gateway, token, endpoint string, query url.Values, body []byte) ([]byte, error) {
	reqURL, err := gatewayURLWithPath(gateway, endpoint, query)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, reqURL, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := (&http.Client{Timeout: 11 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeGatewayHTTPError("gateway "+endpoint, resp, respBody)
	}
	return respBody, nil
}

func applyTarget(target string, servers []Server, configErr error) (string, string) {
	if strings.TrimSpace(target) == "" {
		fmt.Println("Error: --target is required")
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	return server.Gateway, mustResolveToken(*server)
}

func exitOnErr(err error) {
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

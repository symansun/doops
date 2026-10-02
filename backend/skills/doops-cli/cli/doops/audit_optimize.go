package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func runAdminAuditOptimize(args []string, servers []Server, configErr error, session string) {
	var target string
	flags := flag.NewFlagSet("admin audit optimize", flag.ExitOnError)
	flags.StringVar(&target, "target", "", "Configured Gateway target used to select the authenticated control plane")
	flags.Parse(args)
	if target == "" || strings.TrimSpace(session) == "" {
		fmt.Println("Error: --target and -session are required")
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil {
		fmt.Println("Error: configured target not found")
		os.Exit(1)
	}
	token := mustResolveToken(*server)
	body, _ := json.Marshal(map[string]string{"session_id": session})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(server.Gateway, "/")+"/v1/admin/audit/optimize", bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		var response *http.Response
		response, err = (&http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err == nil {
			defer response.Body.Close()
			var raw []byte
			raw, err = io.ReadAll(io.LimitReader(response.Body, 65536))
			if err == nil && response.StatusCode != http.StatusOK {
				err = fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
			}
			if err == nil {
				var result struct {
					Optimized           bool     `json:"optimized"`
					DefinitionsVerified bool     `json:"definitions_verified"`
					QueryPlans          []string `json:"query_plans"`
				}
				if json.Unmarshal(raw, &result) != nil || !result.Optimized || !result.DefinitionsVerified || len(result.QueryPlans) != 3 {
					err = fmt.Errorf("audit maintenance returned no verified result")
				} else {
					fmt.Println(strings.TrimSpace(string(raw)))
					return
				}
			}
		}
	}
	fmt.Printf("Error: audit index maintenance failed: %v\n", err)
	os.Exit(1)
}

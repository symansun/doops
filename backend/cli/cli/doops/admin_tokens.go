package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func runAdminTokensList(args []string, servers []Server, configErr error) {
	fs := flag.NewFlagSet("admin token list", flag.ExitOnError)
	target := fs.String("target", "", "Configured gateway target whose token/gateway should be used")
	fs.Parse(args)
	if strings.TrimSpace(*target) == "" {
		fmt.Println("Error: --target is required")
		fs.Usage()
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, *target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, *target))
		os.Exit(1)
	}
	tokens, err := GatewayAdminTokensList(server.Gateway, mustResolveToken(*server), "user")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	for _, token := range tokens {
		fmt.Printf("token_id=%s kind=%s user=%s name=%s prefix=%s revoked=%t expires_at=%s created_at=%s\n",
			token.ID, token.Kind, token.UserName, token.Name, token.Prefix,
			token.Revoked, token.ExpiresAt, token.CreatedAt)
	}
}

func runAdminTokenRevoke(args []string, servers []Server, configErr error) {
	var target, id string
	fs := flag.NewFlagSet("admin token revoke", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	fs.StringVar(&id, "id", "", "Token id to revoke")
	fs.Parse(args)
	if strings.TrimSpace(target) == "" || strings.TrimSpace(id) == "" {
		fmt.Println("Error: --target and --id are required")
		fs.Usage()
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	if err := GatewayAdminTokenRevoke(server.Gateway, mustResolveToken(*server), id); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("token_id=%s revoked=true\n", strings.TrimSpace(id))
}

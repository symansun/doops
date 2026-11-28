package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func resolveAdminTargetFlags(name string, args []string, servers []Server, configErr error, opID *string) (Server, string) {
	var target string
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose token/gateway should be used")
	if opID != nil {
		fs.StringVar(opID, "id", "", "Active operation id to request cancellation for; acknowledgment does not confirm it has stopped")
	}
	fs.Parse(args)
	if strings.TrimSpace(target) == "" {
		fmt.Println("Error: --target is required")
		fs.Usage()
		os.Exit(1)
	}
	requireConfig(configErr)
	server := findServer(servers, target)
	if server == nil || strings.TrimSpace(server.Gateway) == "" {
		fmt.Println(targetNotFound(servers, target))
		os.Exit(1)
	}
	if opID != nil && strings.TrimSpace(*opID) == "" {
		fmt.Println("Error: --id is required")
		fs.Usage()
		os.Exit(1)
	}
	return *server, mustResolveToken(*server)
}

func requireConfig(err error) {
	if err == nil {
		return
	}
	fmt.Printf("Error: no doops node config found: %v\n", err)
	fmt.Println("First login (discovers visible targets): doops login --gateway <gateway-url>")
	fmt.Println("To pin a route first: doops add --name <name> --gateway <gateway-url> --cluster <cluster> --instance <instance>")
	os.Exit(1)
}

func ResolveToken(server Server) (string, error) {
	if strings.TrimSpace(server.Gateway) == "" {
		return "", fmt.Errorf("target %q has no Gateway route", server.Name)
	}
	auth, err := LoadAuth()
	if err != nil {
		return "", err
	}
	if p := auth.GetGateway(server.Gateway); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no login for Gateway %s; run `doops login --gateway %s`",
		gatewayAuthKey(server.Gateway), gatewayAuthKey(server.Gateway))
}

func mustResolveToken(server Server) string {
	token, err := ResolveToken(server)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	return token
}

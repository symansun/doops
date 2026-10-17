package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"golang.org/x/term"
)

type gatewayAdminUserSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Disabled    bool   `json:"disabled"`
	HasPassword bool   `json:"has_password"`
	GrantCount  int    `json:"grant_count"`
	IsAdmin     bool   `json:"is_admin"`
	CreatedAt   string `json:"created_at"`
}

type gatewayAdminUsersResponse struct {
	Users []gatewayAdminUserSummary `json:"users"`
}

type gatewayAdminUserCreateRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

type gatewayAdminUserCreateResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
}

type gatewayAdminGrant struct {
	ID        int64    `json:"id"`
	UserID    string   `json:"user_id"`
	UserName  string   `json:"user_name"`
	Cluster   string   `json:"cluster"`
	Instance  string   `json:"instance"`
	Actions   []string `json:"actions"`
	CreatedAt string   `json:"created_at"`
}

type gatewayAdminGrantsResponse struct {
	Grants []gatewayAdminGrant `json:"grants"`
}

type gatewayAdminGrantRequest struct {
	UserID   string   `json:"user_id,omitempty"`
	User     string   `json:"user,omitempty"`
	Cluster  string   `json:"cluster"`
	Instance string   `json:"instance"`
	Actions  []string `json:"actions"`
}

type gatewayAdminUserDisableRequest struct {
	UserID   string `json:"user_id"`
	Disabled bool   `json:"disabled"`
}

type gatewayAdminUserPasswordRequest struct {
	UserID   string `json:"user_id,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password"`
}

var ordinaryGatewayActions = []string{
	"target:use",
}

func GatewayAdminUsersList(gateway, adminToken string) ([]gatewayAdminUserSummary, error) {
	raw, err := gatewayAdminRequest("GET", gateway, adminToken, "/v1/admin/users", nil, nil)
	if err != nil {
		return nil, err
	}
	var parsed gatewayAdminUsersResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed.Users, nil
}

func GatewayAdminUserCreate(gateway, adminToken string, create gatewayAdminUserCreateRequest) (gatewayAdminUserCreateResponse, error) {
	create.Name = strings.TrimSpace(create.Name)
	if create.Name == "" {
		return gatewayAdminUserCreateResponse{}, fmt.Errorf("user is required")
	}
	if strings.TrimSpace(create.Password) == "" {
		return gatewayAdminUserCreateResponse{}, fmt.Errorf("password is required")
	}
	body, err := json.Marshal(create)
	if err != nil {
		return gatewayAdminUserCreateResponse{}, err
	}
	raw, err := gatewayAdminRequest("POST", gateway, adminToken, "/v1/admin/users", nil, body)
	if err != nil {
		return gatewayAdminUserCreateResponse{}, err
	}
	var parsed gatewayAdminUserCreateResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return gatewayAdminUserCreateResponse{}, err
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return gatewayAdminUserCreateResponse{}, fmt.Errorf("gateway user create returned empty user id")
	}
	return parsed, nil
}

func GatewayAdminUserDisable(gateway, adminToken, userID string, disabled bool) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("user id is required")
	}
	body, err := json.Marshal(gatewayAdminUserDisableRequest{UserID: userID, Disabled: disabled})
	if err != nil {
		return err
	}
	_, err = gatewayAdminRequest("POST", gateway, adminToken, "/v1/admin/users/disable", nil, body)
	return err
}

func GatewayAdminUserPassword(gateway, adminToken, userID, userName, password string) error {
	userID = strings.TrimSpace(userID)
	userName = strings.TrimSpace(userName)
	password = strings.TrimSpace(password)
	if userID == "" && userName == "" {
		return fmt.Errorf("user id or user is required")
	}
	if userID != "" && userName != "" {
		return fmt.Errorf("user id and user are mutually exclusive")
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}
	body, err := json.Marshal(gatewayAdminUserPasswordRequest{
		UserID: userID, User: userName, Password: password,
	})
	if err != nil {
		return err
	}
	_, err = gatewayAdminRequest("POST", gateway, adminToken, "/v1/admin/users/password", nil, body)
	return err
}

func GatewayAdminGrantsList(gateway, adminToken, userID string) ([]gatewayAdminGrant, error) {
	query := url.Values{}
	if strings.TrimSpace(userID) != "" {
		query.Set("user", strings.TrimSpace(userID))
	}
	raw, err := gatewayAdminRequest("GET", gateway, adminToken, "/v1/admin/grants", query, nil)
	if err != nil {
		return nil, err
	}
	var parsed gatewayAdminGrantsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed.Grants, nil
}

func GatewayAdminGrantCreate(gateway, adminToken string, grant gatewayAdminGrantRequest) error {
	grant.UserID = strings.TrimSpace(grant.UserID)
	grant.User = strings.TrimSpace(grant.User)
	grant.Cluster = strings.TrimSpace(grant.Cluster)
	grant.Instance = strings.TrimSpace(grant.Instance)
	if grant.UserID == "" && grant.User == "" {
		return fmt.Errorf("user id or user is required")
	}
	if grant.Cluster == "" || grant.Instance == "" {
		return fmt.Errorf("cluster and instance are required")
	}
	body, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	_, err = gatewayAdminRequest("POST", gateway, adminToken, "/v1/admin/grants", nil, body)
	return err
}

func GatewayAdminGrantDelete(gateway, adminToken string, id int64) error {
	if id <= 0 {
		return fmt.Errorf("grant id must be positive")
	}
	_, err := gatewayAdminRequest("DELETE", gateway, adminToken, "/v1/admin/grants",
		url.Values{"id": []string{fmt.Sprintf("%d", id)}}, nil)
	return err
}

func runAdminUsers(args []string, servers []Server, configErr error) {
	if len(args) == 0 {
		printAdminUsersUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runAdminUserList(args[1:], servers, configErr)
	case "create":
		runAdminUserCreate(args[1:], servers, configErr)
	case "password":
		runAdminUserPassword(args[1:], servers, configErr)
	case "disable":
		runAdminUserDisable(args[1:], servers, configErr)
	default:
		printAdminUsersUsage()
		os.Exit(1)
	}
}

func runAdminUserPassword(args []string, servers []Server, configErr error) {
	var target, userID, userName string
	var passwordStdin, generatePassword bool
	fs := flag.NewFlagSet("admin user password", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
	fs.StringVar(&userID, "user-id", "", "Gateway user id")
	fs.StringVar(&userName, "user", "", "Gateway username")
	fs.BoolVar(&passwordStdin, "password-stdin", false, "Read the new password from stdin")
	fs.BoolVar(&generatePassword, "generate-password", false, "Generate a random password and print it once")
	fs.Parse(args)
	if strings.TrimSpace(userID) == "" && strings.TrimSpace(userName) == "" {
		fmt.Println("Error: exactly one of --user-id or --user is required")
		os.Exit(1)
	}
	if strings.TrimSpace(userID) != "" && strings.TrimSpace(userName) != "" {
		fmt.Println("Error: --user-id and --user are mutually exclusive")
		os.Exit(1)
	}
	if passwordStdin && generatePassword {
		fmt.Println("Error: --password-stdin and --generate-password are mutually exclusive")
		os.Exit(1)
	}
	gateway, token := applyTarget(target, servers, configErr)
	password, generated, err := readInitialPassword(passwordStdin, generatePassword)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	if err := GatewayAdminUserPassword(gateway, token, userID, userName, password); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	if generated {
		fmt.Printf("generated_password=%s\n", password)
	}
	fmt.Println("password_updated=true")
}

func runAdminUserDisable(args []string, servers []Server, configErr error) {
	var target, userID string
	var disabled bool
	fs := flag.NewFlagSet("admin user disable", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
	fs.StringVar(&userID, "user-id", "", "Gateway user id")
	fs.BoolVar(&disabled, "disabled", true, "Whether the user should be disabled")
	fs.Parse(args)
	if strings.TrimSpace(userID) == "" {
		fmt.Println("Error: --user-id is required")
		os.Exit(1)
	}
	gateway, token := applyTarget(target, servers, configErr)
	if err := GatewayAdminUserDisable(gateway, token, userID, disabled); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("user_id=%s disabled=%t\n", userID, disabled)
}

func runAdminUserList(args []string, servers []Server, configErr error) {
	var target string
	fs := flag.NewFlagSet("admin user list", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
	fs.Parse(args)
	gateway, token := applyTarget(target, servers, configErr)
	users, err := GatewayAdminUsersList(gateway, token)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	for _, user := range users {
		fmt.Printf("user_id=%s name=%s disabled=%t has_password=%t is_admin=%t grants=%d created_at=%s\n",
			user.ID, user.Name, user.Disabled, user.HasPassword, user.IsAdmin, user.GrantCount, user.CreatedAt)
	}
}

func runAdminUserCreate(args []string, servers []Server, configErr error) {
	var target, user, label, expires string
	var passwordStdin, generatePassword bool
	var grantTargets stringListFlag
	fs := flag.NewFlagSet("admin user create", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
	fs.StringVar(&user, "user", "", "New Gateway username")
	fs.BoolVar(&passwordStdin, "password-stdin", false, "Read the new user's password from stdin")
	fs.BoolVar(&generatePassword, "generate-password", false, "Generate a random password and print it once")
	fs.Var(&grantTargets, "grant-target", "Configured target to grant; repeat for multiple exact targets")
	fs.StringVar(&label, "name", "doops user token", "API key label")
	fs.StringVar(&expires, "expires", "", "Optional API key TTL, e.g. 720h")
	fs.Parse(args)

	if strings.TrimSpace(user) == "" {
		fmt.Println("Error: --user is required")
		fs.Usage()
		os.Exit(1)
	}
	user = strings.TrimSpace(user)
	if passwordStdin && generatePassword {
		fmt.Println("Error: --password-stdin and --generate-password are mutually exclusive")
		os.Exit(1)
	}

	gateway, adminToken := applyTarget(target, servers, configErr)
	password, generated, err := readInitialPassword(passwordStdin, generatePassword)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	users, err := GatewayAdminUsersList(gateway, adminToken)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	for _, existing := range users {
		if existing.Name == strings.TrimSpace(user) {
			fmt.Printf("Error: user_exists: Gateway user %q already exists (user_id=%s); refusing duplicate creation\n", user, existing.ID)
			os.Exit(1)
		}
	}

	created, err := GatewayAdminUserCreate(gateway, adminToken, gatewayAdminUserCreateRequest{
		Name:     user,
		Password: password,
		Admin:    false,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	var issued gatewayAdminTokenCreateResponse
	var issuedToken bool
	rollback := func(cause error) {
		if issuedToken && issued.TokenID != "" {
			if revokeErr := GatewayAdminTokenRevoke(gateway, adminToken, issued.TokenID); revokeErr != nil {
				fmt.Printf("Rollback warning: revoke API key %s failed: %v\n", issued.TokenID, revokeErr)
			}
		}
		if disableErr := GatewayAdminUserDisable(gateway, adminToken, created.ID, true); disableErr != nil {
			fmt.Printf("Rollback warning: disable user %s failed: %v\n", created.ID, disableErr)
		}
		fmt.Printf("Error: %v; new user %s was disabled because provisioning did not complete\n", cause, created.ID)
		os.Exit(1)
	}

	grants, err := GatewayAdminGrantsList(gateway, adminToken, created.ID)
	if err != nil {
		rollback(err)
	}
	for _, grant := range grants {
		if err := GatewayAdminGrantDelete(gateway, adminToken, grant.ID); err != nil {
			rollback(fmt.Errorf("remove bootstrap grant %d: %w", grant.ID, err))
		}
	}

	seenScopes := make(map[string]struct{}, len(grantTargets))
	for _, grantTarget := range grantTargets {
		server := findServer(servers, grantTarget)
		if server == nil || strings.TrimSpace(server.Gateway) == "" {
			rollback(fmt.Errorf("grant target %q not found in local Gateway config", grantTarget))
		}
		if gatewayAuthKey(server.Gateway) != gatewayAuthKey(gateway) {
			rollback(fmt.Errorf("grant target %q belongs to a different Gateway", grantTarget))
		}
		scope := strings.TrimSpace(server.Cluster) + "/" + strings.TrimSpace(server.Instance)
		if _, ok := seenScopes[scope]; ok {
			rollback(fmt.Errorf("grant target %q duplicates scope %s", grantTarget, scope))
		}
		seenScopes[scope] = struct{}{}
		if err := GatewayAdminGrantCreate(gateway, adminToken, gatewayAdminGrantRequest{
			UserID:   created.ID,
			Cluster:  server.Cluster,
			Instance: server.Instance,
			Actions:  append([]string(nil), ordinaryGatewayActions...),
		}); err != nil {
			rollback(fmt.Errorf("grant target %q: %w", grantTarget, err))
		}
	}

	issued, err = GatewayAdminTokenCreate(gateway, adminToken, gatewayAdminTokenCreateRequest{
		User:    user,
		Name:    label,
		Expires: expires,
	})
	if err != nil {
		rollback(err)
	}
	issuedToken = true

	if err := verifyCreatedGatewayUser(gateway, adminToken, created.ID, user, grantTargets, servers); err != nil {
		rollback(err)
	}

	fmt.Printf("user_id=%s\n", created.ID)
	fmt.Printf("username=%s\n", user)
	if generated {
		fmt.Printf("generated_password=%s\n", password)
	}
	if len(grantTargets) == 0 {
		fmt.Println("grant=none warning=user has no target permissions")
	} else {
		fmt.Printf("grant_targets=%s\n", strings.Join(grantTargets, ","))
	}
	fmt.Printf("token_type=%s\n", issued.TokenType)
	fmt.Printf("token_id=%s\n", issued.TokenID)
	fmt.Printf("token=%s\n", issued.Token)
	fmt.Println("warning=store this API key now; Gateway only keeps its hash")
}

func readInitialPassword(fromStdin, generate bool) (string, bool, error) {
	if generate {
		raw := make([]byte, 24)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return "", false, fmt.Errorf("generate password: %w", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw), true, nil
	}
	if fromStdin {
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 16*1024+1))
		if err != nil {
			return "", false, fmt.Errorf("read password from stdin: %w", err)
		}
		if len(raw) > 16*1024 {
			return "", false, fmt.Errorf("password input exceeds 16 KiB")
		}
		password := strings.TrimSpace(string(raw))
		if password == "" {
			return "", false, fmt.Errorf("password from stdin is empty")
		}
		return password, false, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", false, fmt.Errorf("password is required; use --password-stdin or --generate-password")
	}
	fmt.Fprint(os.Stderr, "New user password: ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", false, fmt.Errorf("read password: %w", err)
	}
	password := strings.TrimSpace(string(raw))
	if password == "" {
		return "", false, fmt.Errorf("password is empty")
	}
	return password, false, nil
}

func verifyCreatedGatewayUser(gateway, adminToken, userID, userName string, grantTargets stringListFlag, servers []Server) error {
	users, err := GatewayAdminUsersList(gateway, adminToken)
	if err != nil {
		return fmt.Errorf("verify user: %w", err)
	}
	var found *gatewayAdminUserSummary
	for i := range users {
		if users[i].ID == userID {
			found = &users[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("verify user: created user %s not found", userID)
	}
	if found.Name != strings.TrimSpace(userName) || found.Disabled || found.IsAdmin || !found.HasPassword {
		return fmt.Errorf("verify user: unexpected state for %s", userID)
	}

	grants, err := GatewayAdminGrantsList(gateway, adminToken, userID)
	if err != nil {
		return fmt.Errorf("verify grants: %w", err)
	}
	expected := make(map[string]struct{}, len(grantTargets))
	for _, grantTarget := range grantTargets {
		server := findServer(servers, grantTarget)
		if server == nil {
			return fmt.Errorf("verify grant target %q no longer exists in local config", grantTarget)
		}
		expected[strings.TrimSpace(server.Cluster)+"/"+strings.TrimSpace(server.Instance)] = struct{}{}
	}
	for _, grant := range grants {
		if grant.Cluster == "*" || grant.Instance == "*" || containsStringValue(grant.Actions, "*") {
			return fmt.Errorf("verify grants: wildcard grant %d remains", grant.ID)
		}
		scope := strings.TrimSpace(grant.Cluster) + "/" + strings.TrimSpace(grant.Instance)
		if _, ok := expected[scope]; !ok {
			return fmt.Errorf("verify grants: unexpected scope %s", scope)
		}
		if !sameStringSet(grant.Actions, ordinaryGatewayActions) {
			return fmt.Errorf("verify grants: scope %s has unexpected actions", scope)
		}
		delete(expected, scope)
	}
	if len(expected) != 0 {
		return fmt.Errorf("verify grants: missing expected target grant")
	}
	return nil
}

func runAdminGrants(args []string, servers []Server, configErr error) {
	if len(args) == 0 {
		printAdminGrantsUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runAdminGrantList(args[1:], servers, configErr)
	case "add":
		runAdminGrantAdd(args[1:], servers, configErr)
	case "delete", "rm":
		runAdminGrantDelete(args[1:], servers, configErr)
	default:
		printAdminGrantsUsage()
		os.Exit(1)
	}
}

func runAdminGrantList(args []string, servers []Server, configErr error) {
	var target, userID string
	fs := flag.NewFlagSet("admin grant list", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
	fs.StringVar(&userID, "user-id", "", "Optional Gateway user id")
	fs.Parse(args)
	gateway, token := applyTarget(target, servers, configErr)
	grants, err := GatewayAdminGrantsList(gateway, token, userID)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	for _, grant := range grants {
		fmt.Printf("grant_id=%d user_id=%s user=%s cluster=%s instance=%s actions=%s created_at=%s\n",
			grant.ID, grant.UserID, grant.UserName, grant.Cluster, grant.Instance,
			strings.Join(grant.Actions, ","), grant.CreatedAt)
	}
}

func runAdminGrantAdd(args []string, servers []Server, configErr error) {
	var target, scopeTarget, userID, user string
	var actions stringListFlag
	fs := flag.NewFlagSet("admin grant add", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
	fs.StringVar(&scopeTarget, "scope-target", "", "Configured target whose exact cluster/instance should be granted")
	fs.StringVar(&userID, "user-id", "", "Gateway user id")
	fs.StringVar(&user, "user", "", "Gateway username")
	fs.Var(&actions, "action", "Advanced action; omit for standard target access")
	fs.Parse(args)
	if strings.TrimSpace(userID) == "" && strings.TrimSpace(user) == "" {
		fmt.Println("Error: exactly one of --user-id or --user is required")
		os.Exit(1)
	}
	if strings.TrimSpace(userID) != "" && strings.TrimSpace(user) != "" {
		fmt.Println("Error: --user-id and --user are mutually exclusive")
		os.Exit(1)
	}
	gateway, token := applyTarget(target, servers, configErr)
	scope := findServer(servers, scopeTarget)
	if scope == nil {
		fmt.Printf("Error: scope target %q not found\n", scopeTarget)
		os.Exit(1)
	}
	if gatewayAuthKey(scope.Gateway) != gatewayAuthKey(gateway) {
		fmt.Println("Error: --scope-target belongs to a different Gateway")
		os.Exit(1)
	}
	if len(actions) == 0 {
		actions = append(actions, ordinaryGatewayActions...)
	}
	err := GatewayAdminGrantCreate(gateway, token, gatewayAdminGrantRequest{
		UserID: userID, User: user, Cluster: scope.Cluster, Instance: scope.Instance, Actions: actions,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("grant_created=true user=%s user_id=%s cluster=%s instance=%s actions=%s\n",
		user, userID, scope.Cluster, scope.Instance, strings.Join(actions, ","))
}

func runAdminGrantDelete(args []string, servers []Server, configErr error) {
	var target string
	var id int64
	fs := flag.NewFlagSet("admin grant delete", flag.ExitOnError)
	fs.StringVar(&target, "target", "", "Configured gateway target whose admin token should be used")
	fs.Int64Var(&id, "id", 0, "Grant id")
	fs.Parse(args)
	gateway, token := applyTarget(target, servers, configErr)
	if err := GatewayAdminGrantDelete(gateway, token, id); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("grant_id=%d deleted=true\n", id)
}

func printAdminUsersUsage() {
	fmt.Println("Usage: doops admin user list --target <gateway-target>")
	fmt.Println("       doops admin user create --target <gateway-target> --user <username> (--password-stdin|--generate-password) [--grant-target <target>] [--name label] [--expires 720h]")
	fmt.Println("       doops admin user password --target <gateway-target> (--user-id <id>|--user <username>) (--password-stdin|--generate-password)")
	fmt.Println("       doops admin user disable --target <gateway-target> --user-id <id> [--disabled=true|false]")
}

func printAdminGrantsUsage() {
	fmt.Println("Usage: doops admin grant list --target <gateway-target> [--user-id <user-id>]")
	fmt.Println("       doops admin grant add --target <gateway-target> --scope-target <target> (--user-id <id>|--user <username>) [--action <advanced-action> ...]")
	fmt.Println("       doops admin grant delete --target <gateway-target> --id <grant-id>")
}

func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}

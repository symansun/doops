package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The target selects a Gateway login, not an SSH destination. Only emit
// explicitly allowed inventory metadata, even when the caller is an admin.
func runMachines(args []string, servers []Server, configErr error, out io.Writer) error {
	f := flag.NewFlagSet("machines", flag.ContinueOnError)
	f.SetOutput(out)
	target := f.String("target", "", "Configured route selecting the Gateway; lists machines visible to this login")
	var tags stringListFlag
	f.Var(&tags, "tag", "Exact tag; repeat for AND matching")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	gateway, token, err := resolveCredentialGateway(*target, servers, configErr)
	if err != nil {
		return err
	}
	q := url.Values{}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("empty --tag")
		}
		q.Add("tag", tag)
	}
	raw, err := gatewayCredentialRequest(http.MethodGet, gateway, token, "/v1/assets/machines", q, nil)
	if err != nil {
		return err
	}
	var result struct {
		Machines []struct {
			AssetID   string   `json:"asset_id"`
			Name      string   `json:"name"`
			Kind      string   `json:"kind"`
			Address   string   `json:"address"`
			Cluster   string   `json:"cluster"`
			Instance  string   `json:"instance"`
			CreatedAt string   `json:"created_at,omitempty"`
			Tags      []string `json:"tags"`
		} `json:"machines"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Machines == nil {
		return fmt.Errorf("invalid machine inventory response")
	}
	for _, m := range result.Machines {
		if m.Tags == nil {
			return fmt.Errorf("Gateway does not provide machine tags; upgrade it before searching")
		}
	}
	return json.NewEncoder(out).Encode(result)
}

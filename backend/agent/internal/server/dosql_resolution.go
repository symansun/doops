package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/doops/agent/api"
)

// doSQLResolution is the server-side answer to "which credential/database does
// this DoSQL prompt mean". It is written before the model runs and is never
// derived from model output. The model may still call the MCP tool with a
// different credential or a guessed asset id; handleDoSQLTool overrides those
// fields while the resolution file exists.
type doSQLResolution struct {
	CredentialRef   string `json:"credential_ref"`
	Engine          string `json:"engine"`
	DatabaseName    string `json:"database_name"`
	DatabaseAssetID string `json:"database_asset_id,omitempty"`
}

func doSQLResolutionPath(session string) (string, error) {
	workspace, err := resolveWebSession(session)
	if err != nil {
		return "", err
	}
	return filepath.Join(workspace, ".dosql", "resolution.json"), nil
}

func writeDoSQLResolution(session string, resolution doSQLResolution) (func(), error) {
	path, err := doSQLResolutionPath(session)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(resolution)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

func loadDoSQLResolution(session string) (doSQLResolution, bool) {
	path, err := doSQLResolutionPath(session)
	if err != nil {
		return doSQLResolution{}, false
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return doSQLResolution{}, false
	}
	var resolution doSQLResolution
	if json.Unmarshal(encoded, &resolution) != nil || strings.TrimSpace(resolution.CredentialRef) == "" ||
		strings.TrimSpace(resolution.DatabaseName) == "" {
		return doSQLResolution{}, false
	}
	return resolution, true
}

func applyDoSQLResolution(params *doSQLParams) {
	resolution, ok := loadDoSQLResolution(params.SessionID)
	if !ok {
		return
	}
	params.CredentialRef = resolution.CredentialRef
	params.Engine = resolution.Engine
	params.DatabaseName = resolution.DatabaseName
	if resolution.DatabaseAssetID != "" {
		params.DatabaseAssetID = resolution.DatabaseAssetID
	}
}

// resolveDoSQLPromptResolution resolves the database binding without a model.
// It returns a prompt hint and a session resolution; errors are non-fatal for
// the prompt, but the caller must not let the model guess in that case.
func resolveDoSQLPromptResolution(ctx context.Context, bindings []api.AgentCredentialBinding, database string,
	read doSQLSecretReader, discover func(context.Context) (string, string, []doSQLDatabaseCandidate, error)) (doSQLResolution, string, error) {
	bindingCandidate, err := resolveDirectDoSQLBinding(ctx, bindings, database, read)
	if err != nil {
		return doSQLResolution{}, "", err
	}
	resolution := doSQLResolution{
		CredentialRef: bindingCandidate.Binding.Name,
		Engine:        bindingCandidate.Engine,
		DatabaseName:  bindingCandidate.Database,
	}
	hint := fmt.Sprintf("[DoSQL server-side resolution] credential_ref=%s engine=%s database_name=%s",
		resolution.CredentialRef, resolution.Engine, resolution.DatabaseName)

	if discover != nil {
		cluster, instance, candidates, discoveryErr := discover(ctx)
		if discoveryErr == nil {
			var exact, unnamed []doSQLDatabaseCandidate
			for _, candidate := range candidates {
				if candidate.Cluster != cluster || candidate.Instance != instance || candidate.Engine != resolution.Engine ||
					(candidate.Namespace != "" && candidate.Namespace != bindingCandidate.Binding.Namespace) {
					continue
				}
				if candidate.DatabaseName == resolution.DatabaseName {
					exact = append(exact, candidate)
				} else if candidate.DatabaseName == "" {
					unnamed = append(unnamed, candidate)
				}
			}
			switch {
			case len(exact) == 1:
				resolution.DatabaseAssetID = exact[0].DatabaseAssetID
			case len(unnamed) == 1:
				resolution.DatabaseAssetID = unnamed[0].DatabaseAssetID
			}
		}
	}
	if resolution.DatabaseAssetID != "" {
		hint += " database_asset_id=" + resolution.DatabaseAssetID
	}
	hint += "。只能使用这些值调用 mcp_dosql_read query；不要选择其它 credential/engine/database/asset。"
	return resolution, hint, nil
}

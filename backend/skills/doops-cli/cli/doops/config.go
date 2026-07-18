package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Name     string   `json:"name"`
	Aliases  []string `json:"aliases,omitempty"`
	Use      string   `json:"use"`
	Gateway  string   `json:"gateway"`
	Cluster  string   `json:"cluster"`
	Instance string   `json:"instance"`
}

type SessionInfo struct {
	ID        string  `json:"id"`
	Timestamp float64 `json:"timestamp"`
}

type SessionStore struct {
	Path     string
	Sessions map[string]SessionInfo
	mu       sync.Mutex
}

func NewSessionStore() *SessionStore {
	path := defaultSessionStorePath()

	ss := &SessionStore{
		Path:     path,
		Sessions: make(map[string]SessionInfo),
	}
	ss.load()
	return ss
}

func (ss *SessionStore) load() {
	data, err := os.ReadFile(ss.Path)
	if err != nil {
		return
	}

	var rawSessions map[string]SessionInfo
	if err := json.Unmarshal(data, &rawSessions); err != nil {
		return
	}

	now := float64(time.Now().Unix())
	for k, v := range rawSessions {
		if now-v.Timestamp < 3600 {
			ss.Sessions[k] = v
		}
	}
}

func (ss *SessionStore) Save() {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	data, _ := json.MarshalIndent(ss.Sessions, "", "    ")
	os.WriteFile(ss.Path, data, 0644)
}

func (ss *SessionStore) Get(target, sessionName string) string {
	key := target + ":" + sessionName
	info, ok := ss.Sessions[key]
	if !ok {
		return ""
	}
	return info.ID
}

func (ss *SessionStore) Set(target, sessionName, sessionID string) {
	key := target + ":" + sessionName
	ss.mu.Lock()
	ss.Sessions[key] = SessionInfo{
		ID:        sessionID,
		Timestamp: float64(time.Now().Unix()),
	}
	ss.mu.Unlock()
	ss.Save()
}

func (ss *SessionStore) GetOrCreate(target, sessionName string) string {
	sid := ss.Get(target, sessionName)
	if sid == "" {
		// The WebSocket gateway owns remote session mapping. Empty means the
		// next request should let the agent create or resume the remote session.
		return ""
	}
	return sid
}

type LLMConfig struct {
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	BaseURL string `json:"base_url"`
}

type RootConfig struct {
	Servers []Server  `json:"servers"`
	LLM     LLMConfig `json:"llm"`
}

// Global variable to hold loaded LLM config, to avoid changing all signatures at once if lazily needed
// But better to return it properly.

func doopsStateDir() string {
	return filepath.Dir(doopsConfigPath())
}

func ensureDoopsStateDir() string {
	dir := doopsStateDir()
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func defaultSessionStorePath() string {
	return filepath.Join(ensureDoopsStateDir(), "sessions.json")
}

func doopsConfigPath() string {
	if override := strings.TrimSpace(os.Getenv("DOOPS_CONFIG")); override != "" {
		return override
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agent", "skills", "doops", "config.json")
}

// LoadConfig returns Servers and LLMConfig
func LoadConfig() ([]Server, LLMConfig, error) {
	servers, llm, err := loadPath(doopsConfigPath())
	if err != nil {
		return nil, LLMConfig{}, err
	}
	if err := validateGatewayServers(servers); err != nil {
		return nil, LLMConfig{}, fmt.Errorf("invalid Gateway config: %w", err)
	}
	return servers, llm, nil
}

func loadPath(path string) ([]Server, LLMConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, LLMConfig{}, err
	}

	var root RootConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&root); err != nil {
		return nil, LLMConfig{}, fmt.Errorf("failed to parse config at %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, LLMConfig{}, fmt.Errorf("failed to parse config at %s: trailing JSON data", path)
	}
	return root.Servers, root.LLM, nil
}

func LoadServers() ([]Server, error) {
	servers, _, err := LoadConfig()
	return servers, err
}

func GetLLMConfig() LLMConfig {
	_, llm, _ := LoadConfig()
	return llm
}

func saveServers(servers []Server) error {
	configPath := doopsConfigPath()
	var currentLLM LLMConfig
	existingServers, loadedLLM, err := loadPath(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load existing config before save: %w", err)
	}
	if err == nil {
		if err := validateGatewayServers(existingServers); err != nil {
			return fmt.Errorf("refuse to overwrite invalid existing config: %w", err)
		}
		currentLLM = loadedLLM
	}
	if err := validateGatewayServers(servers); err != nil {
		return fmt.Errorf("refuse invalid Gateway config: %w", err)
	}

	root := RootConfig{
		Servers: normalizeServersForSave(servers),
		LLM:     currentLLM,
	}

	data, err := json.MarshalIndent(root, "", "    ")
	if err != nil {
		return err
	}

	os.MkdirAll(filepath.Dir(configPath), 0755)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}
	return os.Chmod(configPath, 0600)
}

func normalizeServersForSave(servers []Server) []Server {
	normalized := make([]Server, 0, len(servers))
	for _, server := range servers {
		server.Aliases = normalizeAliases(server.Aliases)
		normalized = append(normalized, server)
	}
	return normalized
}

func normalizeAliases(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			alias := strings.TrimSpace(part)
			if alias == "" || seen[alias] {
				continue
			}
			seen[alias] = true
			out = append(out, alias)
		}
	}
	return out
}

func validateGatewayServers(servers []Server) error {
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			return fmt.Errorf("target name is empty")
		}
		if strings.TrimSpace(server.Gateway) == "" || strings.TrimSpace(server.Cluster) == "" || strings.TrimSpace(server.Instance) == "" {
			return fmt.Errorf("target %q has an incomplete Gateway route", name)
		}
	}
	return nil
}

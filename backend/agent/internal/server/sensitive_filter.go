package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/user/doops/agent/api"
)

const (
	sensitiveScopeAll     = "all"
	sensitiveScopePrompt  = "prompt"
	sensitiveScopeCommand = "command"

	sensitiveFilterVersion  = 1
	sensitiveRuleLimit      = 500
	sensitiveWhitelistLimit = 500
	sensitiveTextMaxRunes   = 128
	sensitiveNameMaxBytes   = 128
)

var (
	errSensitiveRuleNotFound      = errors.New("sensitive rule not found")
	errSensitiveRuleLimit         = errors.New("sensitive rule limit reached")
	errSensitiveWhitelistNotFound = errors.New("sensitive whitelist entry not found")
	errSensitiveWhitelistExists   = errors.New("sensitive whitelist entry already exists")
	errSensitiveWhitelistLimit    = errors.New("sensitive whitelist limit reached")
	errSensitiveStorage           = errors.New("sensitive filter storage is unavailable")
	errSensitiveInvalid           = errors.New("invalid sensitive filter input")
)

// SensitiveRule is one administrator-managed match rule. Scope decides which
// user-provided text it applies to; the same engine serves prompts and commands.
type SensitiveRule struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Scope     string    `json:"scope"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// match is the normalized form used for matching. It is never serialized.
	match string
}

// SensitiveWhitelistEntry exempts a user or a whole target from the filter.
// An empty Instance on a target entry means every instance in that cluster.
type SensitiveWhitelistEntry struct {
	Kind      string    `json:"kind"`
	UserID    string    `json:"user_id,omitempty"`
	Cluster   string    `json:"cluster,omitempty"`
	Instance  string    `json:"instance,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type SensitiveFilterView struct {
	Rules     []SensitiveRule           `json:"rules"`
	Whitelist []SensitiveWhitelistEntry `json:"whitelist"`
}

type SensitiveRuleInput struct {
	Text  string `json:"text"`
	Scope string `json:"scope"`
}

type SensitiveWhitelistInput struct {
	Kind     string `json:"kind"`
	UserID   string `json:"user_id"`
	Cluster  string `json:"cluster"`
	Instance string `json:"instance"`
}

type sensitiveFilterFile struct {
	Version   int                       `json:"version"`
	Rules     []SensitiveRule           `json:"rules"`
	Whitelist []SensitiveWhitelistEntry `json:"whitelist"`
}

type sensitiveFilterState struct {
	file sensitiveFilterFile
}

func (s *GatewayStore) sensitiveFilterFilePath() string {
	dir := strings.TrimSpace(s.dbDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "sensitive-filter.json")
}

func (s *GatewayStore) loadSensitiveFilterLocked() error {
	if s.sensitiveFilter != nil {
		return nil
	}
	path := s.sensitiveFilterFilePath()
	if path == "" {
		s.sensitiveFilter = &sensitiveFilterState{}
		return nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.sensitiveFilter = &sensitiveFilterState{}
		return nil
	}
	if err != nil {
		return err
	}
	var file sensitiveFilterFile
	if strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &file); err != nil {
			return fmt.Errorf("parse sensitive filter: %w", err)
		}
	}
	if file.Version == 0 {
		file.Version = sensitiveFilterVersion
	}
	if file.Version != sensitiveFilterVersion {
		return fmt.Errorf("unsupported sensitive filter version %d", file.Version)
	}
	normalizeSensitiveFilter(&file)
	s.sensitiveFilter = &sensitiveFilterState{file: file}
	return nil
}

func (s *GatewayStore) saveSensitiveFilterLocked() error {
	path := s.sensitiveFilterFilePath()
	if path == "" || s.sensitiveFilter == nil {
		return errSensitiveStorage
	}
	raw, err := json.MarshalIndent(s.sensitiveFilter.file, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return writeFileAtomic0600(path, raw)
}

// ListSensitiveFilter returns a snapshot for the admin console.
func (s *GatewayStore) ListSensitiveFilter() (SensitiveFilterView, error) {
	s.sensitiveMu.Lock()
	defer s.sensitiveMu.Unlock()
	if err := s.loadSensitiveFilterLocked(); err != nil {
		return SensitiveFilterView{}, err
	}
	rules := append([]SensitiveRule(nil), s.sensitiveFilter.file.Rules...)
	whitelist := append([]SensitiveWhitelistEntry(nil), s.sensitiveFilter.file.Whitelist...)
	if rules == nil {
		rules = []SensitiveRule{}
	}
	if whitelist == nil {
		whitelist = []SensitiveWhitelistEntry{}
	}
	return SensitiveFilterView{Rules: rules, Whitelist: whitelist}, nil
}

// AddSensitiveRules validates the whole batch before changing anything. Exact
// duplicates (case-insensitive text plus scope) are reported as skipped.
func (s *GatewayStore) AddSensitiveRules(inputs []SensitiveRuleInput) ([]SensitiveRule, []string, error) {
	if len(inputs) == 0 {
		return nil, nil, fmt.Errorf("%w: no rules provided", errSensitiveInvalid)
	}
	s.sensitiveMu.Lock()
	defer s.sensitiveMu.Unlock()
	if err := s.loadSensitiveFilterLocked(); err != nil {
		return nil, nil, err
	}
	current := s.sensitiveFilter.file.Rules
	if len(current) >= sensitiveRuleLimit {
		return nil, nil, errSensitiveRuleLimit
	}
	now := time.Now().UTC()
	seen := make(map[string]bool, len(current)+len(inputs))
	for _, rule := range current {
		seen[sensitiveRuleKey(rule.Text, rule.Scope)] = true
	}
	created := make([]SensitiveRule, 0, len(inputs))
	skipped := make([]string, 0)
	for index, input := range inputs {
		rule := SensitiveRule{
			Text:      input.Text,
			Scope:     input.Scope,
			Enabled:   true,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if !normalizeSensitiveRule(&rule, now) {
			return nil, nil, fmt.Errorf("%w: invalid rule at index %d", errSensitiveInvalid, index)
		}
		key := sensitiveRuleKey(rule.Text, rule.Scope)
		if seen[key] {
			skipped = append(skipped, rule.Text)
			continue
		}
		if len(current)+len(created) >= sensitiveRuleLimit {
			return nil, nil, errSensitiveRuleLimit
		}
		seen[key] = true
		created = append(created, rule)
	}
	if len(created) == 0 {
		return []SensitiveRule{}, skipped, nil
	}
	s.sensitiveFilter.file.Rules = append(current, created...)
	if err := s.saveSensitiveFilterLocked(); err != nil {
		s.sensitiveFilter.file.Rules = current
		return nil, nil, err
	}
	return created, skipped, nil
}

func (s *GatewayStore) SetSensitiveRuleEnabled(id string, enabled bool) (SensitiveRule, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SensitiveRule{}, errSensitiveRuleNotFound
	}
	s.sensitiveMu.Lock()
	defer s.sensitiveMu.Unlock()
	if err := s.loadSensitiveFilterLocked(); err != nil {
		return SensitiveRule{}, err
	}
	rules := s.sensitiveFilter.file.Rules
	for index := range rules {
		if rules[index].ID != id {
			continue
		}
		previous := rules[index]
		rules[index].Enabled = enabled
		rules[index].UpdatedAt = time.Now().UTC()
		if err := s.saveSensitiveFilterLocked(); err != nil {
			rules[index] = previous
			return SensitiveRule{}, err
		}
		return rules[index], nil
	}
	return SensitiveRule{}, errSensitiveRuleNotFound
}

func (s *GatewayStore) DeleteSensitiveRule(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errSensitiveRuleNotFound
	}
	s.sensitiveMu.Lock()
	defer s.sensitiveMu.Unlock()
	if err := s.loadSensitiveFilterLocked(); err != nil {
		return err
	}
	rules := s.sensitiveFilter.file.Rules
	for index := range rules {
		if rules[index].ID != id {
			continue
		}
		next := make([]SensitiveRule, 0, len(rules)-1)
		next = append(next, rules[:index]...)
		next = append(next, rules[index+1:]...)
		s.sensitiveFilter.file.Rules = next
		if err := s.saveSensitiveFilterLocked(); err != nil {
			s.sensitiveFilter.file.Rules = rules
			return err
		}
		return nil
	}
	return errSensitiveRuleNotFound
}

func (s *GatewayStore) AddSensitiveWhitelist(input SensitiveWhitelistInput) (SensitiveWhitelistEntry, error) {
	entry := SensitiveWhitelistEntry{
		Kind:     strings.ToLower(strings.TrimSpace(input.Kind)),
		UserID:   strings.TrimSpace(input.UserID),
		Cluster:  strings.TrimSpace(input.Cluster),
		Instance: strings.TrimSpace(input.Instance),
	}
	if !validSensitiveWhitelistEntry(entry) {
		return SensitiveWhitelistEntry{}, fmt.Errorf("%w: invalid whitelist entry", errSensitiveInvalid)
	}
	s.sensitiveMu.Lock()
	defer s.sensitiveMu.Unlock()
	if err := s.loadSensitiveFilterLocked(); err != nil {
		return SensitiveWhitelistEntry{}, err
	}
	key := sensitiveWhitelistKey(entry.Kind, entry.UserID, entry.Cluster, entry.Instance)
	for _, existing := range s.sensitiveFilter.file.Whitelist {
		if sensitiveWhitelistKey(existing.Kind, existing.UserID, existing.Cluster, existing.Instance) == key {
			return SensitiveWhitelistEntry{}, errSensitiveWhitelistExists
		}
	}
	if len(s.sensitiveFilter.file.Whitelist) >= sensitiveWhitelistLimit {
		return SensitiveWhitelistEntry{}, errSensitiveWhitelistLimit
	}
	entry.CreatedAt = time.Now().UTC()
	s.sensitiveFilter.file.Whitelist = append(s.sensitiveFilter.file.Whitelist, entry)
	if err := s.saveSensitiveFilterLocked(); err != nil {
		s.sensitiveFilter.file.Whitelist = s.sensitiveFilter.file.Whitelist[:len(s.sensitiveFilter.file.Whitelist)-1]
		return SensitiveWhitelistEntry{}, err
	}
	return entry, nil
}

func (s *GatewayStore) DeleteSensitiveWhitelist(input SensitiveWhitelistInput) error {
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	userID := strings.TrimSpace(input.UserID)
	cluster := strings.TrimSpace(input.Cluster)
	instance := strings.TrimSpace(input.Instance)
	if kind != "user" && kind != "target" {
		return errSensitiveWhitelistNotFound
	}
	s.sensitiveMu.Lock()
	defer s.sensitiveMu.Unlock()
	if err := s.loadSensitiveFilterLocked(); err != nil {
		return err
	}
	entries := s.sensitiveFilter.file.Whitelist
	for index, entry := range entries {
		if entry.Kind != kind || entry.UserID != userID || entry.Cluster != cluster || entry.Instance != instance {
			continue
		}
		next := make([]SensitiveWhitelistEntry, 0, len(entries)-1)
		next = append(next, entries[:index]...)
		next = append(next, entries[index+1:]...)
		s.sensitiveFilter.file.Whitelist = next
		if err := s.saveSensitiveFilterLocked(); err != nil {
			s.sensitiveFilter.file.Whitelist = entries
			return err
		}
		return nil
	}
	return errSensitiveWhitelistNotFound
}

// MatchSensitiveText returns the first matching rule ID. A whitelisted user or
// target always wins. A damaged filter file is logged and treated as empty so a
// moderation setting cannot take the Gateway's AI and shell paths offline.
func (s *GatewayStore) MatchSensitiveText(userID, cluster, instance, text, scope string) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	s.sensitiveMu.Lock()
	defer s.sensitiveMu.Unlock()
	if err := s.loadSensitiveFilterLocked(); err != nil {
		log.Printf("[gateway] sensitive_filter_load_failed: %v", err)
		return "", false
	}
	for _, entry := range s.sensitiveFilter.file.Whitelist {
		switch entry.Kind {
		case "user":
			if entry.UserID == userID {
				return "", false
			}
		case "target":
			if entry.Cluster == cluster && (entry.Instance == "" || entry.Instance == instance) {
				return "", false
			}
		}
	}
	haystack := normalizeSensitiveMatchText(text)
	for _, rule := range s.sensitiveFilter.file.Rules {
		if !rule.Enabled || !sensitiveScopeMatches(rule.Scope, scope) {
			continue
		}
		if rule.match != "" && strings.Contains(haystack, rule.match) {
			return rule.ID, true
		}
	}
	return "", false
}

type sensitiveFilterSegment struct {
	text  string
	scope string
}

// sensitiveFilterSegmentsForTool extracts only user-originated text from a
// client tool call. Structured tools and internal calls are outside this
// boundary. A dosql prompt carries SQL outside its instruction, so that SQL is
// treated as command text instead of bypassing the filter.
func sensitiveFilterSegmentsForTool(tool string, arguments json.RawMessage) []sensitiveFilterSegment {
	switch tool {
	case "doops_agent_prompt":
		var params api.AgentPromptParams
		if json.Unmarshal(arguments, &params) != nil {
			return nil
		}
		segments := make([]sensitiveFilterSegment, 0, 3)
		if strings.TrimSpace(params.Instruction) != "" {
			segments = append(segments, sensitiveFilterSegment{text: params.Instruction, scope: sensitiveScopePrompt})
		}
		for _, sql := range []string{params.SQL, params.SQLVerification} {
			if strings.TrimSpace(sql) != "" {
				segments = append(segments, sensitiveFilterSegment{text: sql, scope: sensitiveScopeCommand})
			}
		}
		return segments
	case "doops_shell", "doops_bg":
		var params api.ShellParams
		if json.Unmarshal(arguments, &params) != nil {
			return nil
		}
		return []sensitiveFilterSegment{{text: params.Command, scope: sensitiveScopeCommand}}
	case "doops_ssh":
		var params api.SSHParams
		if json.Unmarshal(arguments, &params) != nil {
			return nil
		}
		return []sensitiveFilterSegment{{text: strings.Join(params.Command, " "), scope: sensitiveScopeCommand}}
	default:
		return nil
	}
}

func normalizeSensitiveFilter(file *sensitiveFilterFile) {
	now := time.Now().UTC()
	rules := make([]SensitiveRule, 0, len(file.Rules))
	seen := make(map[string]bool, len(file.Rules))
	for _, rule := range file.Rules {
		if !normalizeSensitiveRule(&rule, now) {
			continue
		}
		key := sensitiveRuleKey(rule.Text, rule.Scope)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(rules) >= sensitiveRuleLimit {
			break
		}
		rules = append(rules, rule)
	}
	sort.SliceStable(rules, func(left, right int) bool {
		return rules[left].CreatedAt.Before(rules[right].CreatedAt)
	})
	file.Rules = rules

	whitelist := make([]SensitiveWhitelistEntry, 0, len(file.Whitelist))
	whitelistSeen := make(map[string]bool, len(file.Whitelist))
	for _, entry := range file.Whitelist {
		entry.Kind = strings.ToLower(strings.TrimSpace(entry.Kind))
		entry.UserID = strings.TrimSpace(entry.UserID)
		entry.Cluster = strings.TrimSpace(entry.Cluster)
		entry.Instance = strings.TrimSpace(entry.Instance)
		if !validSensitiveWhitelistEntry(entry) {
			continue
		}
		key := sensitiveWhitelistKey(entry.Kind, entry.UserID, entry.Cluster, entry.Instance)
		if whitelistSeen[key] {
			continue
		}
		whitelistSeen[key] = true
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = now
		}
		if len(whitelist) >= sensitiveWhitelistLimit {
			break
		}
		whitelist = append(whitelist, entry)
	}
	file.Whitelist = whitelist
}

func normalizeSensitiveRule(rule *SensitiveRule, now time.Time) bool {
	rule.ID = strings.TrimSpace(rule.ID)
	rawText := strings.TrimSpace(rule.Text)
	if rawText == "" || !validSensitiveText(rawText) {
		return false
	}
	rule.Text = strings.Join(strings.Fields(rawText), " ")
	rule.Scope = normalizeSensitiveScope(rule.Scope)
	if rule.Text == "" || rule.Scope == "" || utf8.RuneCountInString(rule.Text) > sensitiveTextMaxRunes {
		return false
	}
	if rule.ID == "" {
		rule.ID = "sw_" + randomHex(10)
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = rule.CreatedAt
	}
	rule.match = normalizeSensitiveMatchText(rule.Text)
	return true
}

func normalizeSensitiveScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", sensitiveScopeAll:
		return sensitiveScopeAll
	case sensitiveScopePrompt:
		return sensitiveScopePrompt
	case sensitiveScopeCommand:
		return sensitiveScopeCommand
	default:
		return ""
	}
}

func validSensitiveText(text string) bool {
	for _, char := range text {
		if char == '\r' || char == '\n' || char == '\t' || unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validSensitiveWhitelistEntry(entry SensitiveWhitelistEntry) bool {
	switch entry.Kind {
	case "user":
		return validSensitiveName(entry.UserID) && entry.Cluster == "" && entry.Instance == ""
	case "target":
		return entry.UserID == "" && validSensitiveName(entry.Cluster) &&
			(entry.Instance == "" || validSensitiveName(entry.Instance))
	default:
		return false
	}
}

func validSensitiveName(value string) bool {
	if value == "" || len(value) > sensitiveNameMaxBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func sensitiveRuleKey(text, scope string) string {
	return normalizeSensitiveMatchText(text) + "\x00" + scope
}

func sensitiveWhitelistKey(kind, userID, cluster, instance string) string {
	return kind + "\x00" + userID + "\x00" + cluster + "\x00" + instance
}

func sensitiveScopeMatches(ruleScope, wanted string) bool {
	return ruleScope == sensitiveScopeAll || ruleScope == wanted
}

func normalizeSensitiveMatchText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

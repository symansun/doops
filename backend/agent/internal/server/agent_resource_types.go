package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	agentResourceSyncProtocol  = "targetManifestV1"
	agentResourceObserveMethod = "agent/resources/observe"
	agentResourceApplyMethod   = "agent/resources/apply"
	targetManifestMaxBytes     = 256 << 10
	agentObservationMaxBytes   = 2 << 20
)

var (
	errTargetManifestUnavailable = errors.New("target_manifest_unavailable")
	errResourceSyncDisconnected  = errors.New("target_manifest_agent_offline")
	errResourceSyncUnsupported   = errors.New("target_manifest_agent_unsupported")
	errResourceSyncMigration     = errors.New("target_manifest_migration_required")
	resourceIdentityToken        = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)
)

type agentResourceObservation struct {
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Identity string `json:"identity,omitempty"`
	Count    int    `json:"count,omitempty"`
}

type agentResourceSnapshot struct {
	Mode                string                     `json:"mode"`
	KubernetesAvailable bool                       `json:"kubernetes_available"`
	Complete            []string                   `json:"complete"`
	Resources           []agentResourceObservation `json:"resources"`
}

type targetManifestTarget struct {
	Cluster             string `json:"cluster"`
	Instance            string `json:"instance"`
	Mode                string `json:"mode,omitempty"`
	KubernetesAvailable *bool  `json:"kubernetes_available,omitempty"`
}

type targetManifestResource struct {
	Ref            string   `json:"ref"`
	Kind           string   `json:"kind"`
	Name           string   `json:"name"`
	State          string   `json:"state"`
	Source         []string `json:"source"`
	ObservedAt     string   `json:"observed_at,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	CanRegister    bool     `json:"can_register,omitempty"`
	Count          int      `json:"count,omitempty"`
	CredentialType string   `json:"credential_type,omitempty"`
	Uses           []string `json:"uses,omitempty"`
}

type targetManifest struct {
	Target      targetManifestTarget     `json:"target"`
	Revision    uint64                   `json:"revision"`
	Digest      string                   `json:"digest"`
	GeneratedAt string                   `json:"generated_at"`
	Resources   []targetManifestResource `json:"resources"`
	Counts      map[string]int           `json:"counts,omitempty"`
	Truncated   bool                     `json:"truncated,omitempty"`
}

type targetManifestStatus struct {
	Manifest        targetManifest `json:"manifest"`
	LastObservedAt  string         `json:"last_observed_at,omitempty"`
	AppliedRevision uint64         `json:"applied_revision"`
	Online          bool           `json:"online"`
	Supported       bool           `json:"supported"`
}

func agentResourceDigest(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (m targetManifest) contentDigest() string {
	m.Revision, m.Digest, m.GeneratedAt = 0, "", ""
	m.Resources = slices.Clone(m.Resources)
	for i := range m.Resources {
		m.Resources[i].ObservedAt = ""
	}
	return agentResourceDigest(m)
}

func validAgentResourceMode(mode string) bool {
	return mode == "kubernetes" || mode == "docker" || mode == "linux"
}

func validObservationKind(kind string) bool {
	switch kind {
	case "machine", "cluster", "namespace", "container", "site", "database":
		return true
	default:
		return false
	}
}

// Credential creation accepts human-readable names. Keep the stored spelling, but
// never move inline secrets, connection strings or control characters into metadata.
var manifestSensitiveName = regexp.MustCompile(`(?i)@(?:tcp|unix)\(|\b(?:postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?|redis(?:s)?|sqlserver|mssql|oracle|amqps?)://|\bjdbc:[a-z0-9]+:|://[^/\s]+@|\b(?:pwd|passwd|password|token|api[_-]?key|secret|authorization|cookie)["']?\s*[:=]|\beyJ[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]+`)

func validManifestDisplayName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.ValidString(name) && inventoryMetadata(name) && !manifestSensitiveName.MatchString(name)
}

func validDeclaredResourceName(kind, name string) bool {
	if !validManifestDisplayName(name) {
		return false
	}
	switch kind {
	case "credential":
		return true // Creation accepts human-readable names; storage limits vary by engine.
	case "site", "database", "image":
		return catalogText(name, false)
	case "namespace":
		return inventoryNamespacePattern.MatchString(name)
	default:
		return len(name) <= 256 // Machine, business system and cluster inventory names.
	}
}

// Escape UTF-8 bytes outside the existing ref alphabet, including '%' itself.
// The dot-only names must survive path normalization as single segments too.
var credentialSegmentEscapes = strings.NewReplacer("+", "%20", "%3A", ":", "~", "%7E")

func credentialManifestNameSegment(name string) string {
	if name == "." || name == ".." {
		return strings.Repeat("%2E", len(name))
	}
	return credentialSegmentEscapes.Replace(url.QueryEscape(name))
}

// validManifestRef calls this for every path segment. Decode once and check
// the canonical spelling so a literal percent sequence cannot alias another name.
func validResourceMetadataToken(segment string) bool {
	name, err := url.PathUnescape(segment)
	return err == nil && validManifestDisplayName(name) && credentialManifestNameSegment(name) == segment
}

func validObservationToken(value string) bool {
	return resourceIdentityToken.MatchString(value) && inventoryMetadata(value)
}

// Only database probes have hierarchical keys: namespace/service/engine.
func validObservationKey(kind, key string) bool {
	if !strings.Contains(key, "/") {
		return validObservationToken(key)
	}
	parts := strings.Split(key, "/")
	return kind == "database" && len(key) <= 128 && len(parts) == 3 &&
		inventoryNamespacePattern.MatchString(parts[0]) && validObservationToken(parts[1]) && catalogHost(parts[1]) &&
		inventoryNamespacePattern.MatchString(parts[2]) && inventoryMetadata(key)
}

func (r agentResourceObservation) valid() bool {
	return validObservationKind(r.Kind) && validObservationKey(r.Kind, r.Key) && validObservationToken(r.Name) &&
		(r.Identity == "" || validObservationToken(r.Identity)) && r.Count >= 0 && (r.Kind == "container" || r.Count == 0)
}

func (s *agentResourceSnapshot) canonicalize() {
	slices.Sort(s.Complete)
	slices.SortFunc(s.Resources, func(a, b agentResourceObservation) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Key, b.Key)
	})
}

func (s agentResourceSnapshot) valid() bool {
	if !validAgentResourceMode(s.Mode) || len(s.Resources) > 10000 {
		return false
	}
	complete := make(map[string]bool, len(s.Complete))
	for _, kind := range s.Complete {
		if !validObservationKind(kind) || complete[kind] || !s.KubernetesAvailable && (kind == "cluster" || kind == "namespace" || kind == "database") {
			return false
		}
		complete[kind] = true
	}
	keys := make(map[string]bool, len(s.Resources))
	for _, resource := range s.Resources {
		key := resource.Kind + "/" + resource.Key
		if keys[key] || !resource.valid() {
			return false
		}
		keys[key] = true
	}
	return true
}

func decodeResourceSync(data []byte, limit int, value any) error {
	if len(data) > limit {
		return errors.New("resource_sync_message_too_large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("resource_sync_message_invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("resource_sync_message_invalid")
	}
	return nil
}

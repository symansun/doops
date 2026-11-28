package server

import (
	"encoding/json"
	"time"
)

type CredentialScope string

const (
	CredentialScopePersonal CredentialScope = "personal"
	CredentialScopePlatform CredentialScope = "platform"
)

type CredentialType string

const (
	CredentialTypeRegistry       CredentialType = "registry"
	CredentialTypeTLS            CredentialType = "tls"
	CredentialTypeOpaque         CredentialType = "opaque"
	CredentialTypeHelmRepository CredentialType = "helmRepository"
	CredentialTypeGitToken       CredentialType = "gitToken"
	// CredentialTypeModelProvider is the doagent model provider configuration a
	// target Agent receives from the Gateway instead of a mounted settings file.
	CredentialTypeModelProvider CredentialType = "modelProvider"
)

type CredentialState string

const (
	CredentialStateActive   CredentialState = "active"
	CredentialStateRevoking CredentialState = "revoking"
	CredentialStateRevoked  CredentialState = "revoked"
)

type CredentialVersionState string

const (
	CredentialVersionStaged     CredentialVersionState = "staged"
	CredentialVersionActive     CredentialVersionState = "active"
	CredentialVersionSuperseded CredentialVersionState = "superseded"
	CredentialVersionRevoked    CredentialVersionState = "revoked"
)

type CredentialUse string

const (
	CredentialUseImagePull     CredentialUse = "imagePull"
	CredentialUseGitCheckout   CredentialUse = "gitCheckout"
	CredentialUseHelmPull      CredentialUse = "helmPull"
	CredentialUseTLS           CredentialUse = "tls"
	CredentialUseOpaqueSecret  CredentialUse = "opaqueSecret"
	CredentialUseDatabase      CredentialUse = "database"
	CredentialUseModelProvider CredentialUse = "modelProvider"
)

type CredentialResource struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Scope     CredentialScope `json:"scope"`
	OwnerID   string          `json:"owner_id,omitempty"`
	Type      CredentialType  `json:"type"`
	State     CredentialState `json:"state"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	RevokedAt *time.Time      `json:"revoked_at,omitempty"`
}

type CredentialVersion struct {
	ID            string                 `json:"id"`
	CredentialID  string                 `json:"credential_id"`
	PayloadDigest string                 `json:"payload_digest"`
	State         CredentialVersionState `json:"state"`
	CreatedBy     string                 `json:"created_by"`
	CreatedAt     time.Time              `json:"created_at"`
	PromotedAt    *time.Time             `json:"promoted_at,omitempty"`
	RevokedAt     *time.Time             `json:"revoked_at,omitempty"`
}

type CredentialGrant struct {
	ID           string               `json:"id"`
	CredentialID string               `json:"credential_id"`
	Scope        CredentialGrantScope `json:"scope"`
	State        CredentialState      `json:"state"`
	CreatedBy    string               `json:"created_by"`
	CreatedAt    time.Time            `json:"created_at"`
	RevokedAt    *time.Time           `json:"revoked_at,omitempty"`
}

type CredentialBundle struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Scope     CredentialScope        `json:"scope"`
	OwnerID   string                 `json:"owner_id,omitempty"`
	State     CredentialState        `json:"state"`
	CreatedBy string                 `json:"created_by"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Items     []CredentialBundleItem `json:"items,omitempty"`
}

type CredentialBundleItem struct {
	CredentialID string `json:"credential_id"`
}

type CredentialBundleCreateRequest struct {
	Name      string
	Scope     CredentialScope
	OwnerID   string
	CreatedBy string
	Items     []CredentialBundleItem
}

type CredentialCreateRequest struct {
	Name      string
	Scope     CredentialScope
	Type      CredentialType
	OwnerID   string
	CreatedBy string
}

type CredentialListFilter struct {
	ActorID string
	Scope   CredentialScope
}

type CredentialListEntry struct {
	CredentialResource
	Grants []CredentialGrant `json:"grants"`
}

type CredentialVersionPutRequest struct {
	CredentialID string
	Payload      json.RawMessage
	CreatedBy    string
	Activate     bool
}

type CredentialGrantCreateRequest struct {
	CredentialID string
	Scope        CredentialGrantScope
	CreatedBy    string
}

type CredentialUseRequest struct {
	ActorID      string
	CredentialID string
	Credential   string
	Scope        CredentialGrantScope
}

type AuthorizedCredentialUse struct {
	Resource CredentialResource `json:"resource"`
	Version  CredentialVersion  `json:"version"`
	Grant    CredentialGrant    `json:"grant"`
}

type CredentialVerification struct {
	ID           string                       `json:"id"`
	CredentialID string                       `json:"credential_id"`
	VersionID    string                       `json:"version_id"`
	GrantID      string                       `json:"grant_id"`
	Use          CredentialUse                `json:"use"`
	Request      CredentialMaterializeRequest `json:"request"`
	Evidence     CredentialMaterialization    `json:"evidence"`
	Status       string                       `json:"status"`
	VerifiedAt   time.Time                    `json:"verified_at"`
}

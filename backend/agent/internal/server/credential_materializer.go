package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

type CredentialMaterializeRequest struct {
	SessionID      string               `json:"session_id"`
	Cluster        string               `json:"cluster,omitempty"`
	Instance       string               `json:"instance,omitempty"`
	CredentialID   string               `json:"credential_id"`
	GrantID        string               `json:"grant_id"`
	VersionID      string               `json:"version_id"`
	CredentialType CredentialType       `json:"credential_type"`
	Scope          CredentialGrantScope `json:"scope"`
	RequiredKeys   []string             `json:"required_keys,omitempty"`
	Payload        json.RawMessage      `json:"payload"`
	// AuthfileOnly delivers the credential to the Agent's own OCI tool
	// configuration file and leaves the Kubernetes Secret alone. The Gateway
	// uses it to restore that file after an Agent restart, where the Secret
	// still exists but the file the Agent wrote is gone with the Pod.
	AuthfileOnly bool `json:"authfile_only,omitempty"`
	// RuntimeEnvOnly materializes an opaque credential into an ephemeral
	// process-private 0600 JSON file outside the session workspace without
	// creating or touching any Kubernetes Secret.
	RuntimeEnvOnly bool `json:"runtime_env_only,omitempty"`
}

type CredentialCleanupRequest struct {
	CredentialType CredentialType         `json:"credential_type,omitempty"`
	SessionID      string                 `json:"session_id"`
	CredentialID   string                 `json:"credential_id"`
	GrantID        string                 `json:"grant_id"`
	VersionID      string                 `json:"version_id"`
	AnyVersion     bool                   `json:"any_version,omitempty"`
	Namespace      string                 `json:"namespace"`
	Workload       CredentialPlanWorkload `json:"workload,omitempty"`
	// AuthfileOnly limits the cleanup to the Agent's own OCI authfile. The
	// Kubernetes Secret is the kubelet's pull credential and outlives the Pod,
	// so a failed reconnect delivery must not remove it: a workload pulling
	// with that Secret would lose its registry access.
	AuthfileOnly bool `json:"authfile_only,omitempty"`
	// RuntimeEnvOnly limits cleanup to the ephemeral 0600 runtime opaque file.
	RuntimeEnvOnly bool `json:"runtime_env_only,omitempty"`
}

type credentialTargetSnapshot struct {
	Secret            []byte
	PreviousVersionID string
	WorkloadKind      string
	WorkloadName      string
	ImagePullNames    []string
}

func materializeCredential(ctx context.Context, req CredentialMaterializeRequest) (CredentialMaterialization, error) {
	if strings.TrimSpace(req.CredentialID) == "" || strings.TrimSpace(req.GrantID) == "" ||
		strings.TrimSpace(req.VersionID) == "" ||
		!validCredentialType(req.CredentialType) ||
		validateCredentialGrantScope(req.Scope) != nil {
		return CredentialMaterialization{}, errors.New("credential_request_invalid")
	}
	if err := validateCredentialPayload(req.CredentialType, req.Payload); err != nil {
		return CredentialMaterialization{}, ErrCredentialPayloadInvalid
	}

	name := credentialSecretName(req.CredentialID, req.GrantID)
	result := CredentialMaterialization{
		CredentialID: req.CredentialID,
		VersionID:    req.VersionID,
		ResourceName: name,
		Namespace:    req.Scope.Namespace,
		Status:       "verified",
	}

	var secretType string
	var secretData map[string]string
	// pendingRegistryAuth holds a registry authfile delivery that must happen
	// only after the Secret verifies.
	var pendingRegistryAuth *registryAuthPendingDelivery
	switch req.CredentialType {
	case CredentialTypeRegistry:
		if req.Scope.Use != CredentialUseImagePull {
			return CredentialMaterialization{}, errors.New("credential_use_mismatch")
		}
		var payload struct {
			Server   string `json:"server"`
			Username string `json:"username"`
			Password string `json:"password"`
			Email    string `json:"email,omitempty"`
		}
		_ = json.Unmarshal(req.Payload, &payload)
		auth := base64.StdEncoding.EncodeToString([]byte(payload.Username + ":" + payload.Password))
		dockerConfig, _ := json.Marshal(map[string]any{"auths": map[string]any{
			payload.Server: map[string]string{
				"username": payload.Username,
				"password": payload.Password,
				"email":    payload.Email,
				"auth":     auth,
			},
		}})
		secretType = "kubernetes.io/dockerconfigjson"
		secretData = map[string]string{".dockerconfigjson": string(dockerConfig)}
		if req.Scope.ImageRepository != "" || req.Scope.ImageDigest != "" {
			digest, err := verifyRegistryManifest(ctx, payload.Server, payload.Username, payload.Password,
				req.Scope.ImageRepository, req.Scope.ImageDigest)
			if err != nil {
				return CredentialMaterialization{}, err
			}
			result.Digest = digest
		}
		// The Secret is what the kubelet pulls with; the Agent's own OCI tools
		// read an authfile. The file is written after the Secret verifies, so a
		// failed materialization never leaves the Agent holding registry
		// access the Secret was not granted.
		authfileEntry := map[string]string{
			"username": payload.Username,
			"password": payload.Password,
			"email":    payload.Email,
			"auth":     auth,
		}
		if req.AuthfileOnly {
			// The Secret already exists and outlives the Pod; only the authfile
			// was lost. Restoring it must not rebuild the Secret, because
			// replacing it would break a workload that is pulling with it.
			if err := deliverRegistryAuthEntry(payload.Server, authfileEntry,
				req.CredentialID, req.GrantID, req.VersionID); err != nil {
				return CredentialMaterialization{}, err
			}
			return registryAuthOnlyMaterialization(req, payload.Server, auth, result.Digest), nil
		}
		pendingRegistryAuth = &registryAuthPendingDelivery{
			server: payload.Server, entry: authfileEntry,
			credentialID: req.CredentialID, grantID: req.GrantID, versionID: req.VersionID,
		}
	case CredentialTypeTLS:
		if req.Scope.Use != CredentialUseTLS {
			return CredentialMaterialization{}, errors.New("credential_use_mismatch")
		}
		var payload struct {
			Certificate string `json:"certificate"`
			PrivateKey  string `json:"privateKey"`
		}
		_ = json.Unmarshal(req.Payload, &payload)
		certificate, fingerprint, err := validateTLSCredential(payload.Certificate, payload.PrivateKey)
		if err != nil {
			return CredentialMaterialization{}, err
		}
		secretType = "kubernetes.io/tls"
		secretData = map[string]string{"tls.crt": payload.Certificate, "tls.key": payload.PrivateKey}
		result.Fingerprint = fingerprint
		result.ExpiresAt = certificate.NotAfter.UTC().Format(time.RFC3339)
	case CredentialTypeOpaque:
		if req.Scope.Use != CredentialUseOpaqueSecret && req.Scope.Use != CredentialUseDatabase {
			return CredentialMaterialization{}, errors.New("credential_use_mismatch")
		}
		var payload struct {
			Data map[string]string `json:"data"`
		}
		_ = json.Unmarshal(req.Payload, &payload)
		if !sameCredentialKeys(req.RequiredKeys, mapKeys(payload.Data)) {
			return CredentialMaterialization{}, errors.New("credential_key_mismatch")
		}
		if req.RuntimeEnvOnly {
			return materializeRuntimeOpaqueCredential(ctx, req, payload.Data, runtimeOpaquePayloadDigest(req.Payload))
		}
		secretType = "Opaque"
		secretData = payload.Data
	case CredentialTypeHelmRepository:
		if req.Scope.Use != CredentialUseHelmPull {
			return CredentialMaterialization{}, errors.New("credential_use_mismatch")
		}
		var payload struct {
			URL      string `json:"url"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.Unmarshal(req.Payload, &payload)
		sum := sha256.Sum256(req.Payload)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		return materializeNativeHelmCredential(ctx, req, nativeHelmAuth{
			URL: payload.URL, Username: payload.Username, Password: payload.Password,
		}, digest)
	case CredentialTypeModelProvider:
		// The model lives in doagent's fixed settings file, not a Kubernetes Secret.
		return materializeModelProviderCredential(ctx, req)
	case CredentialTypeGitToken:
		if req.Scope.Use != CredentialUseGitCheckout {
			return CredentialMaterialization{}, errors.New("credential_use_mismatch")
		}
		var payload struct {
			URL      string `json:"url"`
			Username string `json:"username"`
			Token    string `json:"token"`
		}
		_ = json.Unmarshal(req.Payload, &payload)
		digest, err := verifyGitRemote(ctx, payload.URL, payload.Username, payload.Token)
		if err != nil {
			return CredentialMaterialization{}, err
		}
		secretType = "Opaque"
		secretData = map[string]string{"url": payload.URL, "username": payload.Username, "password": payload.Token}
		result.Digest = digest
	default:
		return CredentialMaterialization{}, errors.New("credential_type_unsupported")
	}

	snapshot, err := snapshotCredentialTarget(ctx, req, name)
	if err != nil {
		return CredentialMaterialization{}, err
	}
	result.PreviousVersionID = snapshot.PreviousVersionID
	applyErr := applyCredentialSecret(ctx, req, name, secretType, secretData)
	applied, inspectErr := inspectCredentialSecret(ctx, req.Scope.Namespace, name)
	if inspectErr != nil || !credentialSecretMatches(applied, req, secretType, secretData) {
		clear(applied.Data)
		if restoreErr := restoreCredentialTarget(ctx, req.Scope.Namespace, name, snapshot, applied); restoreErr != nil {
			return CredentialMaterialization{}, errors.New("credential_target_outcome_unknown")
		}
		if applyErr != nil {
			return CredentialMaterialization{}, applyErr
		}
		if inspectErr != nil {
			return CredentialMaterialization{}, inspectErr
		}
		return CredentialMaterialization{}, errors.New("credential_secret_verification_failed")
	}
	defer clear(applied.Data)
	if req.CredentialType == CredentialTypeRegistry && strings.TrimSpace(req.Scope.Workload.Name) != "" {
		if err := patchImagePullSecret(ctx, req.Scope.Namespace, req.Scope.Workload, name); err != nil {
			if restoreErr := restoreCredentialTarget(ctx, req.Scope.Namespace, name, snapshot, applied); restoreErr != nil {
				return CredentialMaterialization{}, errors.New("credential_target_outcome_unknown")
			}
			return CredentialMaterialization{}, err
		}
	}
	actual, err := inspectCredentialSecret(ctx, req.Scope.Namespace, name)
	if err != nil {
		if restoreErr := restoreCredentialTarget(ctx, req.Scope.Namespace, name, snapshot, applied); restoreErr != nil {
			return CredentialMaterialization{}, errors.New("credential_target_outcome_unknown")
		}
		return CredentialMaterialization{}, err
	}
	defer clear(actual.Data)
	keys := mapKeys(actual.Data)
	if actual.UID != applied.UID || !credentialSecretMatches(actual, req, secretType, secretData) {
		if restoreErr := restoreCredentialTarget(ctx, req.Scope.Namespace, name, snapshot, applied); restoreErr != nil {
			return CredentialMaterialization{}, errors.New("credential_target_outcome_unknown")
		}
		return CredentialMaterialization{}, errors.New("credential_secret_verification_failed")
	}
	result.SecretType = actual.SecretType
	result.ResourceVersion = actual.ResourceVersion
	result.Keys = keys
	if pendingRegistryAuth != nil {
		// The Secret is in place and verified, so the Agent may now use the
		// registry from its own tools.
		if err := deliverRegistryAuthEntry(pendingRegistryAuth.server, pendingRegistryAuth.entry,
			pendingRegistryAuth.credentialID, pendingRegistryAuth.grantID, pendingRegistryAuth.versionID); err != nil {
			if restoreErr := restoreCredentialTarget(ctx, req.Scope.Namespace, name, snapshot, actual); restoreErr != nil {
				return CredentialMaterialization{}, errors.New("credential_target_outcome_unknown")
			}
			return CredentialMaterialization{}, err
		}
	}
	return result, nil
}

func snapshotCredentialTarget(ctx context.Context, req CredentialMaterializeRequest, name string) (credentialTargetSnapshot, error) {
	snapshot := credentialTargetSnapshot{}
	cmd := exec.CommandContext(ctx, "kubectl", "get", "secret", name, "-n", req.Scope.Namespace, "--ignore-not-found", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return credentialTargetSnapshot{}, errors.New("credential_secret_get_failed")
	}
	snapshot.Secret = append([]byte(nil), bytes.TrimSpace(output)...)
	if len(snapshot.Secret) != 0 {
		var existing struct {
			Metadata struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(snapshot.Secret, &existing); err != nil {
			return credentialTargetSnapshot{}, errors.New("credential_secret_verification_failed")
		}
		if existing.Metadata.Labels["app.kubernetes.io/managed-by"] != "doops" ||
			existing.Metadata.Labels["doops.sh/credential-id"] != req.CredentialID {
			return credentialTargetSnapshot{}, errors.New("credential_secret_ownership_conflict")
		}
		snapshot.PreviousVersionID = strings.TrimSpace(existing.Metadata.Annotations["doops.sh/credential-version"])
		if snapshot.PreviousVersionID == "" {
			return credentialTargetSnapshot{}, errors.New("credential_secret_ownership_conflict")
		}
	}
	if req.CredentialType == CredentialTypeRegistry && strings.TrimSpace(req.Scope.Workload.Name) != "" {
		kind := strings.ToLower(strings.TrimSpace(req.Scope.Workload.Kind))
		if (kind != "deployment" && kind != "statefulset" && kind != "daemonset") ||
			!validKubernetesCredentialName(req.Scope.Workload.Name) {
			return credentialTargetSnapshot{}, errors.New("credential_workload_invalid")
		}
		names, err := inspectImagePullSecrets(ctx, req.Scope.Namespace, kind, req.Scope.Workload.Name)
		if err != nil {
			return credentialTargetSnapshot{}, err
		}
		snapshot.WorkloadKind = kind
		snapshot.WorkloadName = req.Scope.Workload.Name
		snapshot.ImagePullNames = names
	}
	return snapshot, nil
}

func restoreCredentialTarget(
	ctx context.Context,
	namespace string,
	name string,
	snapshot credentialTargetSnapshot,
	applied credentialSecretInspection,
) error {
	if len(snapshot.Secret) == 0 {
		current, err := getCredentialSecret(ctx, namespace, name)
		if err != nil {
			return errors.New("credential_secret_restore_failed")
		}
		if len(bytes.TrimSpace(current)) != 0 {
			currentIdentity, identityErr := inspectCredentialSecretIdentity(current)
			if identityErr != nil || applied.UID == "" || currentIdentity.UID != applied.UID ||
				(applied.ResourceVersion != "" && currentIdentity.ResourceVersion != applied.ResourceVersion) {
				return errors.New("credential_secret_restore_failed")
			}
			deleteOptions, _ := json.Marshal(map[string]any{
				"apiVersion": "v1",
				"kind":       "DeleteOptions",
				"preconditions": map[string]string{
					"uid":             currentIdentity.UID,
					"resourceVersion": currentIdentity.ResourceVersion,
				},
			})
			_ = runCredentialCommand(ctx, deleteOptions, "kubectl", "delete",
				"--raw", "/api/v1/namespaces/"+namespace+"/secrets/"+name, "-f", "-")
			remaining, inspectErr := getCredentialSecret(ctx, namespace, name)
			if inspectErr != nil || len(bytes.TrimSpace(remaining)) != 0 {
				return errors.New("credential_secret_restore_failed")
			}
		}
	} else {
		current, err := getCredentialSecret(ctx, namespace, name)
		if err != nil || len(bytes.TrimSpace(current)) == 0 {
			return errors.New("credential_secret_restore_failed")
		}
		currentIdentity, identityErr := inspectCredentialSecretIdentity(current)
		if identityErr != nil || applied.UID == "" || currentIdentity.UID != applied.UID ||
			(applied.ResourceVersion != "" && currentIdentity.ResourceVersion != applied.ResourceVersion) {
			return errors.New("credential_secret_restore_failed")
		}
		manifest, sanitizeErr := credentialSecretRestoreManifest(snapshot.Secret, currentIdentity)
		if sanitizeErr != nil {
			return errors.New("credential_secret_restore_failed")
		}
		_ = runCredentialCommand(ctx, manifest, "kubectl", "replace", "-f", "-")
		restored, err := getCredentialSecret(ctx, namespace, name)
		restoredIdentity, identityErr := inspectCredentialSecretIdentity(restored)
		if err != nil || identityErr != nil || restoredIdentity.UID != currentIdentity.UID ||
			!sameCredentialSecretSnapshot(snapshot.Secret, restored) {
			return errors.New("credential_secret_restore_failed")
		}
	}
	if snapshot.WorkloadName == "" {
		return nil
	}
	imagePullSecrets := make([]map[string]string, 0, len(snapshot.ImagePullNames))
	for _, item := range snapshot.ImagePullNames {
		imagePullSecrets = append(imagePullSecrets, map[string]string{"name": item})
	}
	patch, _ := json.Marshal(map[string]any{
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"imagePullSecrets": imagePullSecrets,
		}}},
	})
	_ = runCredentialCommand(ctx, nil, "kubectl", "patch", snapshot.WorkloadKind, snapshot.WorkloadName,
		"-n", namespace, "--type=merge", "--patch", string(patch))
	observed, err := inspectImagePullSecrets(ctx, namespace, snapshot.WorkloadKind, snapshot.WorkloadName)
	if err != nil || !sameCredentialKeys(observed, snapshot.ImagePullNames) {
		return errors.New("credential_workload_restore_failed")
	}
	return nil
}

func cleanupCredential(ctx context.Context, req CredentialCleanupRequest) error {
	if strings.TrimSpace(req.CredentialID) == "" || strings.TrimSpace(req.GrantID) == "" ||
		(!req.AnyVersion && strings.TrimSpace(req.VersionID) == "") ||
		!validKubernetesCredentialName(req.Namespace) {
		return errors.New("credential_cleanup_invalid")
	}
	if req.AuthfileOnly {
		// A restore owns nothing new: the Kubernetes Secret belongs to the
		// workload pulling with it, and the authfile entry it restores was
		// already there before the restore. Removing either would take away
		// registry access the Agent had before the restore was attempted, so a
		// failed restore leaves both alone.
		return nil
	}
	if cleaned, err := cleanupNativeHelmCredential(req); err != nil {
		return err
	} else if cleaned {
		return nil
	}
	if cleaned, err := cleanupRuntimeOpaqueCredential(req); err != nil {
		return err
	} else if cleaned {
		return nil
	}
	if cleaned, err := cleanupModelProviderCredential(req); err != nil {
		return err
	} else if cleaned {
		return nil
	}
	// A registry grant owns authfile entries as well as a Secret, so this
	// removes the entries and the Secret cleanup below still runs.
	if err := cleanupRegistryAuthEntries(req); err != nil {
		return err
	}
	name := credentialSecretName(req.CredentialID, req.GrantID)
	output, err := getCredentialSecret(ctx, req.Namespace, name)
	if err != nil {
		return err
	}
	secretExists := len(bytes.TrimSpace(output)) != 0
	var secretUID string
	var secretResourceVersion string
	if secretExists {
		var secret struct {
			Metadata struct {
				UID             string            `json:"uid"`
				ResourceVersion string            `json:"resourceVersion"`
				Labels          map[string]string `json:"labels"`
				Annotations     map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(output, &secret); err != nil {
			return errors.New("credential_secret_verification_failed")
		}
		secretUID = strings.TrimSpace(secret.Metadata.UID)
		secretResourceVersion = strings.TrimSpace(secret.Metadata.ResourceVersion)
		if secret.Metadata.Labels["app.kubernetes.io/managed-by"] != "doops" ||
			secret.Metadata.Labels["doops.sh/credential-id"] != req.CredentialID ||
			secret.Metadata.Labels["doops.sh/credential-grant-id"] != req.GrantID ||
			secretUID == "" || secretResourceVersion == "" ||
			(!req.AnyVersion && secret.Metadata.Annotations["doops.sh/credential-version"] != req.VersionID) {
			return errors.New("credential_secret_ownership_mismatch")
		}
	}

	var workloadKind string
	var originalNames []string
	workloadChanged := false
	if strings.TrimSpace(req.Workload.Name) != "" {
		workloadKind, err = credentialWorkloadKind(req.Workload)
		if err != nil {
			return err
		}
		var workloadExists bool
		originalNames, workloadExists, err = inspectImagePullSecretsOptional(ctx, req.Namespace, workloadKind, req.Workload.Name)
		if err != nil {
			return err
		}
		if workloadExists {
			filtered := withoutString(originalNames, name)
			if !sameCredentialKeys(originalNames, filtered) {
				mutationErr := patchImagePullSecrets(ctx, req.Namespace, workloadKind, req.Workload.Name, filtered)
				observed, stillExists, inspectErr := inspectImagePullSecretsOptional(ctx, req.Namespace, workloadKind, req.Workload.Name)
				if inspectErr != nil {
					return errors.New("credential_target_outcome_unknown")
				}
				switch {
				case !stillExists:
					// Workload was deleted concurrently; nothing left to detach.
				case sameCredentialKeys(observed, filtered):
					workloadChanged = true
				case mutationErr != nil && sameCredentialKeys(observed, originalNames):
					return mutationErr
				default:
					return errors.New("credential_target_outcome_unknown")
				}
			}
		}
	}

	if !secretExists {
		return nil
	}
	deleteOptions, _ := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "DeleteOptions",
		"preconditions": map[string]string{
			"uid":             secretUID,
			"resourceVersion": secretResourceVersion,
		},
	})
	_ = runCredentialCommand(ctx, deleteOptions, "kubectl", "delete",
		"--raw", "/api/v1/namespaces/"+req.Namespace+"/secrets/"+name, "-f", "-")
	remaining, inspectErr := getCredentialSecret(ctx, req.Namespace, name)
	if inspectErr != nil {
		return errors.New("credential_target_outcome_unknown")
	}
	if len(bytes.TrimSpace(remaining)) == 0 {
		return nil
	}
	var remainingSecret struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(remaining, &remainingSecret); err != nil ||
		strings.TrimSpace(remainingSecret.Metadata.UID) != secretUID {
		return errors.New("credential_target_outcome_unknown")
	}
	if workloadChanged {
		_ = patchImagePullSecrets(ctx, req.Namespace, workloadKind, req.Workload.Name, originalNames)
		observed, verifyErr := inspectImagePullSecrets(ctx, req.Namespace, workloadKind, req.Workload.Name)
		if verifyErr != nil || !sameCredentialKeys(observed, originalNames) {
			return errors.New("credential_target_outcome_unknown")
		}
	}
	return errors.New("credential_secret_delete_failed")
}

func runCredentialCommand(ctx context.Context, stdin []byte, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	if err := cmd.Run(); err != nil {
		return errors.New("credential_target_mutation_failed")
	}
	return nil
}

func credentialSecretName(credentialID, grantID string) string {
	sum := sha256.Sum256([]byte(credentialID + "\x00" + grantID))
	return "doops-cred-" + hex.EncodeToString(sum[:8])
}

func validKubernetesCredentialName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 253 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '.' {
			continue
		}
		if index == 0 || index == len(value)-1 {
			return false
		}
		return false
	}
	return value[0] != '-' && value[0] != '.' && value[len(value)-1] != '-' && value[len(value)-1] != '.'
}

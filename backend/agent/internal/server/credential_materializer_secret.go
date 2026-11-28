package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os/exec"
	"sort"
	"strings"
)

type credentialSecretInspection struct {
	UID             string
	SecretType      string
	ResourceVersion string
	CredentialID    string
	GrantID         string
	VersionID       string
	Data            map[string]string
}

func applyCredentialSecret(ctx context.Context, req CredentialMaterializeRequest, name, secretType string, values map[string]string) error {
	data := make(map[string]string, len(values))
	for key, value := range values {
		data[key] = base64.StdEncoding.EncodeToString([]byte(value))
	}
	manifest, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": req.Scope.Namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "doops",
				"doops.sh/credential-id":       req.CredentialID,
				"doops.sh/credential-grant-id": req.GrantID,
			},
			"annotations": map[string]string{
				"doops.sh/credential-version": req.VersionID,
			},
		},
		"type": secretType,
		"data": data,
	})
	if err != nil {
		return errors.New("credential_manifest_invalid")
	}
	return runCredentialCommand(ctx, manifest, "kubectl", "apply", "--server-side", "--field-manager=doops-credential", "-f", "-")
}

func patchImagePullSecret(ctx context.Context, namespace string, workload CredentialPlanWorkload, secretName string) error {
	kind, err := credentialWorkloadKind(workload)
	if err != nil {
		return err
	}
	names, err := inspectImagePullSecrets(ctx, namespace, kind, workload.Name)
	if err != nil {
		return err
	}
	if !containsString(names, secretName) {
		names = append(names, secretName)
	}
	mutationErr := patchImagePullSecrets(ctx, namespace, kind, workload.Name, names)
	observed, inspectErr := inspectImagePullSecrets(ctx, namespace, kind, workload.Name)
	if inspectErr != nil {
		return errors.New("credential_target_outcome_unknown")
	}
	if sameCredentialKeys(observed, names) {
		return nil
	}
	if mutationErr != nil {
		return mutationErr
	}
	return errors.New("credential_workload_verification_failed")
}

func inspectImagePullSecrets(ctx context.Context, namespace, kind, name string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", kind, name, "-n", namespace, "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("credential_workload_get_failed")
	}
	return parseWorkloadImagePullSecrets(output)
}

// inspectImagePullSecretsOptional reads imagePullSecrets during cleanup.
// A workload that no longer exists (--ignore-not-found returning empty output)
// returns exists=false so cleanup skips workload detachment and still deletes
// the DoOps-managed Secret. RBAC/network/API errors still fail closed with
// credential_workload_get_failed.
func inspectImagePullSecretsOptional(ctx context.Context, namespace, kind, name string) ([]string, bool, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", kind, name, "-n", namespace, "--ignore-not-found", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, false, errors.New("credential_workload_get_failed")
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, false, nil
	}
	names, err := parseWorkloadImagePullSecrets(output)
	if err != nil {
		return nil, true, err
	}
	return names, true, nil
}

func parseWorkloadImagePullSecrets(output []byte) ([]string, error) {
	var workload struct {
		Spec struct {
			Template struct {
				Spec struct {
					ImagePullSecrets []struct {
						Name string `json:"name"`
					} `json:"imagePullSecrets"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(output, &workload); err != nil {
		return nil, errors.New("credential_workload_invalid")
	}
	names := make([]string, 0, len(workload.Spec.Template.Spec.ImagePullSecrets))
	for _, item := range workload.Spec.Template.Spec.ImagePullSecrets {
		if name := strings.TrimSpace(item.Name); name != "" && !containsString(names, name) {
			names = append(names, name)
		}
	}
	return names, nil
}

func patchImagePullSecrets(ctx context.Context, namespace, kind, name string, names []string) error {
	imagePullSecrets := make([]map[string]string, 0, len(names))
	for _, item := range names {
		imagePullSecrets = append(imagePullSecrets, map[string]string{"name": item})
	}
	patch, _ := json.Marshal(map[string]any{
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"imagePullSecrets": imagePullSecrets,
		}}},
	})
	return runCredentialCommand(ctx, nil, "kubectl", "patch", kind, name, "-n", namespace, "--type=merge", "--patch", string(patch))
}

func credentialWorkloadKind(workload CredentialPlanWorkload) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(workload.Kind))
	if (kind != "deployment" && kind != "statefulset" && kind != "daemonset") ||
		!validKubernetesCredentialName(workload.Name) {
		return "", errors.New("credential_workload_invalid")
	}
	return kind, nil
}

func withoutString(values []string, excluded string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != excluded {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func getCredentialSecret(ctx context.Context, namespace, name string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "kubectl", "get", "secret", name, "-n", namespace, "--ignore-not-found", "-o", "json").Output()
	if err != nil {
		return nil, errors.New("credential_secret_get_failed")
	}
	return output, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func inspectCredentialSecret(ctx context.Context, namespace, name string) (credentialSecretInspection, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "secret", name, "-n", namespace, "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return credentialSecretInspection{}, errors.New("credential_secret_get_failed")
	}
	var secret struct {
		Metadata struct {
			UID             string            `json:"uid"`
			ResourceVersion string            `json:"resourceVersion"`
			Labels          map[string]string `json:"labels"`
			Annotations     map[string]string `json:"annotations"`
		} `json:"metadata"`
		Type string            `json:"type"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(output, &secret); err != nil {
		return credentialSecretInspection{}, errors.New("credential_secret_verification_failed")
	}
	actual := credentialSecretInspection{
		UID:             strings.TrimSpace(secret.Metadata.UID),
		SecretType:      strings.TrimSpace(secret.Type),
		ResourceVersion: strings.TrimSpace(secret.Metadata.ResourceVersion),
		CredentialID:    strings.TrimSpace(secret.Metadata.Labels["doops.sh/credential-id"]),
		GrantID:         strings.TrimSpace(secret.Metadata.Labels["doops.sh/credential-grant-id"]),
		VersionID:       strings.TrimSpace(secret.Metadata.Annotations["doops.sh/credential-version"]),
		Data:            make(map[string]string, len(secret.Data)),
	}
	if actual.UID == "" ||
		strings.TrimSpace(secret.Type) == "" ||
		strings.TrimSpace(secret.Metadata.ResourceVersion) == "" {
		return actual, errors.New("credential_secret_verification_failed")
	}
	for key, encoded := range secret.Data {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			clear(actual.Data)
			actual.Data = nil
			return actual, errors.New("credential_secret_verification_failed")
		}
		actual.Data[key] = string(decoded)
		clear(decoded)
	}
	return actual, nil
}

func inspectCredentialSecretIdentity(raw []byte) (credentialSecretInspection, error) {
	var secret struct {
		Metadata struct {
			UID             string `json:"uid"`
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &secret); err != nil {
		return credentialSecretInspection{}, err
	}
	identity := credentialSecretInspection{
		UID:             strings.TrimSpace(secret.Metadata.UID),
		ResourceVersion: strings.TrimSpace(secret.Metadata.ResourceVersion),
	}
	if identity.UID == "" || identity.ResourceVersion == "" {
		return credentialSecretInspection{}, errors.New("credential_secret_identity_invalid")
	}
	return identity, nil
}

func credentialSecretRestoreManifest(
	snapshot []byte,
	current credentialSecretInspection,
) ([]byte, error) {
	var manifest map[string]any
	if err := json.Unmarshal(snapshot, &manifest); err != nil {
		return nil, err
	}
	metadata, ok := manifest["metadata"].(map[string]any)
	if !ok {
		return nil, errors.New("credential_secret_metadata_invalid")
	}
	for _, key := range []string{
		"uid", "resourceVersion", "generation", "managedFields", "creationTimestamp",
		"deletionTimestamp", "deletionGracePeriodSeconds", "selfLink",
	} {
		delete(metadata, key)
	}
	metadata["uid"] = current.UID
	metadata["resourceVersion"] = current.ResourceVersion
	delete(manifest, "status")
	return json.Marshal(manifest)
}

func credentialSecretMatches(
	actual credentialSecretInspection,
	req CredentialMaterializeRequest,
	secretType string,
	secretData map[string]string,
) bool {
	return actual.SecretType == secretType &&
		actual.CredentialID == req.CredentialID &&
		actual.GrantID == req.GrantID &&
		actual.VersionID == req.VersionID &&
		sameCredentialData(secretData, actual.Data)
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameCredentialKeys(left, right []string) bool {
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func sameCredentialData(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func sameCredentialSecretSnapshot(left, right []byte) bool {
	var decode = func(raw []byte) (struct {
		Metadata struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Type string            `json:"type"`
		Data map[string]string `json:"data"`
	}, error) {
		var secret struct {
			Metadata struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Type string            `json:"type"`
			Data map[string]string `json:"data"`
		}
		err := json.Unmarshal(raw, &secret)
		return secret, err
	}
	expected, err := decode(left)
	if err != nil {
		return false
	}
	actual, err := decode(right)
	if err != nil {
		return false
	}
	return expected.Type == actual.Type &&
		sameCredentialData(expected.Data, actual.Data) &&
		expected.Metadata.Labels["app.kubernetes.io/managed-by"] == actual.Metadata.Labels["app.kubernetes.io/managed-by"] &&
		expected.Metadata.Labels["doops.sh/credential-id"] == actual.Metadata.Labels["doops.sh/credential-id"] &&
		expected.Metadata.Labels["doops.sh/credential-grant-id"] == actual.Metadata.Labels["doops.sh/credential-grant-id"] &&
		expected.Metadata.Annotations["doops.sh/credential-version"] == actual.Metadata.Annotations["doops.sh/credential-version"]
}

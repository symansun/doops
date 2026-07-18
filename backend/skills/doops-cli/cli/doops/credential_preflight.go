package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultDevelopmentCredentialManifest = ".agent/doops-credentials.yaml"
	maxDevelopmentCredentialManifestSize = 64 << 10
)

var developmentCredentialRefPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,126}[a-z0-9])?$`)

type developmentCredentialManifest struct {
	APIVersion  string                             `yaml:"apiVersion"`
	Kind        string                             `yaml:"kind"`
	Target      string                             `yaml:"target"`
	Credentials []developmentCredentialRequirement `yaml:"credentials"`
}

type developmentCredentialRequirement struct {
	CredentialRef string `yaml:"credentialRef"`
	Use           string `yaml:"use"`
}

func runCredentialPreflight(
	args []string,
	sessionName string,
	servers []Server,
	configErr error,
	stdout io.Writer,
) error {
	var manifestPath, target string
	flags := credentialFlagSet("credential preflight", stdout)
	flags.StringVar(&manifestPath, "manifest", defaultDevelopmentCredentialManifest, "Metadata-only development credential manifest")
	flags.StringVar(&target, "target", "", "Configured gateway target; must match manifest target when both are set")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("credential preflight accepts no positional arguments")
	}
	if strings.TrimSpace(sessionName) == "" {
		return fmt.Errorf("-session is required")
	}

	manifest, err := readDevelopmentCredentialManifest(manifestPath)
	if err != nil {
		return err
	}
	target = strings.TrimSpace(target)
	manifestTarget := strings.TrimSpace(manifest.Target)
	if target != "" && manifestTarget != "" && target != manifestTarget {
		return fmt.Errorf("--target %q does not match manifest target %q", target, manifestTarget)
	}
	if target == "" {
		target = manifestTarget
	}
	if target == "" {
		return fmt.Errorf("development credential manifest target or --target is required")
	}

	credentials, err := listCredentialResources(target, "target", servers, configErr)
	if err != nil {
		return err
	}
	failures := 0
	for _, requirement := range manifest.Credentials {
		matches := make([]credentialResource, 0, 1)
		for _, credential := range credentials {
			if credential.Name == requirement.CredentialRef && credential.State == "active" {
				matches = append(matches, credential)
			}
		}
		switch {
		case len(matches) == 0:
			failures++
			if _, err := fmt.Fprintf(stdout, "credential_ref=%s use=%s status=missing\n",
				requirement.CredentialRef, requirement.Use); err != nil {
				return err
			}
		case len(matches) > 1:
			failures++
			if _, err := fmt.Fprintf(stdout, "credential_ref=%s use=%s status=ambiguous matches=%d\n",
				requirement.CredentialRef, requirement.Use, len(matches)); err != nil {
				return err
			}
		default:
			credential := matches[0]
			if !credentialHasActiveUse(credential, requirement.Use) {
				failures++
				if _, err := fmt.Fprintf(stdout,
					"credential_ref=%s use=%s credential_id=%s status=wrong_use\n",
					requirement.CredentialRef, requirement.Use, credential.ID); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(stdout,
				"credential_ref=%s use=%s credential_id=%s status=ready\n",
				requirement.CredentialRef, requirement.Use, credential.ID); err != nil {
				return err
			}
		}
	}
	if failures != 0 {
		return fmt.Errorf("credential preflight failed: %d requirement(s) unavailable", failures)
	}
	_, err = fmt.Fprintf(stdout, "credential_preflight=ready target=%s requirements=%d\n",
		target, len(manifest.Credentials))
	return err
}

func credentialHasActiveUse(credential credentialResource, use string) bool {
	for _, grant := range credential.Grants {
		if grant.State == "active" && grant.Scope.Use == use {
			return true
		}
	}
	return false
}

func readDevelopmentCredentialManifest(path string) (developmentCredentialManifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return developmentCredentialManifest{}, fmt.Errorf("--manifest must not be empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return developmentCredentialManifest{}, fmt.Errorf("open development credential manifest %q: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxDevelopmentCredentialManifestSize+1))
	if err != nil {
		return developmentCredentialManifest{}, fmt.Errorf("read development credential manifest %q: %w", path, err)
	}
	if len(data) > maxDevelopmentCredentialManifestSize {
		return developmentCredentialManifest{}, fmt.Errorf(
			"development credential manifest exceeds %d bytes", maxDevelopmentCredentialManifestSize,
		)
	}

	var manifest developmentCredentialManifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return developmentCredentialManifest{}, fmt.Errorf("decode development credential manifest %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return developmentCredentialManifest{}, fmt.Errorf(
				"development credential manifest %q must contain exactly one document", path,
			)
		}
		return developmentCredentialManifest{}, fmt.Errorf("decode development credential manifest %q: %w", path, err)
	}
	if err := validateDevelopmentCredentialManifest(&manifest); err != nil {
		return developmentCredentialManifest{}, fmt.Errorf("invalid development credential manifest %q: %w", path, err)
	}
	return manifest, nil
}

func validateDevelopmentCredentialManifest(manifest *developmentCredentialManifest) error {
	if manifest == nil {
		return fmt.Errorf("manifest is required")
	}
	if strings.TrimSpace(manifest.APIVersion) != "doops.sh/v1" {
		return fmt.Errorf("apiVersion must be doops.sh/v1")
	}
	if strings.TrimSpace(manifest.Kind) != "DevelopmentCredentialRequirements" {
		return fmt.Errorf("kind must be DevelopmentCredentialRequirements")
	}
	manifest.APIVersion = strings.TrimSpace(manifest.APIVersion)
	manifest.Kind = strings.TrimSpace(manifest.Kind)
	manifest.Target = strings.TrimSpace(manifest.Target)
	if len(manifest.Credentials) == 0 {
		return fmt.Errorf("credentials must contain at least one requirement")
	}
	seen := make(map[string]struct{}, len(manifest.Credentials))
	for index, requirement := range manifest.Credentials {
		ref := strings.TrimSpace(requirement.CredentialRef)
		use := strings.TrimSpace(requirement.Use)
		if ref == "" || use == "" {
			return fmt.Errorf("credentials[%d] requires credentialRef and use", index)
		}
		if !developmentCredentialRefPattern.MatchString(ref) || strings.Contains(ref, "..") {
			return fmt.Errorf("credentials[%d].credentialRef must be a lowercase resource name", index)
		}
		if !validDevelopmentCredentialUse(use) {
			return fmt.Errorf("credentials[%d].use %q is unsupported", index, use)
		}
		key := ref + "\x00" + use
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate credential requirement %q for use %q", ref, use)
		}
		seen[key] = struct{}{}
		manifest.Credentials[index].CredentialRef = ref
		manifest.Credentials[index].Use = use
	}
	return nil
}

func validDevelopmentCredentialUse(use string) bool {
	switch use {
	case "imagePull", "gitCheckout", "helmPull", "tls", "opaqueSecret":
		return true
	default:
		return false
	}
}

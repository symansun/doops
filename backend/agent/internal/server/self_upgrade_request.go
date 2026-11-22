package server

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	imageRepositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$`)
	imageTagPattern        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	imageDigestPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

func validateAgentUpgradeDispatch(args agentUpgradeParams) error {
	if strings.TrimSpace(args.ObserveJob) != "" && args.Rollback {
		return fmt.Errorf("observe_job cannot be combined with rollback")
	}
	if strings.TrimSpace(args.ObserveJob) != "" || args.Rollback {
		return nil
	}
	_, _, _, err := parseUpgradeImage(args.Image)
	return err
}

func parseUpgradeImage(image string) (string, string, string, error) {
	image = strings.TrimSpace(image)
	at := strings.LastIndex(image, "@sha256:")
	if at > 0 {
		repository := image[:at]
		digest := image[at+1:]
		if !imageDigestPattern.MatchString(digest) {
			return "", "", "", fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")
		}
		if !imageRepositoryPattern.MatchString(repository) {
			return "", "", "", fmt.Errorf("agent upgrade image repository is invalid")
		}
		return repository, "", digest, nil
	}
	colon := strings.LastIndex(image, ":")
	slash := strings.LastIndex(image, "/")
	if colon <= slash {
		return "", "", "", fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")
	}
	repository := image[:colon]
	tag := image[colon+1:]
	if !imageRepositoryPattern.MatchString(repository) {
		return "", "", "", fmt.Errorf("agent upgrade image repository is invalid")
	}
	if !imageTagPattern.MatchString(tag) {
		return "", "", "", fmt.Errorf("agent upgrade image tag is invalid")
	}
	return repository, tag, "", nil
}

func parsePinnedImage(image string) (string, string, error) {
	repository, _, digest, err := parseUpgradeImage(image)
	if err != nil {
		return "", "", err
	}
	if digest == "" {
		return "", "", fmt.Errorf("rollback requires an immutable historical image")
	}
	return repository, digest, nil
}

func validateK8sName(kind, name string, max int) error {
	if len(name) == 0 || len(name) > max || !k8sNamePattern.MatchString(name) {
		return fmt.Errorf("invalid %s %q", kind, name)
	}
	return nil
}

type agentUpgradeParams struct {
	UpgradeOperationID    string `json:"upgrade_operation_id"`
	ExpectedReleaseCommit string `json:"expected_release_commit"`
	SourceCommit          string `json:"source_commit"`
	CIURL                 string `json:"ci_url"`
	RequesterID           string `json:"requester_id"`
	AuditID               int64  `json:"audit_id"`
	SessionID             string `json:"session_id"`
	Image                 string `json:"image"`
	Mode                  string `json:"mode"`
	Namespace             string `json:"namespace"`
	Workload              string `json:"workload"`
	Container             string `json:"container"`
	Release               string `json:"release"`
	DryRun                bool   `json:"dry_run"`
	ObserveJob            string `json:"observe_job"`
	Rollback              bool   `json:"rollback"`
	Preflight             bool   `json:"preflight"`
	// AllowMissingRollbackBaseline lets an operator proceed on purpose when
	// the image a rollback would restore is gone from the registry. The
	// default is fail closed, because without that image the third upgrade
	// guarantee (restore the backup when verification fails) cannot hold.
	AllowMissingRollbackBaseline bool `json:"allow_missing_rollback_baseline"`
}

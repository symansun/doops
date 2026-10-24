package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type UpgradeOptions struct {
	Image     string
	Mode      string
	Namespace string
	Workload  string
	Container string
	Release   string
	DryRun    bool
	Rollback  bool
	Session   string
}

var (
	upgradePollInterval  = 5 * time.Second
	upgradeWait          = 21 * time.Minute
	cliImageRepository   = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$`)
	cliImageTag          = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	cliImageDigest       = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	releaseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func UpgradeAgents(base Server, opts UpgradeOptions, verbose bool) error {
	if opts.Rollback {
		if strings.TrimSpace(opts.Image) != "" {
			return fmt.Errorf("rollback does not take an image")
		}
		if opts.DryRun {
			return fmt.Errorf("rollback cannot be combined with --dry-run")
		}
	} else {
		if strings.TrimSpace(opts.Image) == "" {
			return fmt.Errorf("image is required")
		}
		if err := validateUpgradeImage(opts.Image); err != nil {
			return err
		}
	}
	if strings.TrimSpace(base.Gateway) == "" ||
		strings.TrimSpace(base.Cluster) == "" ||
		strings.TrimSpace(base.Instance) == "" {
		return fmt.Errorf("configured Gateway target is required")
	}
	session := resolveSession(opts.Session)
	expectedRelease := ""
	if !opts.DryRun && !opts.Rollback {
		expectedRelease = strings.TrimSpace(cliBuildCommit)
		if !releaseCommitPattern.MatchString(expectedRelease) {
			return fmt.Errorf("published CLI release commit is required for Agent upgrade")
		}
	}
	token, err := ResolveToken(base)
	if err != nil {
		return err
	}
	targets, err := fetchGatewayTargets(base.Gateway, token)
	if err != nil {
		return err
	}
	selected := filterUpgradeTargets(targets, base.Cluster, base.Instance)
	if len(selected) == 0 {
		return fmt.Errorf("configured target is offline: %s/%s", base.Cluster, base.Instance)
	}
	failures := 0
	for _, target := range selected {
		server := base
		server.Name = target.Key
		server.Gateway = base.Gateway
		server.Cluster = target.Cluster
		server.Instance = target.Instance
		args := upgradeArguments(opts)
		if opts.Rollback {
			fmt.Printf("==> rolling back %s/%s\n", target.Cluster, target.Instance)
		} else {
			fmt.Printf("==> upgrading %s/%s image=%s\n", target.Cluster, target.Instance, opts.Image)
		}
		if !opts.DryRun && !opts.Rollback {
			// The preflight Job starts from the target image, so it pulls the image
			// to the node and repeats every pre-change check. Nothing is changed
			// unless it passes.
			if err := runUpgradePreflight(server, token, session, verbose, opts); err != nil {
				fmt.Printf("FAIL %s/%s: preflight failed, nothing was changed: %v\n", target.Cluster, target.Instance, err)
				failures++
				continue
			}
		}
		out, err := callUpgradeTool(server, token, session, verbose, args)
		if err != nil {
			fmt.Printf("FAIL %s/%s: %v\n", target.Cluster, target.Instance, err)
			failures++
			continue
		}
		out = strings.TrimSpace(out)
		if opts.DryRun {
			if !strings.HasPrefix(out, "dry-run: ") {
				fmt.Printf("FAIL %s/%s: invalid dry-run result: %s\n", target.Cluster, target.Instance, out)
				failures++
				continue
			}
			fmt.Printf("PREVIEW %s/%s: %s\n", target.Cluster, target.Instance, out)
			continue
		}
		if _, _, _, err := parseUpgradeAcceptance(out); err != nil {
			fmt.Printf("FAIL %s/%s: %v\n", target.Cluster, target.Instance, err)
			failures++
			continue
		}
		fmt.Printf("ACCEPTED %s/%s: %s\n", target.Cluster, target.Instance, out)
		if err := waitForSelfUpgrade(server, token, session, verbose, out, !opts.Rollback, expectedRelease); err != nil {
			fmt.Printf("FAIL %s/%s: %v\n", target.Cluster, target.Instance, err)
			failures++
			continue
		}
		action := "upgrade"
		if opts.Rollback {
			action = "rollback"
		}
		fmt.Printf("OK %s/%s: %s Job and structuredSSH protocol verified\n", target.Cluster, target.Instance, action)
		fmt.Println(withNext("verify the release", "doops targets --target "+base.Name+"   # RELEASE_COMMIT is the new commit", "doops capabilities --target "+base.Name+" --require structuredSSH"))
	}
	if failures > 0 {
		return fmt.Errorf("upgrade failed on %d/%d target(s)", failures, len(selected))
	}
	return nil
}

func upgradeArguments(opts UpgradeOptions) map[string]interface{} {
	if opts.Rollback {
		args := map[string]interface{}{
			"rollback":  true,
			"mode":      opts.Mode,
			"namespace": opts.Namespace,
			"workload":  opts.Workload,
			"release":   opts.Release,
		}
		return args
	}
	return map[string]interface{}{
		"image":     opts.Image,
		"mode":      opts.Mode,
		"namespace": opts.Namespace,
		"workload":  opts.Workload,
		"container": opts.Container,
		"release":   opts.Release,
		"dry_run":   opts.DryRun,
	}
}

// runUpgradePreflight pulls the target image on the Agent's node and runs every
// pre-change check without modifying the release. It returns only after the Job
// reached a terminal state.
func runUpgradePreflight(server Server, token, session string, verbose bool, opts UpgradeOptions) error {
	args := upgradeArguments(opts)
	args["preflight"] = true
	fmt.Printf("==> preflight: pulling %s and checking the release\n", opts.Image)
	out, err := callUpgradeTool(server, token, session, verbose, args)
	if err != nil {
		return err
	}
	job, namespace, _, err := parseUpgradeAcceptance(strings.TrimSpace(out))
	if err != nil {
		return err
	}
	deadline := time.Now().Add(upgradeWait)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("preflight job %s did not finish (the image may not be pullable)", job)
		}
		observed, err := callUpgradeTool(server, token, session, verbose, map[string]interface{}{
			"observe_job": job,
			"namespace":   namespace,
		})
		if err == nil {
			_, fields, parseErr := parseUpgradeResponse(observed)
			if parseErr != nil {
				return parseErr
			}
			if fields["job"] != job || fields["namespace"] != namespace {
				return fmt.Errorf("preflight observation does not match job %s in namespace %s", job, namespace)
			}
		}
		switch classifyUpgradeObservation(observed, err) {
		case "retry", "running":
			time.Sleep(upgradePollInterval)
			continue
		case "failed":
			return fmt.Errorf("%s", strings.TrimSpace(observed))
		case "succeeded":
			fmt.Printf("preflight passed for %s\n", opts.Image)
			return nil
		default:
			if err != nil {
				return err
			}
			return fmt.Errorf("unrecognized preflight status: %s", strings.TrimSpace(observed))
		}
	}
}

func callUpgradeTool(server Server, token, session string, verbose bool, args map[string]interface{}) (string, error) {
	client := NewMCPClient(server, NewSessionStore(), session, verbose)
	client.Token = token
	defer client.Close()
	return client.CallAndCapture("doops_agent_upgrade", args)
}

func waitForSelfUpgrade(server Server, token, session string, verbose bool, acceptance string, rollbackOnProbeFailure bool, expectedRelease string) error {
	job, namespace, release, err := parseUpgradeAcceptance(acceptance)
	if err != nil {
		return err
	}
	fmt.Printf("==> waiting for self-upgrade job %s\n", job)
	deadline := time.Now().Add(upgradeWait)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("self-upgrade job %s did not finish", job)
		}
		out, err := callUpgradeTool(server, token, session, verbose, map[string]interface{}{
			"observe_job": job,
			"namespace":   namespace,
		})
		if err == nil {
			_, fields, parseErr := parseUpgradeResponse(out)
			if parseErr != nil {
				return parseErr
			}
			if fields["job"] != job || fields["namespace"] != namespace {
				return fmt.Errorf("self-upgrade observation does not match job %s in namespace %s", job, namespace)
			}
		}
		switch classifyUpgradeObservation(out, err) {
		case "retry", "running":
			time.Sleep(upgradePollInterval)
			continue
		case "failed":
			return fmt.Errorf("%s", strings.TrimSpace(out))
		case "succeeded":
			fmt.Printf("%s\n", strings.TrimSpace(out))
			if err := probeStructuredSSH(server, token, session, verbose); err != nil {
				if !rollbackOnProbeFailure {
					return fmt.Errorf("rollback Job completed but Gateway protocol verification failed: %w", err)
				}
				return rollbackAfterProbeFailure(server, token, session, verbose, namespace, release, err)
			}
			fmt.Printf("protocol=structuredSSH status=ready\n")
			if expectedRelease != "" {
				if err := probeAgentRelease(server, token, session, verbose, expectedRelease); err != nil {
					if !rollbackOnProbeFailure {
						return fmt.Errorf("rollback Job completed but Agent release verification failed: %w", err)
					}
					return rollbackAfterProbeFailure(server, token, session, verbose, namespace, release, err)
				}
				fmt.Printf("release_commit=%s status=ready\n", expectedRelease)
			}
			return nil
		default:
			if err != nil {
				return err
			}
			return fmt.Errorf("unrecognized self-upgrade status: %s", strings.TrimSpace(out))
		}
	}
}

func rollbackAfterProbeFailure(server Server, token, session string, verbose bool, namespace, release string, probeErr error) error {
	fmt.Printf("structuredSSH probe failed: %v\n", probeErr)
	fmt.Println("==> starting rollback job")
	out, err := callUpgradeTool(server, token, session, verbose, map[string]interface{}{
		"rollback":  true,
		"namespace": namespace,
		"release":   release,
	})
	if err != nil {
		return fmt.Errorf("structuredSSH probe failed: %v; rollback was not accepted: %w", probeErr, err)
	}
	fmt.Printf("%s\n", strings.TrimSpace(out))
	if err := waitForSelfUpgrade(server, token, session, verbose, out, false, ""); err != nil {
		return fmt.Errorf("structuredSSH probe failed: %v; rollback failed: %w", probeErr, err)
	}
	return fmt.Errorf("structuredSSH probe failed and the release was rolled back: %v", probeErr)
}

func probeAgentRelease(server Server, token, session string, verbose bool, expected string) error {
	client := NewMCPClient(server, NewSessionStore(), session, verbose)
	client.Token = token
	defer client.Close()
	return client.RequireAgentRelease(expected)
}

func probeStructuredSSH(server Server, token, session string, verbose bool) error {
	deadline := time.Now().Add(time.Minute)
	var last error
	for {
		client := NewMCPClient(server, NewSessionStore(), session, verbose)
		client.Token = token
		err := client.RequireProtocol("structuredSSH")
		client.Close()
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) || !upgradeRetryable(err) {
			return last
		}
		time.Sleep(upgradePollInterval)
	}
}

func parseUpgradeAcceptance(out string) (string, string, string, error) {
	status, fields, err := parseUpgradeResponse(out)
	if err != nil {
		return "", "", "", err
	}
	if status != "self-upgrade-accepted" || fields["job"] == "" || fields["namespace"] == "" || fields["release"] == "" {
		return "", "", "", fmt.Errorf("self-upgrade acceptance was not returned")
	}
	return fields["job"], fields["namespace"], fields["release"], nil
}

func parseUpgradeResponse(out string) (string, map[string]string, error) {
	line := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", nil, fmt.Errorf("invalid self-upgrade response header: %s", line)
	}
	fields := map[string]string{}
	for _, field := range parts[1:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok || key == "" || value == "" || fields[key] != "" {
			return "", nil, fmt.Errorf("invalid self-upgrade response header: %s", line)
		}
		fields[key] = value
	}
	return parts[0], fields, nil
}

func classifyUpgradeObservation(out string, err error) string {
	if err != nil {
		if upgradeRetryable(err) {
			return "retry"
		}
		return "error"
	}
	status, _, parseErr := parseUpgradeResponse(out)
	if parseErr != nil {
		return "error"
	}
	switch status {
	case "self-upgrade-succeeded":
		return "succeeded"
	case "self-upgrade-failed":
		return "failed"
	case "self-upgrade-running":
		return "running"
	default:
		return "error"
	}
}

func upgradeRetryable(err error) bool {
	text := strings.ToLower(err.Error())
	for _, part := range []string{
		"offline",
		"connection",
		"eof",
		"websocket",
		"timeout",
		"temporarily",
		"refused",
	} {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}

func validateUpgradeImage(image string) error {
	image = strings.TrimSpace(image)
	at := strings.LastIndex(image, "@sha256:")
	if at > 0 {
		repository := image[:at]
		digest := image[at+1:]
		if !cliImageDigest.MatchString(digest) || !cliImageRepository.MatchString(repository) {
			return fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")
		}
		return nil
	}
	colon := strings.LastIndex(image, ":")
	slash := strings.LastIndex(image, "/")
	if colon <= slash || !cliImageRepository.MatchString(image[:colon]) || !cliImageTag.MatchString(image[colon+1:]) {
		return fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")
	}
	return nil
}

func filterUpgradeTargets(targets []GatewayTarget, cluster, instance string) []GatewayTarget {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	out := make([]GatewayTarget, 0, len(targets))
	for _, target := range targets {
		if cluster != "" && cluster != "*" && target.Cluster != cluster {
			continue
		}
		if instance != "" && instance != "*" && target.Instance != instance {
			continue
		}
		out = append(out, target)
	}
	return out
}

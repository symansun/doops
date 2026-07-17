package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/user/doops/agent/api"
)

const (
	cicdResultClaimed   = "claimed"
	cicdResultCompleted = "completed"
)

var cicdRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type durableCICDResult struct {
	RunID          string              `json:"runId"`
	RunDigest      string              `json:"runDigest"`
	Cluster        string              `json:"cluster"`
	Instance       string              `json:"instance"`
	Target         string              `json:"target"`
	Session        string              `json:"session"`
	SourceRevision string              `json:"sourceRevision"`
	RuntimeID      string              `json:"runtimeId"`
	State          string              `json:"state"`
	Response       api.JSONRPCResponse `json:"response,omitempty"`
	UpdatedAt      time.Time           `json:"updatedAt"`
}

type cicdResultStore struct {
	mu   sync.Mutex
	root string
}

func newCICDResultStore() *cicdResultStore {
	root := strings.TrimSpace(os.Getenv("DOOPS_CICD_RESULT_DIR"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			home = "/root"
		}
		root = filepath.Join(home, ".doops", "cicd-results")
	}
	return &cicdResultStore{root: root}
}

func credentialRunFromInstruction(instruction string) (CredentialRun, bool) {
	var envelope struct {
		Task          string        `json:"task"`
		CredentialRun CredentialRun `json:"credentialRun"`
	}
	if json.Unmarshal([]byte(instruction), &envelope) != nil ||
		strings.TrimSpace(envelope.Task) != "execute-doops-cicd" {
		return CredentialRun{}, false
	}
	run := envelope.CredentialRun
	return run, validCICDRunBinding(run)
}

func validCICDRunID(runID string) bool {
	return cicdRunIDPattern.MatchString(runID)
}

func validCICDRunBinding(run CredentialRun) bool {
	return validCICDRunID(strings.TrimSpace(run.ID)) &&
		strings.TrimSpace(run.Cluster) != "" &&
		strings.TrimSpace(run.Instance) != "" &&
		strings.TrimSpace(run.Target) != "" &&
		strings.TrimSpace(run.Session) != "" &&
		validCredentialWorkspaceCommit(run.SourceRevision)
}

func cicdRunDigest(run CredentialRun) (string, error) {
	if !validCICDRunBinding(run) {
		return "", errors.New("invalid credential run binding")
	}
	return digestJSON(run)
}

func (s *cicdResultStore) begin(run CredentialRun) (*durableCICDResult, bool, error) {
	digest, err := cicdRunDigest(run)
	if err != nil {
		return nil, false, errors.New("invalid credential run binding")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if result, err := s.readLocked(run); err == nil {
		return result, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	result := &durableCICDResult{
		RunID:          run.ID,
		RunDigest:      digest,
		Cluster:        run.Cluster,
		Instance:       run.Instance,
		Target:         run.Target,
		Session:        run.Session,
		SourceRevision: run.SourceRevision,
		RuntimeID:      agentProcessRuntimeID,
		State:          cicdResultClaimed,
		UpdatedAt:      time.Now().UTC(),
	}
	if err := s.createClaimLocked(result); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := s.readLocked(run)
			return existing, false, readErr
		}
		return nil, false, err
	}
	return result, true, nil
}

func (s *cicdResultStore) complete(run CredentialRun, response api.JSONRPCResponse) error {
	response.ID = nil
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.readLocked(run)
	if err != nil {
		return err
	}
	if existing.State != cicdResultClaimed || existing.RuntimeID != agentProcessRuntimeID {
		return errors.New("credential run is not owned by this agent runtime")
	}
	existing.State = cicdResultCompleted
	existing.Response = response
	existing.UpdatedAt = time.Now().UTC()
	return s.writeLocked(existing)
}

func (s *cicdResultStore) get(run CredentialRun) (*durableCICDResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(run)
}

func (s *cicdResultStore) readLocked(run CredentialRun) (*durableCICDResult, error) {
	digest, err := cicdRunDigest(run)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path(run.ID, digest))
	if err != nil {
		return nil, err
	}
	var result durableCICDResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode CICD result: %w", err)
	}
	if result.RunID != run.ID ||
		result.RunDigest != digest ||
		result.Cluster != run.Cluster ||
		result.Instance != run.Instance ||
		result.Target != run.Target ||
		result.Session != run.Session ||
		result.SourceRevision != run.SourceRevision ||
		(result.State != cicdResultClaimed && result.State != cicdResultCompleted) {
		return nil, errors.New("invalid CICD result record")
	}
	return &result, nil
}

func (s *cicdResultStore) createClaimLocked(result *durableCICDResult) error {
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return fmt.Errorf("create CICD result directory: %w", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode CICD result: %w", err)
	}
	path := s.path(result.RunID, result.RunDigest)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write CICD claim: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync CICD claim: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close CICD claim: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return err
	}
	published = true
	return nil
}

func (s *cicdResultStore) writeLocked(result *durableCICDResult) error {
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return fmt.Errorf("create CICD result directory: %w", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode CICD result: %w", err)
	}
	path := s.path(result.RunID, result.RunDigest)
	tmp, err := os.CreateTemp(s.root, "."+result.RunID+".*.tmp")
	if err != nil {
		return fmt.Errorf("create CICD result temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod CICD result temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write CICD result temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync CICD result temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close CICD result temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish CICD result: %w", err)
	}
	return syncDirectory(s.root)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open CICD result directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync CICD result directory: %w", err)
	}
	return nil
}

func (s *cicdResultStore) path(runID, digest string) string {
	return filepath.Join(s.root, runID+"-"+strings.TrimPrefix(digest, "sha256:")+".json")
}

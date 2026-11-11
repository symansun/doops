package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func handleCOSSync(argBytes json.RawMessage) (string, error) {
	var args struct {
		SessionID string        `json:"session_id"`
		Files     []cosSyncFile `json:"files"`
	}
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid cos sync arguments")
	}
	if err := validateSession(args.SessionID); err != nil {
		return "", err
	}
	for _, file := range args.Files {
		if _, err := validateCOSSyncFile(args.SessionID, file); err != nil {
			return "", err
		}
	}
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for _, file := range args.Files {
		if err := downloadCOSObject(client, args.SessionID, file); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("synced %d objects into the session workspace", len(args.Files)), nil
}

func downloadCOSObject(client *http.Client, sessionID string, file cosSyncFile) error {
	dest, err := validateCOSSyncFile(sessionID, file)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, file.URL, nil)
	if err != nil {
		return fmt.Errorf("cos sync request rejected")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cos sync download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cos sync download failed")
	}
	if resp.ContentLength >= 0 && resp.ContentLength != file.Size {
		return fmt.Errorf("cos sync size mismatch")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer output.Close()
	written, err := io.Copy(output, io.LimitReader(resp.Body, file.Size+1))
	if err != nil {
		return fmt.Errorf("cos sync download failed")
	}
	if written != file.Size {
		return fmt.Errorf("cos sync size mismatch")
	}
	mode := os.FileMode(0o644)
	if *file.Executable {
		mode = 0o755
	}
	if err := output.Chmod(mode); err != nil {
		return err
	}
	return output.Close()
}

func validateCOSSyncFile(sessionID string, file cosSyncFile) (string, error) {
	if file.Executable == nil {
		return "", fmt.Errorf("%s: push again from the original source to record executable files", cosErrPermissionsMissing)
	}
	if file.Size < 0 {
		return "", cosFail(cosErrManifestInvalid)
	}
	if err := validateCOSSyncURL(file.URL); err != nil {
		return "", err
	}
	return resolveWorkspaceFilePath(sessionID, file.Path)
}

func validateCOSSyncURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("cos sync url rejected")
	}
	host := parsed.Hostname()
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("cos sync url rejected")
}

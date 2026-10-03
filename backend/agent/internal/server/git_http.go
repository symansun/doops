package server

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Git HTTP is private to the authenticated Gateway tunnel. Each request owns
// its Git process group, including receive hooks, until synchronous cleanup.
type gitHTTPHandler struct {
	root        string
	gitPath     string
	postReceive string
}

func newGitHTTPHandler(root, postReceive string) (*gitHTTPHandler, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	handler := &gitHTTPHandler{root: root, gitPath: "git", postReceive: postReceive}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".git") {
			if err := handler.installHook(filepath.Join(root, entry.Name())); err != nil {
				return nil, err
			}
		}
	}
	return handler, nil
}

func (h *gitHTTPHandler) installHook(repo string) error {
	if h.postReceive == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(repo, "hooks", "post-receive"), []byte(h.postReceive), 0755)
}

func (h *gitHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 || !strings.HasSuffix(parts[0], ".git") || parts[0] == ".git" ||
		strings.Contains(parts[0], "..") || strings.ContainsAny(parts[0], "\\\x00") {
		http.Error(w, "invalid Git repository path", http.StatusBadRequest)
		return
	}
	endpoint := strings.Join(parts[1:], "/")
	service := endpoint
	advertise := r.Method == http.MethodGet && endpoint == "info/refs"
	if advertise {
		service = r.URL.Query().Get("service")
	}
	if (service != "git-upload-pack" && service != "git-receive-pack") ||
		(!advertise && (r.Method != http.MethodPost || len(parts) != 2)) {
		http.Error(w, "unsupported Git service", http.StatusBadRequest)
		return
	}
	if err := h.serve(w, r, parts[0], service, advertise); err != nil {
		// A failure after headers have streamed must still reach the tunnel's
		// terminal response; an HTTP 200 header is not execution evidence.
		if reporter, ok := w.(interface{ gitExecutionFailed(error) }); ok {
			reporter.gitExecutionFailed(err)
		}
		http.Error(w, "Git execution failed", http.StatusInternalServerError)
	}
}

func (h *gitHTTPHandler) serve(w http.ResponseWriter, r *http.Request, name, service string, advertise bool) error {
	repo := filepath.Join(h.root, name)
	info, err := os.Lstat(repo)
	if os.IsNotExist(err) {
		if err := runGitHTTPCommand(r.Context(), exec.Command(h.gitPath, "init", "--bare", repo), nil, io.Discard, ""); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Git repository is not a directory")
	}
	for _, dir := range []string{"objects", "hooks"} {
		info, err := os.Stat(filepath.Join(repo, dir))
		if err != nil || !info.IsDir() {
			return fmt.Errorf("incomplete Git repository: missing %s directory", dir)
		}
	}
	// Init may have exited before cancellation was observed but before hooks
	// were installed. Every admitted request ensures the required hook exists.
	if err := h.installHook(repo); err != nil {
		return err
	}

	args := []string{strings.TrimPrefix(service, "git-"), "--stateless-rpc"}
	var body io.ReadCloser
	var prelude string
	contentType := "application/x-" + service + "-result"
	if advertise {
		args = append(args, "--advertise-refs")
		line := "# service=" + service + "\n"
		prelude = fmt.Sprintf("%04x%s0000", len(line)+4, line)
		contentType = "application/x-" + service + "-advertisement"
	} else {
		body = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			reader, err := gzip.NewReader(r.Body)
			if err != nil {
				return err
			}
			defer reader.Close()
			// Closing the decoder does not unblock the underlying request body.
			body = &gitDecodedBody{Reader: reader, Closer: r.Body}
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	return runGitHTTPCommand(r.Context(), exec.Command(h.gitPath, append(args, repo)...), body, w, prelude)
}

type gitDecodedBody struct {
	io.Reader
	io.Closer
}

func runGitHTTPCommand(ctx context.Context, cmd *exec.Cmd, body io.ReadCloser, output io.Writer, prelude string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdout.Close()
	defer stdoutWriter.Close()
	cmd.Stdout = stdoutWriter
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdin, stdinWriter *os.File
	if body != nil {
		stdin, stdinWriter, err = os.Pipe()
		if err != nil {
			return err
		}
		defer stdin.Close()
		defer stdinWriter.Close()
		cmd.Stdin = stdin
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	cleanupDone := make(chan struct{})
	stopCleanup := context.AfterFunc(ctx, func() {
		defer close(cleanupDone)
		if body != nil {
			_ = body.Close()
			_ = stdinWriter.Close()
		}
		_ = stdout.Close()
	})
	defer func() {
		if !stopCleanup() {
			<-cleanupDone
		}
	}()
	_ = stdoutWriter.Close()
	inputDone := make(chan error, 1)
	if body != nil {
		_ = stdin.Close()
		go func() {
			_, err := io.Copy(stdinWriter, body)
			_ = stdinWriter.Close()
			if err != nil {
				cancel()
			}
			inputDone <- err
		}()
	} else {
		inputDone <- nil
	}
	outputDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(output, prelude)
		if err == nil {
			_, err = io.Copy(output, stdout)
		}
		if err != nil {
			cancel()
		}
		outputDone <- err
	}()
	commandDone := make(chan error, 1)
	go func() { commandDone <- cmd.Wait() }()
	// Direct file descriptors let Wait reap Git without racing output drainage.
	err = waitProcessGroup(ctx, cmd.Process.Pid, commandDone)
	if err != nil {
		cancel()
	}
	// Git can exit successfully before the tunnel's separate EOF frame arrives.
	// Keep input ownership until EOF; cancellation still closes both streams.
	inputErr, outputErr := <-inputDone, <-outputDone
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if inputErr != nil {
		return inputErr
	}
	return outputErr
}

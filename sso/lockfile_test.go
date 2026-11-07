package sso

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fileStore is a TokenStore in a file, shared by the test's processes the way a keyring is.
type fileStore struct{ path string }

type fileTokens struct {
	Access  string    `json:"access"`
	Refresh string    `json:"refresh"`
	Expires time.Time `json:"expires"`
}

func (f fileStore) Load() (Tokens, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return Tokens{}, ErrNoTokens
	}
	var t fileTokens
	if err := json.Unmarshal(b, &t); err != nil {
		return Tokens{}, err
	}
	return Tokens(t), nil
}

func (f fileStore) Save(t Tokens) error {
	b, _ := json.Marshal(fileTokens(t))
	return os.WriteFile(f.path, b, 0o600)
}

const helperEnv = "SSO_LOCK_HELPER"

// TestLockFileHelper is the child process of TestLockFileStopsTwoProcessesSpendingOneRefreshToken, not a test.
func TestLockFileHelper(t *testing.T) {
	dir := os.Getenv(helperEnv)
	if dir == "" {
		t.Skip("child process only")
	}
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	o := OAuth{ClientID: "cid", AuthBase: os.Getenv(helperEnv + "_BASE"), Store: fileStore{filepath.Join(dir, "tokens.json")}}
	if os.Getenv(helperEnv+"_LOCK") == "1" {
		o.LockFile = filepath.Join(dir, "tokens.lock")
	}
	if _, err := o.Fresh(t.Context()); err != nil {
		fmt.Fprintln(os.Stdout, "RESULT", err)
		return
	}
	fmt.Fprintln(os.Stdout, "RESULT ok")
}

func TestLockFileStopsTwoProcessesSpendingOneRefreshToken(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		t.Skip("child process")
	}
	run := func(lock bool) (revoked int, refreshes int32) {
		dir := t.TempDir()
		if err := (fileStore{filepath.Join(dir, "tokens.json")}).Save(Tokens{Access: "old", Refresh: "R0", Expires: time.Now().Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		var current atomic.Value
		current.Store("R0")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			atomic.AddInt32(&refreshes, 1)
			time.Sleep(150 * time.Millisecond)
			if r.PostForm.Get("refresh_token") != current.Load() {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			next := fmt.Sprintf("R%d", atomic.LoadInt32(&refreshes))
			current.Store(next)
			_, _ = fmt.Fprintf(w, `{"access_token":"A","refresh_token":%q,"expires_in":3600}`, next)
		}))
		defer srv.Close()
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		var cmds []*exec.Cmd
		var starts []io.WriteCloser
		var outs []*strings.Builder
		for range 4 {
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestLockFileHelper$", "-test.v") //nolint:gosec // G204: re-runs this test binary as the second process
			cmd.Env = append(os.Environ(), helperEnv+"="+dir, helperEnv+"_BASE="+srv.URL, helperEnv+"_LOCK="+map[bool]string{true: "1", false: "0"}[lock])
			out := &strings.Builder{}
			cmd.Stdout = out
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			starts = append(starts, in)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			cmds, outs = append(cmds, cmd), append(outs, out)
		}
		for _, in := range starts {
			_, _ = io.WriteString(in, "go\n")
			_ = in.Close()
		}
		for i, cmd := range cmds {
			if err := cmd.Wait(); err != nil {
				t.Fatalf("child: %v\n%s", err, outs[i])
			}
			if !strings.Contains(outs[i].String(), "RESULT ok") {
				revoked++
			}
		}
		return revoked, atomic.LoadInt32(&refreshes)
	}
	if revoked, n := run(true); revoked != 0 || n != 1 {
		t.Fatalf("with the lock file: %d processes failed, %d refreshes (want 0 and 1)", revoked, n)
	}
	if revoked, _ := run(false); revoked == 0 {
		t.Fatal("without the lock file no process lost the race, so the test proves nothing")
	}
}

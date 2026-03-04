//go:build cgo && typedb

package driver

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNoTestsStartupPathDoesNotHang(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, ".."))

	cmd := exec.Command("go", "test", "./driver", "-tags", "cgo,typedb", "-run", "^$", "-count=1", "-timeout", "20s")
	cmd.Dir = repoRoot
	out, err, timedOut := runCommandWithHardTimeout(cmd, 30*time.Second)
	if timedOut {
		t.Fatalf("startup-only go test hung beyond timeout; output:\n%s", out)
	}
	if err != nil {
		t.Fatalf("startup-only go test failed: %v\noutput:\n%s", err, out)
	}
}

func TestOpenInvalidAddressReturnsWithoutHang(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestOpenInvalidAddressHelperProcess")
	cmd.Env = append(os.Environ(), "GO_TYPEQL_HELPER_INVALID_OPEN=1")
	out, err, timedOut := runCommandWithHardTimeout(cmd, 8*time.Second)
	if timedOut {
		t.Fatalf("invalid-address open helper hung beyond timeout; output:\n%s", out)
	}
	if err != nil {
		t.Fatalf("invalid-address open helper failed: %v\noutput:\n%s", err, out)
	}
}

func TestOpenInvalidAddressHelperProcess(t *testing.T) {
	if os.Getenv("GO_TYPEQL_HELPER_INVALID_OPEN") != "1" {
		return
	}
	driver, err := OpenWithTLS("127.0.0.1:1", "admin", "password", false, "")
	if err == nil {
		if driver != nil {
			driver.Close()
		}
		t.Fatal("expected open error for invalid address")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "connect") &&
		!strings.Contains(strings.ToLower(err.Error()), "refused") &&
		!strings.Contains(strings.ToLower(err.Error()), "unreachable") &&
		!strings.Contains(strings.ToLower(err.Error()), "timeout") {
		// Error wording can vary by platform/runtime; we only require fast failure.
		t.Logf("open returned non-connection error (still acceptable for hang regression): %v", err)
	}
}

func runCommandWithHardTimeout(cmd *exec.Cmd, timeout time.Duration) (string, error, bool) {
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		return out.String(), err, false
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return out.String(), err, false
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			time.Sleep(250 * time.Millisecond)
			_ = cmd.Process.Kill()
		}
		return out.String(), fmt.Errorf("command timed out after %s", timeout), true
	}
}

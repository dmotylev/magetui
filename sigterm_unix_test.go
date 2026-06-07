//go:build unix

package magetui_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dmotylev/magetui"
)

// TestSigterm_TeardownAndExitCode is the one place a real signal crosses a
// process boundary: the test re-executes itself as a child whose Target
// lingers until signaled, then asserts the conventional exit code and the
// ⊘ teardown output.
func TestSigterm_TeardownAndExitCode(t *testing.T) {
	if os.Getenv("MAGETUI_TEST_SIGTERM_CHILD") == "1" {
		sigtermChild()
		return // unreachable: sigtermChild exits the process
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestSigterm_TeardownAndExitCode$")
	cmd.Env = append(os.Environ(), "MAGETUI_TEST_SIGTERM_CHILD=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	watchdog := time.AfterFunc(10*time.Second, func() { _ = cmd.Process.Kill() })
	defer watchdog.Stop()

	// Wait until the lingering step announces itself: by then the SIGTERM
	// watcher is long registered.
	var collected strings.Builder
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(&collected, line)
		if strings.Contains(line, "lingerForSignal") && strings.Contains(line, "started") {
			break
		}
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for sc.Scan() {
		fmt.Fprintln(&collected, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the child's output: %v", err)
	}

	err = cmd.Wait()
	var xe *exec.ExitError
	if !errors.As(err, &xe) {
		t.Fatalf("child exited %v, want an exit error", err)
	}
	if got := xe.ExitCode(); got != 143 {
		t.Errorf("child exit code = %d, want 143 (128+SIGTERM)\noutput:\n%s", got, collected.String())
	}
	got := collected.String()
	if !strings.Contains(got, "⊘ lingerForSignal") {
		t.Errorf("no ⊘ teardown line in the child's output:\n%s", got)
	}
	if !strings.Contains(got, "interrupted, 1 of 1 steps did not finish.") {
		t.Errorf("no interruption acknowledgment in the child's output:\n%s", got)
	}
}

// sigtermChild plays the magefile: a Target with one dependency that
// lingers until the context is canceled, exiting with the code mage's
// machinery would extract.
func sigtermChild() {
	err := magetui.Target(context.Background(), func(ctx context.Context) error {
		magetui.Deps(ctx, lingerForSignal)
		return nil
	})
	code := 0
	if err != nil {
		code = 1
		// Not errors.AsType: the target is a bare interface without an
		// Error method, which AsType's constraint rejects.
		var ec interface{ ExitStatus() int }
		if errors.As(err, &ec) {
			code = ec.ExitStatus()
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
	os.Exit(code)
}

func lingerForSignal(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

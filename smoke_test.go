package markedup_test

import (
	"os/exec"
	"testing"
)

func TestBinaryBuilds(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", t.TempDir()+"/markedup", "./cmd/markedup")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/markedup failed: %v\n%s", err, out)
	}
}

func TestCheckValidExitsZero(t *testing.T) {
	// Build the binary first.
	build := exec.Command("go", "build", "-o", "markedup_test_bin", "./cmd/markedup")
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("rm", "-f", "markedup_test_bin").Run()
	})

	cmd := exec.Command("./markedup_test_bin", "check", "testdata/valid")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("markedup check testdata/valid should exit 0, got: %v\n%s", err, out)
	}
}

func TestCheckInvalidExitsNonZero(t *testing.T) {
	// Build the binary first.
	build := exec.Command("go", "build", "-o", "markedup_test_bin", "./cmd/markedup")
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("rm", "-f", "markedup_test_bin").Run()
	})

	cmd := exec.Command("./markedup_test_bin", "check", "testdata/invalid")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("markedup check testdata/invalid should exit non-zero, but exited 0\n%s", out)
	}
	// Verify it's an exit error (not some other failure).
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
}

package config

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

// restoreGlobals saves and restores the package-level function variables
// so tests don't leak state.
func restoreGlobals(t *testing.T) {
	t.Helper()
	origExecCommand := execCommand
	origLookPath := lookPath
	t.Cleanup(func() {
		execCommand = origExecCommand
		lookPath = origLookPath
	})
}

// --- OllamaAvailable / HFAvailable ---

func TestOllamaAvailable_True(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(file string) (string, error) {
		if file == "ollama" {
			return "/usr/local/bin/ollama", nil
		}
		return "", exec.ErrNotFound
	}
	if !OllamaAvailable() {
		t.Fatal("expected OllamaAvailable() == true when ollama is in PATH")
	}
}

func TestOllamaAvailable_False(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	if OllamaAvailable() {
		t.Fatal("expected OllamaAvailable() == false when ollama is not in PATH")
	}
}

func TestHFAvailable_True(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(file string) (string, error) {
		if file == "hf" {
			return "/usr/local/bin/hf", nil
		}
		return "", exec.ErrNotFound
	}
	if !HFAvailable() {
		t.Fatal("expected HFAvailable() == true when hf is in PATH")
	}
}

func TestHFAvailable_False(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	if HFAvailable() {
		t.Fatal("expected HFAvailable() == false when hf is not in PATH")
	}
}

// --- OllamaPull ---

func TestOllamaPull_BinaryNotFound(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	err := OllamaPull(context.Background(), "llama3", &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when ollama binary is not found")
	}
	if !errorContains(err, "ollama binary not found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestOllamaPull_StreamsOutput(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(file string) (string, error) {
		if file == "ollama" {
			return "/usr/local/bin/ollama", nil
		}
		return "", exec.ErrNotFound
	}
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Verify arguments
		if name != "ollama" || len(args) != 2 || args[0] != "pull" || args[1] != "llama3" {
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return exec.CommandContext(ctx, "echo", "pulling llama3...")
	}

	var buf bytes.Buffer
	if err := OllamaPull(context.Background(), "llama3", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected output to be streamed to writer")
	}
}

// --- HFPull ---

func TestHFPull_BinaryNotFound(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	err := HFPull(context.Background(), "some/model", &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when hf binary is not found")
	}
	if !errorContains(err, "hf binary not found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestHFPull_StreamsOutput(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(file string) (string, error) {
		if file == "hf" {
			return "/usr/local/bin/hf", nil
		}
		return "", exec.ErrNotFound
	}
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != "hf" || len(args) != 2 || args[0] != "download" || args[1] != "some/model" {
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return exec.CommandContext(ctx, "echo", "downloading some/model...")
	}

	var buf bytes.Buffer
	if err := HFPull(context.Background(), "some/model", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected output to be streamed to writer")
	}
}

// --- Context cancellation ---

func TestOllamaPull_ContextCancellation(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(file string) (string, error) {
		if file == "ollama" {
			return "/usr/local/bin/ollama", nil
		}
		return "", exec.ErrNotFound
	}
	execCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		// Use sleep to simulate a long-running process that can be cancelled
		return exec.CommandContext(ctx, "sleep", "30")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := OllamaPull(ctx, "llama3", &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestHFPull_ContextCancellation(t *testing.T) {
	restoreGlobals(t)
	lookPath = func(file string) (string, error) {
		if file == "hf" {
			return "/usr/local/bin/hf", nil
		}
		return "", exec.ErrNotFound
	}
	execCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "30")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := HFPull(ctx, "some/model", &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// errorContains checks if an error's message contains a substring.
func errorContains(err error, substr string) bool {
	if err == nil {
		return false
	}
	return bytes.Contains([]byte(err.Error()), []byte(substr))
}

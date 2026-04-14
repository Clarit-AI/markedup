// Package config provides configuration helpers for the markedup CLI,
// including model download utilities for Ollama and HuggingFace.
package config

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// execCommand is the function used to create exec.Cmd instances.
// It can be overridden in tests to mock command execution.
var execCommand = exec.CommandContext

// lookPath is the function used to check if a binary exists in PATH.
// It can be overridden in tests to mock binary detection.
var lookPath = exec.LookPath

// OllamaAvailable reports whether the ollama CLI binary is in PATH.
func OllamaAvailable() bool {
	_, err := lookPath("ollama")
	return err == nil
}

// HFAvailable reports whether the hf CLI binary is in PATH.
func HFAvailable() bool {
	_, err := lookPath("hf")
	return err == nil
}

// OllamaPull runs "ollama pull <model>" and streams output to w.
// The process is terminated if ctx is cancelled.
func OllamaPull(ctx context.Context, model string, w io.Writer) error {
	if !OllamaAvailable() {
		return fmt.Errorf("ollama binary not found in PATH: %w", exec.ErrNotFound)
	}
	cmd := execCommand(ctx, "ollama", "pull", model)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ollama pull %s: %w", model, err)
	}
	return nil
}

// HFPull runs "hf download <model>" and streams output to w.
// The process is terminated if ctx is cancelled.
func HFPull(ctx context.Context, model string, w io.Writer) error {
	if !HFAvailable() {
		return fmt.Errorf("hf binary not found in PATH: %w", exec.ErrNotFound)
	}
	cmd := execCommand(ctx, "hf", "download", model)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hf download %s: %w", model, err)
	}
	return nil
}

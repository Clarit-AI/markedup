// Copyright 2025 MarkedUp authors. All rights reserved.
// Use of this source code is governed by a MIT-style license that can be
// found in the LICENSE file.

package enrich

import (
	"context"

	"github.com/Clarit-AI/markedup/llm"
	"testing"
)

// TestLlmClientCompatibility verifies that the llm.Client used by the
// enrichment tier still exposes the expected API surface for making
// chat completion calls. This test serves as a compatibility gate:
// if a dependency is bumped in a way that breaks the contract, this
// test will fail to compile or fail at runtime with a clear message.
func TestLlmClientCompatibility(t *testing.T) {
	// Create a client with a dummy endpoint. We don't expect the call to
	// succeed because there's no server, but we want to verify that the
	// API surface is intact.
	client := llm.NewClient(llm.Config{
		Endpoint: "http://localhost:12345", // intentionally wrong
		Model:    "test-model",
	})

	// Call ChatCompletion to ensure the method exists and has the expected
	// signature. We ignore the error because we only care that the call
	// compiles and the method is present. If the method signature changes,
	// this test will fail to compile.
	_, err := client.ChatCompletion(context.TODO(), []llm.Message{})
	if err == nil {
		// If we somehow get a successful call, that's unexpected but not
		// a failure of the compatibility test itself.
		t.Errorf("expected error from failed connection, got success")
	}
	// We don't assert on the error because any error (connection refused, etc.)
	// indicates the method was callable, which is what we want to verify.
}
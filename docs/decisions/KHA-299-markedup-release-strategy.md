# KHA-299: MarkedUp Release Strategy

## Decision

Adopt a release strategy of pinned dependencies accompanied by a compatibility-test gate.

## Rationale

To ensure stability and predictability in releases, we pin all dependencies to specific versions. This prevents unexpected updates from introducing breaking changes. Additionally, we implement a compatibility test that verifies the pinned dependencies still expose the expected API surface that MarkedUp relies on, particularly the OpenAI-compatible client used by the enrichment tier.

## Implementation

1. Dependencies are pinned in `go.mod` to specific versions.
2. A compatibility test is run in CI to ensure that the pinned versions of critical dependencies (e.g., the OpenAI client) still provide the expected interface.
3. If a dependency update breaks compatibility, the test fails, alerting developers to review and either update the code or maintain the pin.

## Compatibility Test

The compatibility test should verify that the OpenAI-compatible client used in `enrich/` still exposes the necessary methods and types for making chat completion calls. A compile-time check of the client type and its constructor/signature is sufficient.

## References

- See `go.mod` for pinned versions.
- The compatibility test is implemented in the test suite.
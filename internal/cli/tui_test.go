package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Clarit-AI/markedup/config"
	"github.com/Clarit-AI/markedup/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTUI_FirstRunShowsSplashBeforeSetup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	restoreTUIHooks(t)

	var calls []string
	runStartupSplash = func() (bool, error) {
		calls = append(calls, "splash")
		return false, nil
	}
	runSetupWizard = func() (*config.Config, error) {
		calls = append(calls, "setup")
		return &config.Config{}, nil
	}
	hydrateKeys = func(_ *config.Config) error { return nil }
	runTUIProgram = func(_ *index.KnowledgeIndex, _ string, _ *config.Config) error {
		calls = append(calls, "tui")
		return nil
	}

	require.NoError(t, runTUI(dir))
	assert.Equal(t, []string{"splash", "setup", "tui"}, calls)
}

func TestRunTUI_FirstRunCtrlCDuringSplashCancelsSetup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	restoreTUIHooks(t)

	var calls []string
	runStartupSplash = func() (bool, error) {
		calls = append(calls, "splash")
		return true, nil
	}
	runSetupWizard = func() (*config.Config, error) {
		calls = append(calls, "setup")
		return &config.Config{}, nil
	}
	hydrateKeys = func(_ *config.Config) error { return nil }
	runTUIProgram = func(_ *index.KnowledgeIndex, _ string, _ *config.Config) error {
		calls = append(calls, "tui")
		return nil
	}

	require.NoError(t, runTUI(dir))
	assert.Equal(t, []string{"splash"}, calls)
}

func TestRunTUI_ConfiguredLaunchLeavesSplashToRootModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".markedup.yaml"), []byte("{}\n"), 0644))
	restoreTUIHooks(t)

	var calls []string
	runStartupSplash = func() (bool, error) {
		calls = append(calls, "splash")
		return false, nil
	}
	runSetupWizard = func() (*config.Config, error) {
		calls = append(calls, "setup")
		return &config.Config{}, nil
	}
	hydrateKeys = func(_ *config.Config) error { return nil }
	runTUIProgram = func(_ *index.KnowledgeIndex, _ string, _ *config.Config) error {
		calls = append(calls, "tui")
		return nil
	}

	require.NoError(t, runTUI(dir))
	assert.Equal(t, []string{"tui"}, calls)
}

func restoreTUIHooks(t *testing.T) {
	t.Helper()

	originalSetup := runSetupWizard
	originalSplash := runStartupSplash
	originalRun := runTUIProgram
	originalHydrate := hydrateKeys
	originalConfig := appConfig

	t.Cleanup(func() {
		runSetupWizard = originalSetup
		runStartupSplash = originalSplash
		runTUIProgram = originalRun
		hydrateKeys = originalHydrate
		appConfig = originalConfig
	})
}

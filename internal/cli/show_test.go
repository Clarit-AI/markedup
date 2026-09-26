package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `markedup show` takes an ambiguous single argument: it may name a knowledge
// base or a page. The disambiguation used to be unimplemented — the branch
// always treated the argument as a page ID — so `markedup show <kb>` looked up
// a page whose ID was the path, found nothing, and exited 1 with no output.

// looksLikePath is the disambiguation, tested directly so the rules are
// pinned independently of cobra wiring.
func TestLooksLikePath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "kb"), 0o755))

	cases := []struct {
		name string
		arg  string
		want bool
	}{
		{"empty", "", false},
		{"plain slug id", "alice", false},
		{"slug with dashes", "knowledge-graph", false},
		{"existing directory", filepath.Join(dir, "kb"), true},
		{"dot", ".", true},
		{"absolute path", dir, true},
		{"nested path", filepath.Join(dir, "kb", "sub"), true},
		{"trailing separator", dir + string(os.PathSeparator), true},
		// A path that does not exist still reads as a path: the separator is
		// the signal, so a typo'd directory produces a load error naming the
		// path rather than a confusing "page not found".
		{"nonexistent path with separator", "/no/such/kb", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, looksLikePath(tc.arg))
		})
	}
}

func TestRunShow_StatsForPathArgument(t *testing.T) {
	kb := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(kb, "alpha.md"),
		[]byte("# Alpha\n\nLinks to [[Beta]].\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(kb, "beta.md"),
		[]byte("# Beta\n\nContent.\n"), 0o644))

	cmd := newShowCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{kb})

	require.NoError(t, cmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "Pages:")
	assert.Contains(t, out, "2", "both pages should be counted")
	// The old failure was a silent exit 1; assert we produce real output.
	assert.NotEmpty(t, out)
}

func TestRunShow_PageIDFromCurrentDir(t *testing.T) {
	kb := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(kb, "alpha.md"),
		[]byte("# Alpha\n\nContent.\n"), 0o644))

	// A bare slug must still resolve as a page ID, not a path.
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(kb))
	t.Cleanup(func() { _ = os.Chdir(wd) })

	cmd := newShowCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"alpha"})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "Alpha")
}

func TestRunShow_PathAndID(t *testing.T) {
	kb := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(kb, "alpha.md"),
		[]byte("# Alpha\n\nContent.\n"), 0o644))

	cmd := newShowCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{kb, "alpha"})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "Alpha")
}

func TestRunShow_UnknownIDStillErrors(t *testing.T) {
	kb := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(kb, "alpha.md"),
		[]byte("# Alpha\n\nContent.\n"), 0o644))

	cmd := newShowCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{kb, "does-not-exist"})

	err := cmd.Execute()
	require.Error(t, err, "an unknown page ID must still be an error")
	assert.Contains(t, err.Error(), "not found")
}

// Package enrich — coding-agent handoff (Phase 3, #142).
//
// When the LLM fallback (Phase 1, #140) is unconfigured OR fails AND the
// user is running interactively (TTY), surface a prompt offering to hand the
// remaining failure queue to an external coding agent (claude, codex, …).
// On accept we write a structured retry log to ~/.markedup/logs/ and print a
// suggested command line that pipes the log into the first coding agent
// found on PATH. The user runs that externally, then re-feeds the agent's
// YAML output back via `markedup enrich --apply-fallback <result>.yaml`,
// which routes through the same MergeModelResult path used by NuExtract and
// the LLM fallback so frontmatter shape stays canonical.
package enrich

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Clarit-AI/markedup/markdown"
	"github.com/Clarit-AI/markedup/schema"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// DefaultCodingAgents is the PATH probe order. First found wins. Kept as a
// package var (not const) so tests and future config can override it.
var DefaultCodingAgents = []string{"claude", "codex"}

// IsTerminal reports whether fd refers to a terminal. Wrapped here so the
// CLI layer doesn't have to take a direct golang.org/x/term dependency, and
// so tests can stub stdin without touching the real terminal.
func IsTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

// IsInteractive returns true when stdin is attached to a terminal. The
// handoff prompt is suppressed when this is false (cron, piped runs, CI).
func IsInteractive() bool {
	return IsTerminal(os.Stdin.Fd())
}

// FindCodingAgent scans PATH for the first known coding-agent binary in
// preference order. Returns the resolved absolute path and the agent's short
// name, or ("", "", false) when none are installed.
func FindCodingAgent(candidates []string) (path, name string, ok bool) {
	if len(candidates) == 0 {
		candidates = DefaultCodingAgents
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, c, true
		}
	}
	return "", "", false
}

// HandoffPaths bundles the on-disk artifacts produced for one handoff
// session — the retry log we write now and the result file the user is
// expected to produce by running the suggested command.
type HandoffPaths struct {
	LogPath    string // ~/.markedup/logs/failed-enrichment-<ts>.md
	ResultPath string // ~/.markedup/logs/retry-result-<ts>.yaml
}

// SuggestedCommand returns the shell line printed to the user after the
// retry log is written. Uses the first coding agent on PATH; falls back to
// "claude" as a placeholder when none are installed (the user can sub in
// whatever they have).
func (h HandoffPaths) SuggestedCommand(agentName string) string {
	if agentName == "" {
		agentName = DefaultCodingAgents[0]
	}
	return fmt.Sprintf("%s -p %s --output-format yaml > %s",
		agentName, h.LogPath, h.ResultPath)
}

// HandoffJob is one unit in the retry log. Mirrors FallbackJob but also
// carries the LLM-fallback error (if the LLM fallback ran and failed) so
// the coding agent has full context on what was already tried.
type HandoffJob struct {
	Path        string
	RelPath     string
	Body        string
	RawResponse string // primary Tier 2 raw output (may be empty)
	PrimaryErr  error  // primary parse error
	FallbackErr error  // LLM fallback error (may be nil if fallback was unconfigured)
	EntityTypes []string
	Predicates  []string
}

// timestampedLogPaths picks a deterministic timestamp and assembles the
// log/result file paths under ~/.markedup/logs/. Exposed for tests via
// dirOverride; production callers pass "".
func timestampedLogPaths(dirOverride string, now time.Time) (HandoffPaths, error) {
	dir := dirOverride
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return HandoffPaths{}, fmt.Errorf("handoff: resolve home dir: %w", err)
		}
		dir = filepath.Join(home, ".markedup", "logs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return HandoffPaths{}, fmt.Errorf("handoff: mkdir %s: %w", dir, err)
	}
	ts := now.Format("20060102-1504")
	return HandoffPaths{
		LogPath:    filepath.Join(dir, fmt.Sprintf("failed-enrichment-%s.md", ts)),
		ResultPath: filepath.Join(dir, fmt.Sprintf("retry-result-%s.yaml", ts)),
	}, nil
}

// LogPathsNow returns a fresh pair of log/result paths under
// ~/.markedup/logs/ using the current wall clock.
func LogPathsNow() (HandoffPaths, error) {
	return timestampedLogPaths("", time.Now())
}

// LogPathsIn returns a fresh pair of log/result paths under dir. Used by
// tests so retry logs don't pollute the real user home.
func LogPathsIn(dir string) (HandoffPaths, error) {
	return timestampedLogPaths(dir, time.Now())
}

// WriteRetryLog renders jobs into a markdown brief the coding agent can
// read directly. Format is intentionally human-friendly (per-file H2 with
// path, raw response, parse error, and a suggested-frontmatter schema
// block) — coding agents do best with prose + fenced examples, not raw
// JSON dumps. Output path is the LogPath returned from LogPathsNow().
func WriteRetryLog(path string, jobs []HandoffJob) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("handoff: create retry log: %w", err)
	}
	defer f.Close()
	return writeRetryLogTo(f, jobs)
}

func writeRetryLogTo(w io.Writer, jobs []HandoffJob) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintln(bw, "# MarkedUp — failed Tier 2 enrichment retry log")
	fmt.Fprintln(bw)
	fmt.Fprintln(bw, "The MarkedUp `enrich` command's primary Tier 2 extractor (NuExtract / Triplex)")
	fmt.Fprintln(bw, "and its LLM fallback both failed (or no LLM fallback was configured) for the")
	fmt.Fprintln(bw, "files below. Please re-extract the requested frontmatter for each file and")
	fmt.Fprintln(bw, "emit a single YAML document at the end of your response with this shape:")
	fmt.Fprintln(bw)
	fmt.Fprintln(bw, "```yaml")
	fmt.Fprintln(bw, "files:")
	fmt.Fprintln(bw, "  - path: <absolute path exactly as listed below>")
	fmt.Fprintln(bw, "    entity_type: <one of: person, organization, concept, project, technology, event, location, document>")
	fmt.Fprintln(bw, "    summary: <one-sentence description of what this document represents>")
	fmt.Fprintln(bw, "    entities:")
	fmt.Fprintln(bw, "      - {name: <string>, type: <UPPERCASE_TYPE>}")
	fmt.Fprintln(bw, "    relationships:")
	fmt.Fprintln(bw, "      - {subject: <entity name>, predicate: <lower_snake>, object: <entity name>}")
	fmt.Fprintln(bw, "```")
	fmt.Fprintln(bw)
	fmt.Fprintln(bw, "Re-feed your YAML output back to MarkedUp via:")
	fmt.Fprintln(bw)
	fmt.Fprintln(bw, "    markedup enrich --apply-fallback <your-result>.yaml")
	fmt.Fprintln(bw)
	fmt.Fprintf(bw, "## %d file(s) need re-extraction\n", len(jobs))
	fmt.Fprintln(bw)

	for i, j := range jobs {
		fmt.Fprintf(bw, "### %d. %s\n", i+1, j.RelPath)
		fmt.Fprintln(bw)
		fmt.Fprintf(bw, "- **Absolute path:** `%s`\n", j.Path)
		if j.PrimaryErr != nil {
			fmt.Fprintf(bw, "- **Primary parse error:** %s\n", oneLine(j.PrimaryErr.Error()))
		}
		if j.FallbackErr != nil {
			fmt.Fprintf(bw, "- **LLM fallback error:** %s\n", oneLine(j.FallbackErr.Error()))
		}
		if len(j.EntityTypes) > 0 {
			fmt.Fprintf(bw, "- **Allowed entity types:** %s\n", strings.Join(j.EntityTypes, ", "))
		}
		if len(j.Predicates) > 0 {
			fmt.Fprintf(bw, "- **Allowed predicates:** %s\n", strings.Join(j.Predicates, ", "))
		}
		fmt.Fprintln(bw)
		if j.RawResponse != "" {
			fmt.Fprintln(bw, "**Raw primary model response (unparseable):**")
			fmt.Fprintln(bw)
			fmt.Fprintln(bw, "```")
			fmt.Fprintln(bw, truncate(j.RawResponse, 4000))
			fmt.Fprintln(bw, "```")
			fmt.Fprintln(bw)
		}
		fmt.Fprintln(bw, "**Document body:**")
		fmt.Fprintln(bw)
		fmt.Fprintln(bw, "```markdown")
		fmt.Fprintln(bw, j.Body)
		fmt.Fprintln(bw, "```")
		fmt.Fprintln(bw)
	}
	return bw.Flush()
}

// oneLine collapses an error message to a single line so the markdown bullet
// list stays readable when the underlying error embeds raw model output.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// PromptYesNo writes the question to out and reads a single line from in.
// Returns true on "y"/"yes" (case-insensitive); anything else (including
// empty / EOF) is treated as no — matching the [y/N] default in the prompt.
func PromptYesNo(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprint(out, question)
	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// FallbackResultDoc is the YAML document the user's coding agent is asked
// to produce. It's a deliberately permissive shape: extra fields are
// ignored, missing fields are tolerated by the per-file merge.
type FallbackResultDoc struct {
	Files []FallbackResultFile `yaml:"files"`
}

// FallbackResultFile is one file's worth of recovered Tier 2 metadata.
// Field names mirror the retry-log schema block so a coding agent that
// reads the log can produce valid YAML without additional translation.
type FallbackResultFile struct {
	Path          string              `yaml:"path"`
	EntityType    string              `yaml:"entity_type,omitempty"`
	Summary       string              `yaml:"summary,omitempty"`
	Entities      []FallbackEntity    `yaml:"entities,omitempty"`
	Relationships []FallbackRelation  `yaml:"relationships,omitempty"`
}

// FallbackEntity matches the entities[] item shape in the retry log.
type FallbackEntity struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

// FallbackRelation matches the relationships[] item shape in the retry log
// (subject/predicate/object — the same SPO triple the LLM fallback emits).
type FallbackRelation struct {
	Subject   string `yaml:"subject"`
	Predicate string `yaml:"predicate"`
	Object    string `yaml:"object"`
}

// ParseFallbackResult unmarshals YAML produced by an external coding agent.
// Tolerates the YAML being wrapped in code fences (```yaml … ```) so users
// can paste a code block straight from chat without trimming.
func ParseFallbackResult(data []byte) (*FallbackResultDoc, error) {
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	if s == "" {
		return nil, errors.New("apply-fallback: empty input")
	}
	var doc FallbackResultDoc
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		return nil, fmt.Errorf("apply-fallback: parse YAML: %w", err)
	}
	if len(doc.Files) == 0 {
		return nil, errors.New("apply-fallback: result document contains no files")
	}
	return &doc, nil
}

// ToModelResult converts one FallbackResultFile into the canonical
// ModelResult shape consumed by MergeModelResult. Same translation rules as
// parseFallbackPayload (LLM fallback): SPO → schema.Relationship with
// Target=object, Type=predicate; entity types lowercased into Role.
func (f FallbackResultFile) ToModelResult() *ModelResult {
	res := &ModelResult{
		EntityType: strings.ToLower(strings.TrimSpace(f.EntityType)),
		Summary:    strings.TrimSpace(f.Summary),
	}
	for _, e := range f.Entities {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		res.Entities = append(res.Entities, schema.Entity{
			Name: name,
			Role: strings.TrimSpace(e.Type),
		})
	}
	known := make(map[string]string, len(res.Entities))
	for _, e := range res.Entities {
		known[strings.ToLower(e.Name)] = e.Name
	}
	for _, r := range f.Relationships {
		obj := strings.TrimSpace(r.Object)
		pred := strings.TrimSpace(r.Predicate)
		if obj == "" || pred == "" {
			continue
		}
		if canonical, ok := known[strings.ToLower(obj)]; ok {
			obj = canonical
		}
		res.Relationships = append(res.Relationships, schema.Relationship{
			Target:   obj,
			Type:     pred,
			Strength: nuExtractStrength,
		})
	}
	return res
}

// ApplyFallbackOutcome is the per-file result of an --apply-fallback merge.
type ApplyFallbackOutcome struct {
	Path    string
	Applied bool
	Err     error
}

// ApplyFallbackResult walks doc.Files and merges each file's recovered
// metadata into its target file's frontmatter via MergeModelResult — the
// same code path NuExtract and the LLM fallback use, so post-merge
// frontmatter is byte-comparable to a successful primary run.
//
// Skips silently (with a per-file Err) when the target path doesn't exist
// or can't be parsed; partial success is the norm because the user may have
// re-organized files between the original failure and the agent's
// re-extraction.
func ApplyFallbackResult(doc *FallbackResultDoc, opts MergeOptions) []ApplyFallbackOutcome {
	if doc == nil {
		return nil
	}
	out := make([]ApplyFallbackOutcome, 0, len(doc.Files))
	for _, f := range doc.Files {
		oc := ApplyFallbackOutcome{Path: f.Path}
		if strings.TrimSpace(f.Path) == "" {
			oc.Err = errors.New("entry has empty path")
			out = append(out, oc)
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			oc.Err = fmt.Errorf("read target: %w", err)
			out = append(out, oc)
			continue
		}
		page, err := markdown.ParseBytesPermissive(data)
		if err != nil {
			oc.Err = fmt.Errorf("parse target: %w", err)
			out = append(out, oc)
			continue
		}
		merged := MergeModelResult(page.Frontmatter, f.ToModelResult(), opts)
		if f.Summary != "" {
			merged = MergeSummary(merged, f.Summary, opts)
		}
		content, err := markdown.ReplaceFrontmatter(&merged, data)
		if err != nil {
			oc.Err = fmt.Errorf("render frontmatter: %w", err)
			out = append(out, oc)
			continue
		}
		if err := markdown.WriteFrontmatterFile(f.Path, content); err != nil {
			oc.Err = fmt.Errorf("write target: %w", err)
			out = append(out, oc)
			continue
		}
		oc.Applied = true
		out = append(out, oc)
	}
	return out
}

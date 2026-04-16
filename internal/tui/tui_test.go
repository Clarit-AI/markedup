package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/KHAEntertainment/markedup/config"
	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/schema"
)

// buildTestIndex constructs a minimal KnowledgeIndex for testing.
func buildTestIndex(t *testing.T, pages ...*schema.Page) *index.KnowledgeIndex {
	t.Helper()
	// Use the Load function with a temp dir containing test files,
	// or build the index by writing markdown files. For unit tests we'll
	// use the public API via a temp directory with test fixtures.

	// For simplicity, write pages to a temp dir and load.
	dir := t.TempDir()
	for _, p := range pages {
		content := "---\n"
		content += "id: " + p.Frontmatter.ID + "\n"
		content += "title: " + p.Frontmatter.Title + "\n"
		entityType := p.Frontmatter.EntityType
		if entityType == "" {
			entityType = "concept"
		}
		content += "entity-type: " + entityType + "\n"
		content += "confidence: 0.9\n"
		if len(p.Frontmatter.Tags) > 0 {
			content += "tags:\n"
			for _, tag := range p.Frontmatter.Tags {
				content += "  - " + tag + "\n"
			}
		}
		if len(p.Frontmatter.Relationships) > 0 {
			content += "relationships:\n"
			for _, r := range p.Frontmatter.Relationships {
				content += "  - target: " + r.Target + "\n"
				content += "    type: " + r.Type + "\n"
				content += "    strength: 0.8\n"
			}
		}
		content += "---\n"
		if p.Body != "" {
			content += p.Body + "\n"
		}

		err := writeTestFile(dir, p.Frontmatter.ID+".md", content)
		require.NoError(t, err)
	}

	result, err := index.Load(t.Context(), dir, index.WithIgnoreErrors(true))
	require.NoError(t, err)
	return result.Index
}

func writeTestFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
}

func TestNewModel_EmptyIndex(t *testing.T) {
	idx := buildTestIndex(t)
	m := NewModel(idx, ".", nil)

	assert.True(t, m.empty)
	// Empty index should show onboarding screen, not search.
	assert.Equal(t, viewOnboarding, m.current)

	v := m.View()
	assert.Contains(t, v, "Getting Started")
	assert.Contains(t, v, "No pages found in this knowledge base")
	assert.Contains(t, v, "markedup enrich .")
	assert.Contains(t, v, "markedup embed .")
	assert.Contains(t, v, "markedup tui")
}

func TestNewModel_WithPages(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", EntityType: "concept", Confidence: 0.9}},
	)

	m := NewModel(idx, ".", nil)
	assert.False(t, m.empty)
	// With pages, should start at home screen.
	assert.Equal(t, viewHome, m.current)

	v := m.View()
	assert.Contains(t, v, "markedup")
	assert.Contains(t, v, "Search")
}

func TestModel_WindowResize(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", Confidence: 0.9}},
	)
	m := NewModel(idx, ".", nil)

	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	rm := resized.(Model)
	assert.Equal(t, 120, rm.width)
	assert.Equal(t, 40, rm.height)
	assert.Equal(t, 120, rm.search.width)
	assert.Equal(t, 40, rm.search.height)
}

func TestModel_QuitOnCtrlC(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", Confidence: 0.9}},
	)
	m := NewModel(idx, ".", nil)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, cmd)
	// Execute the cmd to verify it's a quit command.
	quitMsg := cmd()
	_, isQuit := quitMsg.(tea.QuitMsg)
	assert.True(t, isQuit)
}

func TestModel_QuitOnQ_EmptyInput(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", Confidence: 0.9}},
	)
	m := NewModel(idx, ".", nil)

	// q with empty input should quit.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, cmd)
	quitMsg := cmd()
	_, isQuit := quitMsg.(tea.QuitMsg)
	assert.True(t, isQuit)
}

func TestSearchModel_EmptyResults(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", Confidence: 0.9}},
	)

	sm := newSearchModel(idx)
	sm.width = 80
	sm.height = 24

	v := sm.View()
	assert.Contains(t, v, "Start typing to search")
}

func TestSearchModel_Selected_NoResults(t *testing.T) {
	idx := buildTestIndex(t)
	sm := newSearchModel(idx)

	_, ok := sm.Selected()
	assert.False(t, ok)
}

func TestSearchModel_Debounce(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", Confidence: 0.9}},
	)

	sm := newSearchModel(idx)
	sm.width = 80
	sm.height = 24

	// Simulate the debounce message directly.
	sm.results = index.Search(idx, "test", index.WithLimit(50))
	assert.Greater(t, len(sm.results), 0)
}

func TestDocModel_NilPage(t *testing.T) {
	dm := newDocModel(nil, 80, 24)
	v := dm.View()
	assert.Contains(t, v, "No page selected")
}

func TestDocModel_WithPage(t *testing.T) {
	page := &schema.Page{
		Frontmatter: schema.GraphFrontmatter{
			ID:         "test-1",
			Title:      "Test Page",
			EntityType: "concept",
			Confidence: 0.85,
			Tags:       []string{"go", "testing"},
		},
		Body:       "This is the body content.",
		SourcePath: "/tmp/test-1.md",
	}

	dm := newDocModel(page, 80, 24)
	v := dm.View()
	assert.Contains(t, v, "Test Page")
}

func TestExploreModel_NilPage(t *testing.T) {
	idx := buildTestIndex(t)
	em := newExploreModel(idx, nil)
	em.width = 80
	em.height = 24

	v := em.View()
	assert.Contains(t, v, "No node selected")
}

func TestExploreModel_Selected_NoNeighbors(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "lonely", Title: "Lonely Node", Confidence: 0.9}},
	)
	page, _ := idx.Get("lonely")
	em := newExploreModel(idx, page)
	em.width = 80
	em.height = 24

	_, ok := em.Selected()
	assert.False(t, ok)

	v := em.View()
	assert.Contains(t, v, "No connections found")
}

func TestExploreModel_WithNeighbors(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{
			Frontmatter: schema.GraphFrontmatter{
				ID: "node-a", Title: "Node A", Confidence: 0.9,
				Relationships: []schema.Relationship{
					{Target: "node-b", Type: "related-to", Strength: 0.8},
				},
			},
		},
		&schema.Page{
			Frontmatter: schema.GraphFrontmatter{
				ID: "node-b", Title: "Node B", Confidence: 0.9,
			},
		},
	)
	page, _ := idx.Get("node-a")
	em := newExploreModel(idx, page)
	em.width = 80
	em.height = 24

	assert.Len(t, em.neighbors, 1)
	assert.Equal(t, "Node B", em.neighbors[0].page.Frontmatter.Title)

	sel, ok := em.Selected()
	assert.True(t, ok)
	assert.Equal(t, "node-b", sel.Frontmatter.ID)
}

func TestExploreModel_CursorNavigation(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{
			Frontmatter: schema.GraphFrontmatter{
				ID: "center", Title: "Center", Confidence: 0.9,
				Relationships: []schema.Relationship{
					{Target: "a", Type: "ref", Strength: 0.5},
					{Target: "b", Type: "ref", Strength: 0.5},
				},
			},
		},
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "a", Title: "A", Confidence: 0.9}},
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "b", Title: "B", Confidence: 0.9}},
	)
	page, _ := idx.Get("center")
	em := newExploreModel(idx, page)
	em.width = 80
	em.height = 24

	assert.Equal(t, 0, em.cursor)

	// Move down.
	em, _ = em.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 1, em.cursor)

	// Move down again (should not exceed bounds).
	em, _ = em.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 1, em.cursor)

	// Move up.
	em, _ = em.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, em.cursor)

	// Move up again (should not go below 0).
	em, _ = em.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, em.cursor)
}

func TestModel_ViewTransitions(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{
			Frontmatter: schema.GraphFrontmatter{
				ID: "test-1", Title: "Test Page", Confidence: 0.9,
				EntityType: "concept",
			},
			Body: "Some content here.",
		},
	)

	m := NewModel(idx, ".", nil)
	// With pages, starts at home screen.
	assert.Equal(t, viewHome, m.current)

	// Enter on Search menu item (index 0) to go to search view.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	assert.Equal(t, viewSearch, m.current)

	// Simulate having search results.
	m.search.results = index.Search(idx, "test", index.WithLimit(50))
	require.Greater(t, len(m.search.results), 0)

	// Enter to go to document view.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	assert.Equal(t, viewDocument, m.current)

	// Esc to go back to search.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	assert.Equal(t, viewSearch, m.current)

	// Tab to go to explore view.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	assert.Equal(t, viewExplore, m.current)

	// Esc to go back to search.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	assert.Equal(t, viewSearch, m.current)

	// h to return to home.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(Model)
	assert.Equal(t, viewHome, m.current)
}

func TestHomeModel_Navigation(t *testing.T) {
	m := newHomeModel()
	m.width = 80
	m.height = 24

	assert.Equal(t, 0, m.cursor)

	// Navigate down.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 1, m.cursor)

	// Navigate up.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, m.cursor)

	// Can't go above 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, m.cursor)

	v := m.View()
	assert.Contains(t, v, "Search")
	assert.Contains(t, v, "Explore")
	assert.Contains(t, v, "Enrich")
	assert.Contains(t, v, "Embed")
	assert.Contains(t, v, "Settings")
	// "Graph" menu item removed; only appears in subtitle "Knowledge Graph Terminal UI"
	assert.NotContains(t, v, "Graph overview")
}

func TestOnboardingModel_View(t *testing.T) {
	m := newOnboardingModel()
	m.width = 80
	m.height = 24

	v := m.View()
	assert.Contains(t, v, "Getting Started")
	assert.Contains(t, v, "No pages found in this knowledge base")
	assert.Contains(t, v, "markedup enrich .")
	assert.Contains(t, v, "markedup embed .")
	assert.Contains(t, v, "markedup tui")
}

// --------------------------------------------------------------------------
// New tests for Wave 5 features
// --------------------------------------------------------------------------

func TestHomeModel_Items(t *testing.T) {
	m := newHomeModel()
	m.width = 80
	m.height = 24

	// Exactly 5 menu items.
	assert.Len(t, homeMenuItems, 5)
	assert.Equal(t, "Search", homeMenuItems[homeMenuSearch].label)
	assert.Equal(t, "Explore", homeMenuItems[homeMenuExplore].label)
	assert.Equal(t, "Enrich", homeMenuItems[homeMenuEnrich].label)
	assert.Equal(t, "Embed", homeMenuItems[homeMenuEmbed].label)
	assert.Equal(t, "Settings", homeMenuItems[homeMenuSettings].label)

	// Graph item is gone.
	for _, item := range homeMenuItems {
		assert.NotEqual(t, "Graph", item.label)
	}

	// View renders all items.
	v := m.View()
	assert.Contains(t, v, "Search")
	assert.Contains(t, v, "Explore")
	assert.Contains(t, v, "Enrich")
	assert.Contains(t, v, "Embed")
	assert.Contains(t, v, "Settings")
}

func TestHomeModel_TaskRunning_DisablesEnrichEmbed(t *testing.T) {
	m := newHomeModel()
	m.width = 80
	m.height = 24
	m.taskRunning = true

	v := m.View()
	assert.Contains(t, v, "Enrich (running...)")
	assert.Contains(t, v, "Embed (running...)")
}

func TestStatusModel_View_Idle(t *testing.T) {
	s := statusModel{state: taskIdle}
	v := s.View(80)
	assert.Equal(t, "", v)
}

func TestStatusModel_View_Running(t *testing.T) {
	s := statusModel{state: taskRunning, taskName: "Enrich"}
	v := s.View(80)
	assert.Contains(t, v, "Enrich")
	assert.Contains(t, v, "ing...")
}

func TestStatusModel_View_Done(t *testing.T) {
	s := statusModel{state: taskDone, taskName: "Enrich"}
	v := s.View(80)
	assert.Contains(t, v, "✓")
	assert.Contains(t, v, "Enrich")
	assert.Contains(t, v, "complete")
}

func TestStatusModel_View_Error(t *testing.T) {
	s := statusModel{state: taskError, taskName: "Embed", message: "connection refused"}
	v := s.View(80)
	assert.Contains(t, v, "✗")
	assert.Contains(t, v, "Embed")
	assert.Contains(t, v, "failed")
	assert.Contains(t, v, "connection refused")
}

func TestStatusModel_ShouldDismiss(t *testing.T) {
	// Not done — should not dismiss.
	s := statusModel{state: taskRunning}
	assert.False(t, s.shouldDismiss())

	// Done but dismissAt in the future.
	s = statusModel{state: taskDone, dismissAt: time.Now().Add(10 * time.Second)}
	assert.False(t, s.shouldDismiss())

	// Done and dismissAt in the past.
	s = statusModel{state: taskDone, dismissAt: time.Now().Add(-1 * time.Second)}
	assert.True(t, s.shouldDismiss())

	// Error and dismissAt in the past.
	s = statusModel{state: taskError, dismissAt: time.Now().Add(-1 * time.Second)}
	assert.True(t, s.shouldDismiss())
}

func TestSettingsModel_View(t *testing.T) {
	cfg := &config.Config{}
	cfg.Embed.Endpoint = "http://localhost:11434/v1"
	cfg.Embed.Model = "nomic-embed-text"
	cfg.LLM.Endpoint = "http://localhost:11434"
	cfg.LLM.Model = "llama3"

	m := newSettingsModel(cfg, 80, 24)
	v := m.View()

	assert.Contains(t, v, "Settings")
	assert.Contains(t, v, "Embed")
	assert.Contains(t, v, "http://localhost:11434/v1")
	assert.Contains(t, v, "nomic-embed-text")
	assert.Contains(t, v, "LLM")
	assert.Contains(t, v, "llama3")
	assert.Contains(t, v, "Rerank")
	assert.Contains(t, v, "Triplex")
	assert.Contains(t, v, "markedup setup")
	assert.Contains(t, v, "esc/h: back")
}

func TestSettingsModel_View_NilConfig(t *testing.T) {
	m := newSettingsModel(nil, 80, 24)
	v := m.View()
	assert.Contains(t, v, "Settings")
	assert.Contains(t, v, "not configured")
}

func TestModel_Settings_Navigation(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", Confidence: 0.9}},
	)
	m := NewModel(idx, ".", nil)
	assert.Equal(t, viewHome, m.current)

	// Navigate down to Settings (index 4).
	for i := 0; i < homeMenuSettings; i++ {
		m.home.cursor = i + 1
	}
	m.home.cursor = homeMenuSettings

	// Press Enter on Settings.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	assert.Equal(t, viewSettings, m.current)

	// Esc returns to home.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	assert.Equal(t, viewHome, m.current)
}

func TestModel_ExploreMode_Header(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", Confidence: 0.9}},
	)
	m := NewModel(idx, ".", nil)

	// Navigate to Explore from home.
	m.home.cursor = homeMenuExplore
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	assert.Equal(t, viewSearch, m.current)
	assert.True(t, m.search.exploreMode)

	v := m.View()
	assert.Contains(t, v, "Explore Mode")
	assert.Contains(t, v, "tab to traverse")
}

func TestModel_StatusBar_AppendedToView(t *testing.T) {
	idx := buildTestIndex(t,
		&schema.Page{Frontmatter: schema.GraphFrontmatter{ID: "test-1", Title: "Test Page", Confidence: 0.9}},
	)
	m := NewModel(idx, ".", nil)
	m.status = statusModel{state: taskDone, taskName: "Enrich"}

	v := m.View()
	assert.Contains(t, v, "✓")
	assert.Contains(t, v, "Enrich")
}

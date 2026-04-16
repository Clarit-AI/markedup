package tui

import (
	"fmt"
	"time"
)

// taskState represents the current state of a background task.
type taskState int

const (
	taskIdle    taskState = iota
	taskRunning           // subprocess is running
	taskDone              // completed successfully
	taskError             // completed with error
)

// statusModel tracks the state of a background task and renders a status bar.
type statusModel struct {
	state      taskState
	taskName   string    // "Enrich" or "Embed"
	message    string    // completion/error message
	dismissAt  time.Time // auto-dismiss after 5s on done/error
	spinnerPos int       // spinner animation position
}

// statusTickMsg is sent to advance the spinner animation.
type statusTickMsg struct{}

// taskDoneMsg is sent when a background subprocess completes.
type taskDoneMsg struct {
	taskName string
	err      error
	output   string
}

var spinnerFrames = []string{"⠋", "⠙", "⠸", "⠴", "⠦", "⠇"}

// View renders the status bar line for the given terminal width.
// - idle: empty string (no bar shown)
// - running: spinner + task name
// - done: success message
// - error: error message
func (m statusModel) View(width int) string {
	switch m.state {
	case taskIdle:
		return ""
	case taskRunning:
		frame := spinnerFrames[m.spinnerPos%len(spinnerFrames)]
		msg := fmt.Sprintf("%s %sing...", frame, m.taskName)
		return helpStyle.Render(msg)
	case taskDone:
		if m.message != "" {
			return selectedStyle.Render(fmt.Sprintf("✓ %s: %s", m.taskName, m.message))
		}
		return selectedStyle.Render(fmt.Sprintf("✓ %s complete", m.taskName))
	case taskError:
		msg := fmt.Sprintf("✗ %s failed: %s", m.taskName, m.message)
		return warningStyle.Render(msg)
	}
	return ""
}

// tick advances the spinner frame and returns a new statusModel.
func (m statusModel) tick() statusModel {
	m.spinnerPos++
	return m
}

// shouldDismiss returns true when the done/error state has expired.
func (m statusModel) shouldDismiss() bool {
	if m.state != taskDone && m.state != taskError {
		return false
	}
	return !m.dismissAt.IsZero() && time.Now().After(m.dismissAt)
}

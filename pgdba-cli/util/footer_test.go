package util

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestFilterFooters_DontRepeatFilterHint guards against passing "/ filter" to
// FilterFooter, which already starts the footer with it.
func TestFilterFooters_DontRepeatFilterHint(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`FilterFooter\([^)]*"[^"]*/ filter[^"]*"`)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if match := call.Find(source); match != nil {
			t.Errorf("%s passes \"/ filter\" to FilterFooter, which already adds it: %s", file, match)
		}
	}
}

// TestDashboard_ShortcutRowsFitEightyColumns keeps every footer row of the dashboard
// within 80 columns: a wider row wraps and pushes the header off a standard terminal.
func TestDashboard_ShortcutRowsFitEightyColumns(t *testing.T) {
	view := DashboardModel{width: 80, height: 40}.View()
	lines := strings.Split(view, "\n")
	dividerAt := -1
	for i, line := range lines {
		if strings.Contains(line, "────────") {
			dividerAt = i
		}
	}
	if dividerAt < 0 || dividerAt == len(lines)-1 {
		t.Fatal("shortcut footer not found")
	}
	for _, row := range lines[dividerAt+1:] {
		if width := lipgloss.Width(row); width > 80 {
			t.Errorf("shortcut row is %d columns wide: %q", width, row)
		}
	}
}

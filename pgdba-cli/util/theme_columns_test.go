package util

import (
	"testing"

	"github.com/charmbracelet/bubbles/table"
)

func TestFitColumnsToContent(t *testing.T) {
	cols := []table.Column{{Title: "Name", Width: 5}, {Title: "Other", Width: 5}, {Title: "N", Width: 6}}
	rows := []table.Row{
		{"a_really_long_index_name_idx", "short", "1"},
		{"x", "medium_value", "2"},
	}
	totalWidth := func(cols []table.Column) int {
		total := 0
		for _, col := range cols {
			total += col.Width + 2
		}
		return total
	}

	wide := FitColumnsToContent(cols, rows, []int{0, 1}, 100)
	if wide[0].Width != 28 || wide[2].Width != 6 || totalWidth(wide) != 100 {
		t.Errorf("wide terminal: got %+v (total %d)", wide, totalWidth(wide))
	}

	// 30 cols of room for 28+12 of content: the widest column gives way first.
	narrow := FitColumnsToContent(cols, rows, []int{0, 1}, 6+2*3+30)
	if narrow[1].Width != 12 || narrow[0].Width != 18 || totalWidth(narrow) != 42 {
		t.Errorf("narrow terminal: got %+v (total %d)", narrow, totalWidth(narrow))
	}

	tiny := FitColumnsToContent(cols, rows, []int{0, 1}, 20)
	if tiny[0].Width != 8 || tiny[1].Width != 8 {
		t.Errorf("columns must not shrink below the minimum: got %+v", tiny)
	}
}

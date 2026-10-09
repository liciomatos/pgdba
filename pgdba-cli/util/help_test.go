package util

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// screenModels must list every model type in this package that has a View method.
// TestEveryScreenHasHelp finds them by parsing the source, so a new screen without an
// entry here (and a help topic) fails the build's test step.
var screenModels = map[string]tea.Model{
	"DashboardModel":           DashboardModel{},
	"SlowQueriesModel":         SlowQueriesModel{},
	"LongRunningQueriesModel":  LongRunningQueriesModel{},
	"ReplicationSlotsModel":    ReplicationSlotsModel{},
	"RecordLocksModel":         RecordLocksModel{},
	"ConnectionsModel":         ConnectionsModel{},
	"AutovacuumModel":          AutovacuumModel{},
	"AutovacuumDetailModel":    AutovacuumDetailModel{},
	"IndexUsageModel":          IndexUsageModel{},
	"IndexDetailModel":         IndexDetailModel{},
	"CacheHitModel":            CacheHitModel{},
	"UsersModel":               UsersModel{},
	"RolesModel":               RolesModel{},
	"PgConfigModel":            PgConfigModel{},
	"SchemaBrowserModel":       SchemaBrowserModel{},
	"ExtensionsModel":          ExtensionsModel{},
	"DatabaseSelectorModel":    DatabaseSelectorModel{},
	"QueryLoadModel":           QueryLoadModel{},
	"WaitEventsModel":          WaitEventsModel{},
	"FreezeModel":              FreezeModel{},
	"DatabaseSizeModel":        DatabaseSizeModel{},
	"TempFilesModel":           TempFilesModel{},
	"MemoryStatsModel":         MemoryStatsModel{},
	"PubSubModel":              PubSubModel{},
	"PubTableDetailModel":      PubTableDetailModel{},
	"SubTableDetailModel":      SubTableDetailModel{},
	"ToastTablesModel":         ToastTablesModel{},
	"ReplicationConfigModel":   ReplicationConfigModel{},
	"ReplicationStandbysModel": ReplicationStandbysModel{},
	"ReplicaIdentityModel":     ReplicaIdentityModel{},
	"XminHorizonModel":         XminHorizonModel{},
	"VacuumProgressModel":      VacuumProgressModel{},
	"VersionModel":             VersionModel{},
}

// modelsWithoutHelp are View types that are not screens of their own.
var modelsWithoutHelp = map[string]bool{"ErrorModel": true, "HelpModel": true}

func viewTypesInPackage(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "View" {
				continue
			}
			if ident, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				types = append(types, ident.Name)
			}
		}
	}
	sort.Strings(types)
	return types
}

func TestEveryScreenHasHelp(t *testing.T) {
	usedTopics := map[string]bool{}
	for _, typeName := range viewTypesInPackage(t) {
		if modelsWithoutHelp[typeName] {
			continue
		}
		model, listed := screenModels[typeName]
		if !listed {
			t.Errorf("%s has a View method but isn't in screenModels — add it there and give it a HelpTopic", typeName)
			continue
		}
		provider, ok := model.(HelpTopicProvider)
		if !ok {
			t.Errorf("%s doesn't implement HelpTopic() — add it in help.go", typeName)
			continue
		}
		topic := provider.HelpTopic()
		usedTopics[topic] = true
		help, ok := screenHelp[topic]
		if !ok {
			t.Errorf("%s uses help topic %q, which has no entry in screenHelp", typeName, topic)
			continue
		}
		if help.Title == "" || help.Purpose == "" || help.Source == "" {
			t.Errorf("help topic %q needs at least a Title, Purpose and Source", topic)
		}
	}
	for topic := range screenHelp {
		if !usedTopics[topic] {
			t.Errorf("help topic %q isn't used by any screen", topic)
		}
	}
}

func TestRenderScreenHelp_FitsWidth(t *testing.T) {
	for topic := range screenHelp {
		for _, width := range []int{60, 100, 160} {
			rendered := RenderScreenHelp(topic, width)
			for _, section := range []string{"PURPOSE", "KEYS", "DATA SOURCE"} {
				if !strings.Contains(rendered, section) {
					t.Errorf("%s: missing %s section", topic, section)
				}
			}
			for _, line := range strings.Split(rendered, "\n") {
				if lipgloss.Width(line) > width {
					t.Errorf("%s at width %d: line of %d columns: %q", topic, width, lipgloss.Width(line), stripAnsi(line))
					break
				}
			}
		}
	}
}

type helpTestScreen struct{ name string }

func (s helpTestScreen) Init() tea.Cmd                       { return nil }
func (s helpTestScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }
func (s helpTestScreen) View() string                        { return s.name }

func TestHelpModel_OpensScrollsAndReturns(t *testing.T) {
	screen := helpTestScreen{name: "index usage screen"}
	var help tea.Model = NewHelpModel("index_usage", screen)
	help, _ = help.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	view := stripAnsi(help.View())
	if !strings.Contains(view, "Help — Index Usage") || !strings.Contains(view, "PURPOSE") {
		t.Fatalf("help page doesn't show the topic:\n%s", view)
	}
	if lines := strings.Count(view, "\n") + 1; lines > 20 {
		t.Fatalf("help page has %d lines on a 20-line terminal", lines)
	}
	if !help.(HelpModel).ConsumesKey("1") {
		t.Fatal("help must claim global shortcuts so they don't navigate away")
	}

	scrolled, _ := help.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if scrolled.(HelpModel).viewport.YOffset == 0 {
		t.Fatal("pgdown should scroll a help page longer than the window")
	}

	for _, key := range []tea.KeyMsg{keyMsg("q"), keyMsg("?"), {Type: tea.KeyEsc}} {
		back, _ := scrolled.Update(key)
		if back != tea.Model(screen) {
			t.Fatalf("%q should return to the original screen, got %T", key.String(), back)
		}
	}
}

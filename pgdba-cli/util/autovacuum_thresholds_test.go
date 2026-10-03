package util

import (
	"math"
	"testing"
)

// defaultGlobals mirrors a stock postgresql.conf for the GUCs the threshold
// computation reads (PG18 values; older versions simply lack some names).
func defaultGlobals() map[string]string {
	return map[string]string{
		"autovacuum":                            "on",
		"track_counts":                          "on",
		"autovacuum_vacuum_threshold":           "50",
		"autovacuum_vacuum_scale_factor":        "0.2",
		"autovacuum_vacuum_max_threshold":       "100000000",
		"autovacuum_vacuum_insert_threshold":    "1000",
		"autovacuum_vacuum_insert_scale_factor": "0.2",
		"autovacuum_analyze_threshold":          "50",
		"autovacuum_analyze_scale_factor":       "0.1",
		"autovacuum_freeze_max_age":             "200000000",
		"vacuum_freeze_min_age":                 "50000000",
		"vacuum_freeze_table_age":               "150000000",
		"vacuum_failsafe_age":                   "1600000000",
		"autovacuum_multixact_freeze_max_age":   "400000000",
		"vacuum_multixact_freeze_min_age":       "5000000",
		"vacuum_multixact_freeze_table_age":     "150000000",
		"vacuum_multixact_failsafe_age":         "1600000000",
	}
}

func assertClose(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func TestComputeAutovacuumThresholds_GlobalDefaults(t *testing.T) {
	result := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion:       16,
		TableOptions:       map[string]string{},
		GlobalSettings:     defaultGlobals(),
		Reltuples:          10000,
		DeadTuples:         2051,
		InsertsSinceVacuum: 100,
		ModsSinceAnalyze:   1050,
		XIDAge:             160_000_000,
		MXIDAge:            10,
	})
	assertClose(t, "vacuum threshold", result.DeadTuples.Threshold, 50+0.2*10000)
	if !result.DeadTuples.Due || result.DeadTuples.Base.Source != SourceGlobal {
		t.Errorf("dead tuples 2051 > 2050 should be due from global settings: %+v", result.DeadTuples)
	}
	if result.DeadTuples.MaxThreshold != nil {
		t.Error("PG16 has no autovacuum_vacuum_max_threshold")
	}
	assertClose(t, "insert threshold", result.Inserts.Threshold, 1000+0.2*10000)
	if result.Inserts.UnfrozenFraction != nil {
		t.Error("PG16 insert threshold must not use relallfrozen")
	}
	assertClose(t, "analyze threshold", result.Analyze.Threshold, 50+0.1*10000)
	if result.Analyze.Due {
		t.Error("1050 modifications is not > 1050")
	}
	if result.XIDWraparound.Due || !result.XIDAggressive.Due {
		t.Errorf("XID age 160M: want aggressive (>150M) but not wraparound (>200M): %+v / %+v",
			result.XIDWraparound, result.XIDAggressive)
	}
	assertClose(t, "xid failsafe", result.XIDFailsafe.Limit.Value, 1_600_000_000)
	if !result.AutovacuumDue() {
		t.Error("table should be due for autovacuum")
	}
}

func TestComputeAutovacuumThresholds_TableOverridesWinOverGlobals(t *testing.T) {
	result := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion: 16,
		TableOptions: map[string]string{
			"autovacuum_vacuum_scale_factor":     "0.01",
			"autovacuum_analyze_threshold":       "500",
			"autovacuum_freeze_max_age":          "100000000",
			"autovacuum_freeze_table_age":        "90000000",
			"autovacuum_vacuum_insert_threshold": "-1",
		},
		GlobalSettings: defaultGlobals(),
		Reltuples:      1_000_000,
	})
	if result.DeadTuples.ScaleFactor.Source != SourceTable || result.DeadTuples.Base.Source != SourceGlobal {
		t.Errorf("unexpected sources: %+v", result.DeadTuples)
	}
	assertClose(t, "vacuum threshold", result.DeadTuples.Threshold, 50+0.01*1_000_000)
	assertClose(t, "analyze threshold", result.Analyze.Threshold, 500+0.1*1_000_000)
	if result.Inserts != nil {
		t.Error("insert threshold -1 at table level must disable insert-triggered vacuum")
	}
	if result.XIDWraparound.Limit.Value != 100_000_000 || result.XIDWraparound.Limit.Source != SourceTable {
		t.Errorf("table freeze_max_age lower than global should win: %+v", result.XIDWraparound.Limit)
	}
	if result.XIDAggressive.Limit.Value != 90_000_000 || result.XIDAggressive.Limit.Source != SourceTable {
		t.Errorf("table freeze_table_age should be used as-is: %+v", result.XIDAggressive.Limit)
	}
}

func TestComputeAutovacuumThresholds_FreezeMaxAgeReloptionCannotRaiseGlobal(t *testing.T) {
	result := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion:   16,
		TableOptions:   map[string]string{"autovacuum_freeze_max_age": "1000000000"},
		GlobalSettings: defaultGlobals(),
	})
	if result.XIDWraparound.Limit.Value != 200_000_000 || result.XIDWraparound.Limit.Source != SourceGlobal {
		t.Errorf("reloption above global must be ignored: %+v", result.XIDWraparound.Limit)
	}
}

func TestComputeAutovacuumThresholds_FreezeAgesClampedToFreezeMaxAge(t *testing.T) {
	globals := defaultGlobals()
	globals["vacuum_freeze_table_age"] = "2000000000"
	globals["vacuum_freeze_min_age"] = "1000000000"
	result := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion: 16, TableOptions: map[string]string{}, GlobalSettings: globals,
	})
	if result.XIDAggressive.Limit.Value != 190_000_000 || result.XIDAggressive.Limit.Source != SourceAdjusted {
		t.Errorf("freeze_table_age should be capped at 95%% of 200M: %+v", result.XIDAggressive.Limit)
	}
	if result.XIDFreezeMinAge.Value != 100_000_000 || result.XIDFreezeMinAge.Source != SourceAdjusted {
		t.Errorf("freeze_min_age should be capped at 50%% of 200M: %+v", result.XIDFreezeMinAge)
	}
}

func TestComputeAutovacuumThresholds_FailsafeRaisedAndAbsentOnPG13(t *testing.T) {
	globals := defaultGlobals()
	globals["vacuum_failsafe_age"] = "100000000"
	result := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion: 14, TableOptions: map[string]string{}, GlobalSettings: globals,
	})
	if result.XIDFailsafe.Limit.Value != 210_000_000 || result.XIDFailsafe.Limit.Source != SourceAdjusted {
		t.Errorf("failsafe should be raised to 105%% of 200M: %+v", result.XIDFailsafe.Limit)
	}
	pg13 := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion: 13, TableOptions: map[string]string{}, GlobalSettings: globals,
	})
	if pg13.XIDFailsafe != nil || pg13.MXIDFailsafe != nil {
		t.Error("PG13 has no vacuum failsafe")
	}
}

func TestComputeAutovacuumThresholds_PG18MaxThresholdAndUnfrozenFraction(t *testing.T) {
	relallfrozen := int64(750)
	result := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion:   18,
		TableOptions:   map[string]string{},
		GlobalSettings: defaultGlobals(),
		Reltuples:      1_000_000_000,
		Relpages:       1000,
		Relallfrozen:   &relallfrozen,
	})
	if result.DeadTuples.MaxThreshold == nil {
		t.Fatal("PG18 should report autovacuum_vacuum_max_threshold")
	}
	assertClose(t, "capped vacuum threshold", result.DeadTuples.Threshold, 100_000_000)
	assertClose(t, "unfrozen fraction", *result.Inserts.UnfrozenFraction, 0.25)
	assertClose(t, "insert threshold", result.Inserts.Threshold, 1000+0.2*1_000_000_000*0.25)

	result = computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion:   18,
		TableOptions:   map[string]string{"autovacuum_vacuum_max_threshold": "-1"},
		GlobalSettings: defaultGlobals(),
		Reltuples:      1_000_000_000,
	})
	if result.DeadTuples.MaxThreshold != nil {
		t.Error("max threshold -1 must disable the cap")
	}
	assertClose(t, "uncapped vacuum threshold", result.DeadTuples.Threshold, 50+0.2*1_000_000_000)
}

func TestComputeAutovacuumThresholds_NeverAnalyzedAndDisabled(t *testing.T) {
	result := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion:   16,
		TableOptions:   map[string]string{"autovacuum_enabled": "false"},
		GlobalSettings: defaultGlobals(),
		Reltuples:      -1,
		DeadTuples:     51,
		XIDAge:         10,
	})
	if !result.ReltuplesUnknown || result.Reltuples != 0 {
		t.Errorf("reltuples -1 should be treated as 0: %+v", result)
	}
	assertClose(t, "vacuum threshold", result.DeadTuples.Threshold, 50)
	if result.AutovacuumEnabled || result.AutovacuumEnabledSource != SourceTable {
		t.Errorf("autovacuum_enabled=false should disable at table level")
	}
	if result.AutovacuumDue() {
		t.Error("disabled table is only due for anti-wraparound vacuum")
	}
	result.XIDWraparound.Due = true
	if !result.AutovacuumDue() {
		t.Error("anti-wraparound vacuum runs even with autovacuum disabled")
	}
}

func TestParsePGBool(t *testing.T) {
	cases := map[string][2]bool{
		"on": {true, true}, "off": {false, true}, "true": {true, true}, "f": {false, true},
		"yes": {true, true}, "n": {false, true}, "1": {true, true}, "0": {false, true},
		"o": {false, false}, "": {false, false}, "maybe": {false, false},
	}
	for input, want := range cases {
		value, ok := parsePGBool(input)
		if value != want[0] || ok != want[1] {
			t.Errorf("parsePGBool(%q) = %v,%v want %v,%v", input, value, ok, want[0], want[1])
		}
	}
}

func TestParameterFormula_UsesSourcesAndResult(t *testing.T) {
	thresholds := computeAutovacuumThresholds(autovacuumThresholdInputs{
		MajorVersion:   16,
		TableOptions:   map[string]string{"autovacuum_vacuum_scale_factor": "0.01"},
		GlobalSettings: defaultGlobals(),
		Reltuples:      50000,
	})
	want := "Vacuum when dead tuples > 50 (global) + 0.01 (table) × 50,000 reltuples = 550"
	for _, name := range []string{"autovacuum_vacuum_threshold", "autovacuum_vacuum_scale_factor"} {
		if got := parameterFormula(name, &thresholds); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
	if got := parameterFormula("autovacuum_vacuum_cost_delay", &thresholds); got != "" {
		t.Errorf("cost delay has no formula, got %q", got)
	}
	threshold, current, status := thresholdCells("autovacuum_vacuum_threshold", &thresholds)
	if threshold != "550" || current != "0" || status != "ok" {
		t.Errorf("unexpected cells %q %q %q", threshold, current, status)
	}
}

func TestBuildParamRows_FormatsIntegerSettings(t *testing.T) {
	rows := buildParamRows([]AutovacuumParam{
		{Name: "autovacuum_freeze_max_age", TableValue: "100000000", GlobalValue: "200000000"},
		{Name: "autovacuum_vacuum_scale_factor", GlobalValue: "0.2"},
		{Name: "autovacuum_vacuum_cost_delay", GlobalValue: "2", Unit: "ms"},
		{Name: "autovacuum_vacuum_cost_limit", GlobalValue: "-1"},
		{Name: "autovacuum_enabled", TableValue: "false", GlobalValue: "on"},
	}, nil)
	want := [][2]string{
		{"100,000,000 *", "200,000,000"},
		{"(inherited)", "0.2"},
		{"(inherited)", "2 ms"},
		{"(inherited)", "-1"},
		{"false *", "on"},
	}
	for i, row := range rows {
		if row[1] != want[i][0] || row[2] != want[i][1] {
			t.Errorf("%s: got %q / %q, want %q / %q", row[0], row[1], row[2], want[i][0], want[i][1])
		}
	}
}

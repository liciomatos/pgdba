package util

import "testing"

func redundanciesOf(indexes []*IndexUsage, name string) []IndexRedundancy {
	for _, idx := range indexes {
		if idx.IndexName == name {
			return idx.RedundantWith
		}
	}
	return nil
}

func TestDetectRedundantIndexes(t *testing.T) {
	btree := func(name string, unique bool, columns ...int64) *IndexUsage {
		return &IndexUsage{IndexName: name, AccessMethod: "btree", IsUnique: unique, IsValid: true, ColumnNumbers: columns}
	}
	pkey := btree("t_pkey", true, 1)
	duplicateOfPK := btree("t_id_idx", false, 1)
	ab := btree("t_a_b_idx", false, 2, 3)
	a := btree("t_a_idx", false, 2)
	ba := btree("t_b_a_idx", false, 3, 2)
	uniqueA := btree("t_a_key", true, 2)
	partialA := btree("t_a_partial", false, 2)
	partialA.IsPartial = true
	hashA := &IndexUsage{IndexName: "t_a_hash", AccessMethod: "hash", IsValid: true, ColumnNumbers: []int64{2}}
	indexes := []*IndexUsage{pkey, duplicateOfPK, ab, a, ba, uniqueA, partialA, hashA}
	detectRedundantIndexes(indexes)

	if got := redundanciesOf(indexes, "t_id_idx"); len(got) != 1 || got[0] != (IndexRedundancy{"t_pkey", RedundancyDuplicate}) {
		t.Errorf("plain index on the PK column should duplicate the PK, got %v", got)
	}
	if got := redundanciesOf(indexes, "t_pkey"); len(got) != 0 {
		t.Errorf("a unique index is never redundant because of a non-unique one, got %v", got)
	}
	aRedundancies := redundanciesOf(indexes, "t_a_idx")
	if !containsRedundancy(aRedundancies, IndexRedundancy{"t_a_b_idx", RedundancyPrefix}) ||
		!containsRedundancy(aRedundancies, IndexRedundancy{"t_a_key", RedundancyDuplicate}) {
		t.Errorf("t_a_idx should be a prefix of t_a_b_idx and duplicate t_a_key, got %v", aRedundancies)
	}
	if containsRedundancy(aRedundancies, IndexRedundancy{"t_b_a_idx", RedundancyPrefix}) {
		t.Error("(a) is not a prefix of (b, a)")
	}
	if got := redundanciesOf(indexes, "t_a_b_idx"); !containsRedundancy(got, IndexRedundancy{"t_b_a_idx", RedundancySameColumns}) {
		t.Errorf("(a, b) and (b, a) have the same columns, got %v", got)
	}
	if got := redundanciesOf(indexes, "t_a_key"); len(got) != 0 {
		t.Errorf("a unique prefix enforces its own uniqueness and is never redundant, got %v", got)
	}
	if got := redundanciesOf(indexes, "t_a_partial"); len(got) != 0 {
		t.Errorf("partial indexes are not compared, got %v", got)
	}
	if got := redundanciesOf(indexes, "t_a_hash"); len(got) != 0 {
		t.Errorf("different access methods are not compared, got %v", got)
	}
}

func containsRedundancy(redundancies []IndexRedundancy, want IndexRedundancy) bool {
	for _, redundancy := range redundancies {
		if redundancy == want {
			return true
		}
	}
	return false
}

func TestIndexUsage_WasteScoreAndStatus(t *testing.T) {
	share := func(value float64) *float64 { return &value }
	big := IndexUsage{IsValid: true, IndexSizeBytes: 29 << 30, TableScanSharePct: share(7), IdxScan: 100}
	tinyUnused := IndexUsage{IsValid: true, IndexSizeBytes: 8192}
	if big.WasteScore() <= tinyUnused.WasteScore() {
		t.Error("a 29 GB index serving 7% of its table's scans must outrank a tiny unused one")
	}
	if IndexUsageStatus(tinyUnused) != StatusOK {
		t.Error("tiny unused indexes (empty tables) should not be flagged")
	}
	if IndexUsageStatus(IndexUsage{IsValid: true, IndexSizeBytes: 2 << 20}) != StatusWarning {
		t.Error("an unused index above 1 MB should be a warning")
	}
	if IndexUsageStatus(IndexUsage{IsValid: false}) != StatusCritical {
		t.Error("INVALID indexes are critical")
	}
}

func TestTableChurnStatusAndHint(t *testing.T) {
	pct := func(value float64) *float64 { return &value }
	packed := TableChurn{Updates: 50_000, HotPct: pct(12), Fillfactor: 100}
	if TableChurnStatus(packed) != StatusWarning || TableChurnHint(packed) == "" {
		t.Error("an update-heavy table with low HOT % should warn with a hint")
	}
	roomy := TableChurn{Updates: 50_000, HotPct: pct(12), Fillfactor: 80, FillfactorSet: true}
	if TableChurnHint(roomy) == TableChurnHint(packed) {
		t.Error("the hint should differ when fillfactor already leaves room")
	}
	if TableChurnStatus(TableChurn{Updates: 500, HotPct: pct(0), Fillfactor: 100}) != StatusOK {
		t.Error("few updates are not worth a warning")
	}
	if TableChurnStatus(TableChurn{Updates: 50_000, HotPct: pct(80), Fillfactor: 100}) != StatusOK {
		t.Error("mostly HOT updates are fine")
	}
}

func TestWALAndIOStatus(t *testing.T) {
	if WALStatsStatus(WALStats{Records: 1000, FPI: 400}) != StatusWarning {
		t.Error("40% full-page images should warn")
	}
	if WALStatsStatus(WALStats{Records: 100_000, FPI: 1000, BuffersFull: 200}) != StatusWarning {
		t.Error("wal_buffers full on 0.2% of records should warn")
	}
	if WALStatsStatus(WALStats{Records: 100_000, FPI: 1000, BuffersFull: 50}) != StatusOK {
		t.Error("healthy WAL should be ok")
	}
	if WALStatsStatus(WALStats{}) != StatusOK {
		t.Error("no records yet should be ok")
	}
	pct := 30.0
	if IOStatsStatus(IOStats{ClientBackendWritePct: &pct}) != StatusWarning {
		t.Error("client backends writing 30% should warn")
	}
	if IOStatsStatus(IOStats{}) != StatusOK {
		t.Error("no writes should be ok")
	}
}

func TestCacheHitStatus(t *testing.T) {
	pct := func(value float64) *float64 { return &value }
	cases := []struct {
		value         *float64
		instance, row Status
	}{
		{nil, StatusOK, StatusOK},
		{pct(99.5), StatusOK, StatusOK},
		{pct(95), StatusWarning, StatusOK},
		{pct(80), StatusCritical, StatusWarning},
		{pct(60), StatusCritical, StatusCritical},
	}
	for _, tc := range cases {
		if got := CacheHitStatus(tc.value); got != tc.instance {
			t.Errorf("CacheHitStatus(%v) = %s, want %s", tc.value, got, tc.instance)
		}
		if got := TableCacheHitStatus(tc.value); got != tc.row {
			t.Errorf("TableCacheHitStatus(%v) = %s, want %s", tc.value, got, tc.row)
		}
	}
}

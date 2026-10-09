package util

import (
	"testing"
	"time"
)

func TestStatusThresholds(t *testing.T) {
	seconds := func(value float64) *float64 { return &value }
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	expired, soon, later := now.Add(-time.Hour), now.Add(3*24*time.Hour), now.Add(30*24*time.Hour)
	pid := 42
	errorCount := int64(2)
	cases := []struct {
		name string
		got  Status
		want Status
	}{
		{"lock 1.5 aas", WaitEventStatus("Lock", 1.5), StatusCritical},
		{"lock 0.2 aas", WaitEventStatus("Lock", 0.2), StatusWarning},
		{"io 1 aas", WaitEventStatus("IO", 1), StatusWarning},
		{"io 0.5 aas", WaitEventStatus("IO", 0.5), StatusOK},
		{"cpu 10 aas", WaitEventStatus("CPU", 10), StatusOK},
		{"connections 69%", ConnectionUsageStatus(69), StatusOK},
		{"connections 70%", ConnectionUsageStatus(70), StatusWarning},
		{"connections 90%", ConnectionUsageStatus(90), StatusCritical},
		{"idle in tx 4 min", IdleInTransactionStatus(240), StatusOK},
		{"idle in tx 5 min", IdleInTransactionStatus(300), StatusWarning},
		{"idle in tx 1 h", IdleInTransactionStatus(3600), StatusCritical},
		{"slow 1500 of 1000", SlowQueryStatus(1500, 1000), StatusWarning},
		{"slow 2500 of 1000", SlowQueryStatus(2500, 1000), StatusCritical},
		{"load 25%", QueryLoadStatus(25), StatusWarning},
		{"load 45%", QueryLoadStatus(45), StatusCritical},
		{"running 30 s", LongRunningStatus(30), StatusWarning},
		{"running 90 s", LongRunningStatus(90), StatusCritical},
		{"slot lost", ReplicationSlotStatus(ReplicationSlot{Active: true, WALStatus: "lost"}), StatusCritical},
		{"slot inactive", ReplicationSlotStatus(ReplicationSlot{WALStatus: "reserved"}), StatusWarning},
		{"slot active", ReplicationSlotStatus(ReplicationSlot{Active: true, WALStatus: "reserved"}), StatusOK},
		{"standby 100 MB", StandbyLagStatus(100<<20, nil), StatusWarning},
		{"standby 2 GB", StandbyLagStatus(2<<30, nil), StatusCritical},
		{"standby 90 s", StandbyLagStatus(0, seconds(90)), StatusCritical},
		{"user expired", UserStatus(&expired, now), StatusCritical},
		{"user expiring", UserStatus(&soon, now), StatusWarning},
		{"user valid", UserStatus(&later, now), StatusOK},
		{"user no expiry", UserStatus(nil, now), StatusOK},
		{"extension outdated", ExtensionStatus(Extension{Version: "1.7", DefaultVersion: "1.10"}), StatusWarning},
		{"extension current", ExtensionStatus(Extension{Version: "1.10", DefaultVersion: "1.10"}), StatusOK},
		{"subscription down", SubscriptionStatus(Subscription{Enabled: true}), StatusCritical},
		{"subscription errors", SubscriptionStatus(Subscription{Enabled: true, WorkerPID: &pid, ApplyErrorCount: &errorCount}), StatusWarning},
		{"subscription disabled", SubscriptionStatus(Subscription{}), StatusWarning},
		{"subscription healthy", SubscriptionStatus(Subscription{Enabled: true, WorkerPID: &pid}), StatusOK},
		{"dashboard blocked", DashboardStatus(DashboardResult{BlockedQueries: 1}), StatusCritical},
		{"dashboard quiet", DashboardStatus(DashboardResult{ConnectionPct: 10}), StatusOK},
		{"temp files", TempFileStatus(TempFileUsage{TempFiles: 3}), StatusWarning},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, tc.got, tc.want)
		}
	}
}

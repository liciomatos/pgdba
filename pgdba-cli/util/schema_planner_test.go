package util

import "testing"

func TestHBARuleStatus(t *testing.T) {
	cases := []struct {
		rule HBARule
		want Status
	}{
		{HBARule{Type: "host", Address: "0.0.0.0/0", AuthMethod: "trust"}, StatusCritical},
		{HBARule{Type: "host", Address: "127.0.0.1/255.255.255.255", AuthMethod: "trust"}, StatusWarning},
		{HBARule{Type: "host", Address: "::1/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", AuthMethod: "trust"}, StatusWarning},
		{HBARule{Type: "local", AuthMethod: "trust"}, StatusWarning},
		{HBARule{Type: "host", Address: "10.0.0.0/8", AuthMethod: "password"}, StatusCritical},
		{HBARule{Type: "hostssl", Address: "10.0.0.0/8", AuthMethod: "password"}, StatusWarning},
		{HBARule{Type: "host", Address: "10.0.0.0/8", AuthMethod: "md5"}, StatusWarning},
		{HBARule{Type: "hostssl", Address: "all", AuthMethod: "scram-sha-256"}, StatusOK},
		{HBARule{Type: "host", AuthMethod: "scram-sha-256", Error: "invalid IP address"}, StatusCritical},
	}
	for _, c := range cases {
		if got := HBARuleStatus(c.rule); got != c.want {
			t.Errorf("HBARuleStatus(%+v) = %v, want %v", c.rule, got, c.want)
		}
	}
}

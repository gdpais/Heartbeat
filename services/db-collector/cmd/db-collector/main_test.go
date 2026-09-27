package main

import "testing"

func TestCertificateTrustRequiresExplicitOptIn(t *testing.T) {
	const key = "HEARTBEAT_DB_COLLECTOR_SQLSERVER_TRUST_SERVER_CERTIFICATE"
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"false", false}, {"0", false}, {"invalid", false},
		{"true", true}, {"TRUE", true}, {"1", true}, {"on", true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(key, tc.value)
			if got := boolEnv(key, false); got != tc.want {
				t.Fatalf("boolEnv(%q) = %t, want %t", tc.value, got, tc.want)
			}
		})
	}
}

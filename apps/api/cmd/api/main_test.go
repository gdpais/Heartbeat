package main

import (
	"strings"
	"testing"
)

const listenAddrKey = "HEARTBEAT_API_LISTEN_ADDR"

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv(listenAddrKey, "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8081" {
		t.Fatalf("ListenAddr = %q, want the default :8081", cfg.ListenAddr)
	}
}

func TestLoadConfigReadsTheListenAddress(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:9000", ":0", "[::1]:8081", "localhost:http"} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv(listenAddrKey, addr)
			cfg, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ListenAddr != addr {
				t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, addr)
			}
		})
	}
}

func TestLoadConfigRejectsAnInvalidListenAddress(t *testing.T) {
	// No silent fallback to the default: a typo must stop startup.
	for _, addr := range []string{"8081", "localhost", "127.0.0.1:", ":8081:9000", "[::1"} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv(listenAddrKey, addr)
			_, err := loadConfig()
			if err == nil {
				t.Fatalf("loadConfig accepted %s=%q", listenAddrKey, addr)
			}
			if !strings.Contains(err.Error(), listenAddrKey) {
				t.Fatalf("error %q does not name %s", err, listenAddrKey)
			}
		})
	}
}

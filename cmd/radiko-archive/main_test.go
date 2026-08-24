package main

import (
	"testing"
	"time"
)

func TestParseBroadcastDateUsesJST(t *testing.T) {
	date, err := parseBroadcastDate("2026-08-24")
	if err != nil {
		t.Fatal(err)
	}
	if got := date.Format(time.RFC3339); got != "2026-08-24T00:00:00+09:00" {
		t.Fatalf("unexpected date: %s", got)
	}
}

func TestCleanName(t *testing.T) {
	got := cleanName(`  番組 / title: special?  `, 96)
	if got != "番組_title_special" {
		t.Fatalf("unexpected clean name: %q", got)
	}
}

func TestParseIDSet(t *testing.T) {
	got := parseIDSet("TBS, QRR,TBS, ")
	if len(got) != 2 || !got["TBS"] || !got["QRR"] {
		t.Fatalf("unexpected set: %#v", got)
	}
}

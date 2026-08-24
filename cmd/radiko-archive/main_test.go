package main

import (
	"reflect"
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

func TestParseAreasAll(t *testing.T) {
	got, err := parseAreas("all")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 47 || got[0] != "JP1" || got[46] != "JP47" {
		t.Fatalf("unexpected areas: %#v", got)
	}
}

func TestParseAreasNormalizesAndSorts(t *testing.T) {
	got, err := parseAreas("JP27, jp01,JP13,JP27")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"JP1", "JP13", "JP27"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseAreasRejectsInvalid(t *testing.T) {
	for _, input := range []string{"US1", "JP0", "JP48", "JPx"} {
		if _, err := parseAreas(input); err == nil {
			t.Fatalf("expected error for %q", input)
		}
	}
}

func TestNetrcQuote(t *testing.T) {
	if got := netrcQuote(`a"b\\c`); got != `"a\"b\\\\c"` {
		t.Fatalf("unexpected netrc quote: %s", got)
	}
}

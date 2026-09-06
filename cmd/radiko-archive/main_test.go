package main

import (
	"errors"
	"reflect"
	"strings"
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

func TestIsUnavailableOutput(t *testing.T) {
	rajiko := "[RadikoTimeFree] Extracting URL: rdk://LFR-20260904110000\n" +
		"ERROR: [RadikoTimeFree] LFR-20260904110000: This programme is not available. " +
		"If this is an NHK station, you may wish to try NHK Radiru."
	if !isUnavailableOutput(rajiko) {
		t.Fatal("expected the rajiko rights-restriction message to be detected")
	}
	for _, output := range []string{
		"",
		"ERROR: unable to download m3u8: HTTP Error 503: Service Unavailable",
		"ERROR: fragment 7 not found, unable to continue",
	} {
		if isUnavailableOutput(output) {
			t.Fatalf("expected a retryable failure for %q", output)
		}
	}
}

func TestUnavailableErrorWrapsSentinel(t *testing.T) {
	err := unavailableError("ERROR: [RadikoTimeFree] IBC-20260904110500: This programme is not available.")
	if !errors.Is(err, errProgramUnavailable) {
		t.Fatalf("expected errProgramUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "This programme is not available") {
		t.Fatalf("expected the yt-dlp detail to be preserved: %v", err)
	}
}

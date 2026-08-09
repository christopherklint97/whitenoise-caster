package main

import (
	"testing"
	"time"
)

func TestNextDailyStopUsesConfiguredTimezone(t *testing.T) {
	stockholm, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Fatal(err)
	}

	// 04:30 UTC is 06:30 CEST on this date, so the next run is tomorrow.
	now := time.Date(2026, time.June, 1, 4, 30, 0, 0, time.UTC)
	next := nextDailyStop(now, 6, 30, stockholm)
	want := time.Date(2026, time.June, 2, 6, 30, 0, 0, stockholm)
	if !next.Equal(want) {
		t.Fatalf("nextDailyStop() = %s, want %s", next, want)
	}
}

func TestNextDailyStopPreservesWallClockAcrossDST(t *testing.T) {
	stockholm, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Fatal(err)
	}

	// The day before Sweden moves from CET to CEST.
	now := time.Date(2026, time.March, 28, 7, 0, 0, 0, stockholm)
	next := nextDailyStop(now, 6, 30, stockholm)
	want := time.Date(2026, time.March, 29, 6, 30, 0, 0, stockholm)
	if !next.Equal(want) {
		t.Fatalf("nextDailyStop() = %s, want %s", next, want)
	}
	if _, offset := next.Zone(); offset != 2*60*60 {
		t.Fatalf("nextDailyStop() offset = %d, want CEST (+7200)", offset)
	}
}

func TestParseDailyStop(t *testing.T) {
	hour, minute, location, err := parseDailyStop("06:30", "Europe/Stockholm")
	if err != nil {
		t.Fatal(err)
	}
	if hour != 6 || minute != 30 || location.String() != "Europe/Stockholm" {
		t.Fatalf("parseDailyStop() = %d:%d %s", hour, minute, location)
	}
}

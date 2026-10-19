package main

import (
	"math"
	"testing"
)

// A GB quota that overflows int64 when converted to bytes wraps negative and
// bypassed the plain ">= 0" guard, so every upload silently started failing
// while the dashboard showed "ok". gbToBytes rejects both negatives and
// overflow.
func TestGBToBytes(t *testing.T) {
	cases := []struct {
		gb     float64
		want   int64
		wantOk bool
	}{
		{0, 0, true},
		{1, 1 << 30, true},
		{100, 100 << 30, true},
		{-1, 0, false},      // negative
		{-0.0001, 0, false}, // small negative
		{1e12, 0, false},    // overflows int64 when *2^30
		{math.MaxFloat64, 0, false},
	}
	for _, c := range cases {
		got, ok := gbToBytes(c.gb)
		if ok != c.wantOk {
			t.Errorf("gbToBytes(%v) ok=%v, want %v", c.gb, ok, c.wantOk)
		}
		if ok && got != c.want {
			t.Errorf("gbToBytes(%v) = %d, want %d", c.gb, got, c.want)
		}
		// On rejection the result must never be a wrapped negative.
		if !ok && got < 0 {
			t.Errorf("gbToBytes(%v) returned negative %d on rejection", c.gb, got)
		}
	}

	// The exact boundary the old code broke on: a value whose byte product just
	// exceeds MaxInt64 must be refused, not wrapped to a huge negative.
	overflowGB := (float64(math.MaxInt64) / (1 << 30)) * 2
	if _, ok := gbToBytes(overflowGB); ok {
		t.Errorf("gbToBytes(%v) was accepted; it overflows int64", overflowGB)
	}
}

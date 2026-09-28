package chbl

import "testing"

type testLoadReader map[uint64]float64

func (m testLoadReader) Load(id uint64) float64 { return m[id] }

func TestForwardStaysHomeWhenUnderBound(t *testing.T) {
	ring := NewRing([]uint64{1, 2, 3})
	home := ring.Home(10)
	loads := map[uint64]float64{1: 0.1, 2: 0.1, 3: 0.1}
	id, reason := Forward(ring, loads, 1.0, 3, home)
	if id != home {
		t.Fatalf("home=%d got %d", home, id)
	}
	if reason != ReasonHome {
		t.Fatalf("reason=%q", reason)
	}
}

func TestForwardWalksRingWhenHomeOverBound(t *testing.T) {
	ring := NewRing([]uint64{1, 2, 3})
	home := ring.Home(10)
	loads := map[uint64]float64{1: 2.0, 2: 2.0, 3: 2.0}
	loads[home] = 2.0
	next := ring.Next(home)
	loads[next] = 0.2
	id, reason := Forward(ring, loads, 1.0, 3, home)
	if id != next {
		t.Fatalf("expected next=%d, got %d (home=%d reason=%q)", next, id, home, reason)
	}
	if reason != ReasonForwarded {
		t.Fatalf("reason=%q", reason)
	}
}

func TestForwardLeastLoadedAfterChainExhausted(t *testing.T) {
	ring := NewRing([]uint64{1, 2, 3})
	home := ring.Home(10)
	loads := map[uint64]float64{1: 2.0, 2: 1.5, 3: 3.0}
	id, reason := Forward(ring, loads, 1.0, 3, home)
	if reason != ReasonLeastLoaded {
		t.Fatalf("reason=%q", reason)
	}
	if id != 2 {
		t.Fatalf("expected least-loaded 2, got %d", id)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	if NormalizeBound(0) != DefaultBound {
		t.Fatalf("bound default")
	}
	if NormalizeMaxChainLen(0) != DefaultMaxChainLen {
		t.Fatalf("chain default")
	}
}

func TestSubsetMatchesRebuiltRing(t *testing.T) {
	configured := NewRing([]uint64{7, 19, 41, 83})
	subset := configured.Subset(map[uint64]bool{7: true, 41: true})
	rebuilt := NewRing([]uint64{7, 41})
	for fn := uint64(0); fn < 1000; fn++ {
		if subset.Home(fn) != rebuilt.Home(fn) {
			t.Fatalf("home differs for function %d", fn)
		}
	}
	for _, id := range []uint64{7, 41} {
		if subset.Next(id) != rebuilt.Next(id) {
			t.Fatalf("successor differs for leaf %d", id)
		}
	}
}

func TestForwardByMatchesMapPath(t *testing.T) {
	ring := NewRing([]uint64{7, 19, 41, 83})
	for _, loads := range []map[uint64]float64{
		{7: 0.2, 19: 0.3, 41: 0.4, 83: 0.5},
		{7: 2, 19: 2, 41: 0.4, 83: 2},
		{7: 2, 19: 2, 41: 2, 83: 2},
	} {
		for fn := uint64(0); fn < 100; fn++ {
			home := ring.Home(fn)
			wantID, wantReason := Forward(ring, loads, 1, 3, home)
			gotID, gotReason := ForwardBy(ring, testLoadReader(loads), 1, 3, home)
			if gotID != wantID || gotReason != wantReason {
				t.Fatalf("function %d: got %d %q, want %d %q", fn, gotID, gotReason, wantID, wantReason)
			}
		}
	}
}

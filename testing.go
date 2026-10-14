package sessionfuzz

import (
	"fmt"
	"math/rand/v2"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// SeedBytes is a deterministic pseudo-random input of n bytes.
func SeedBytes(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 0xf022))
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(r.UintN(256))
	}
	return out
}

// SweepSessions and SweepBytes size [Sweep]: 200 sessions of 1000 input bytes
// each take a few seconds against an in-process engine.
const (
	SweepSessions = 200
	SweepBytes    = 1000
)

var digits = regexp.MustCompile(`[0-9]+`)

// Sweep plays SweepSessions deterministic sessions — for plain `go test`, so
// CI checks the invariants without -fuzz. It fails once per new kind of
// violation (numbers are ignored when grouping), printing the replay seed, and
// logs how many sessions hit each known issue.
func Sweep(t *testing.T, engine Engine, opts Options) {
	t.Helper()
	seen := map[string]int{}
	known := map[string]int{}
	for seed := uint64(1); seed <= SweepSessions; seed++ {
		r, err := Run(engine, SeedBytes(seed, SweepBytes), opts)
		if err != nil {
			t.Fatal(err)
		}
		if r == nil {
			continue
		}
		for id := range r.Known {
			known[id]++
		}
		if r.Violation == nil {
			continue
		}
		kind := r.Violation.Invariant + ": " + digits.ReplaceAllString(r.Violation.Detail, "N")
		seen[kind]++
		if seen[kind] == 1 {
			t.Errorf("seed %d (replay with SESSIONFUZZ_SEED=%d): %s", seed, seed, r.Report())
		}
	}
	for kind, n := range seen {
		t.Logf("%4d sessions: NEW %s", n, kind)
	}
	for id, n := range known {
		t.Logf("%4d sessions hit known issue %s", n, id)
	}
}

// Fuzz registers seeds and the fuzz target for coverage-guided fuzzing:
//
//	func FuzzSession(f *testing.F) { sessionfuzz.Fuzz(f, myEngine, opts) }
//
//	go test -run '^$' -fuzz FuzzSession -fuzztime 5m
func Fuzz(f *testing.F, engine Engine, opts Options) {
	for seed := uint64(1); seed <= 16; seed++ {
		f.Add(SeedBytes(seed, 600))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := Run(engine, data, opts)
		if err != nil {
			t.Fatal(err)
		}
		if r != nil && r.Violation != nil {
			t.Fatal(r.Report())
		}
	})
}

// Replay prints one [Sweep] session in full when SESSIONFUZZ_SEED is set —
// the script, the final table, and the session as a poker-scene-dsl scenario —
// and skips otherwise:
//
//	SESSIONFUZZ_SEED=5 go test -run TestReplay -v
func Replay(t *testing.T, engine Engine, opts Options) {
	t.Helper()
	raw := os.Getenv("SESSIONFUZZ_SEED")
	if raw == "" {
		t.Skip("set SESSIONFUZZ_SEED to replay a sweep session")
	}
	seed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(engine, SeedBytes(seed, SweepBytes), opts)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range r.Script {
		t.Logf("%3d  %s", i+1, line)
	}
	t.Log("\n" + DescribeState(r.Final))
	var b strings.Builder
	b.WriteString("as a github.com/block52/poker-scene-dsl scenario:\n\n\tsc.Act(\n")
	for _, line := range r.Scene() {
		fmt.Fprintf(&b, "\t\t%q,\n", line)
	}
	b.WriteString("\t)\n")
	t.Log("\n" + b.String())
	if r.Violation != nil {
		t.Fatalf("invariant %q violated: %s", r.Violation.Invariant, r.Violation.Detail)
	}
}

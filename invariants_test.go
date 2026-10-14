package sessionfuzz

import (
	"errors"
	"math/big"
	"regexp"
	"strings"
	"testing"
)

// fakeTable reports a fixed state and hands every action to apply.
type fakeTable struct {
	state State
	apply func(*fakeTable, Action) error
}

func (f *fakeTable) State() State { return f.state }
func (f *fakeTable) Apply(a Action) error {
	if f.apply == nil {
		return errors.New("rejected")
	}
	return f.apply(f, a)
}

func engineFor(t *fakeTable) Engine {
	return EngineFunc(func(Config) (Table, error) { return t, nil })
}

func chips(n int64) *big.Int { return big.NewInt(n) }

func player(seat int, stack int64, status Status, legal ...string) Player {
	p := Player{Address: DefaultAddresses(12)[seat-1], Seat: seat, Stack: chips(stack), Status: status}
	for _, a := range legal {
		p.Legal = append(p.Legal, LegalAction{Action: a})
	}
	return p
}

// sessionAt builds a session whose table is already in st, with the chip
// total the fuzzer would expect.
func sessionAt(st State, total int64, opts Options) *session {
	return &session{
		opts: &opts, cfg: DefaultConfig(0x01), state: st, chips: chips(total),
		res: &Result{Known: map[string]int{}},
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name      string
		state     State
		total     int64
		invariant string
		detail    string
	}{
		{
			name:  "healthy hand",
			state: State{Round: Flop, NextToAct: 1, Pot: chips(200), BigBlind: 2, SmallBlind: 1, Players: []Player{player(1, 900, Active, ActCheck, ActBet), player(2, 900, Active)}},
			total: 2000,
		},
		{
			name:      "busted with chips",
			state:     State{Round: End, Pot: chips(0), Players: []Player{player(1, 40, Busted), player(2, 960, Active, ActNewHand)}, NextToAct: 2},
			total:     1000,
			invariant: "status-stack", detail: "seat 1 is busted with stack 40",
		},
		{
			name:      "chips vanish",
			state:     State{Round: End, Pot: chips(800), Players: []Player{player(1, 600, Active, ActNewHand), player(2, 600, Active, ActNewHand)}, NextToAct: 1},
			total:     2000,
			invariant: "chip-conservation", detail: "stacks + pot = 1200, want 2000",
		},
		{
			name:      "nextToAct is busted",
			state:     State{Round: Turn, NextToAct: 1, Pot: chips(0), Players: []Player{player(1, 0, Busted), player(2, 1000, Active, ActCheck)}},
			total:     1000,
			invariant: "liveness", detail: "nextToAct=1 is busted in turn",
		},
		{
			name:      "blind on a seat that is not dealt in",
			state:     State{Round: Ante, NextToAct: 3, Pot: chips(0), Players: []Player{player(1, 500, Active), player(2, 500, Active), player(3, 500, Seated, ActSitIn)}},
			total:     1500,
			invariant: "liveness", detail: "nextToAct=3 (seated) has no progress action in ante",
		},
		{
			name:      "small and big blind collapse",
			state:     State{Round: Preflop, NextToAct: 1, SmallBlind: 2, BigBlind: 2, Pot: chips(150), Players: []Player{player(1, 900, Active, ActCall), player(2, 950, Active), player(3, 1000, Folded)}},
			total:     3000,
			invariant: "positions", detail: "small and big blind are both seat 2",
		},
		{
			name:      "nobody offered new-hand",
			state:     State{Round: End, NextToAct: 1, Pot: chips(0), Players: []Player{player(1, 1000, Folded, ActSitOut), player(5, 1000, SittingIn, ActSitOut)}},
			total:     2000,
			invariant: "stall-at-end", detail: "players with chips want to play [folded sitting-in]",
		},
		{
			name:      "new-hand offered off the clock",
			state:     State{Round: End, NextToAct: 1, Pot: chips(0), Players: []Player{player(1, 1000, Folded), player(2, 1000, Active, ActNewHand)}},
			total:     2000,
			invariant: "end-nextToAct", detail: "offered to seats [2] but nextToAct=1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := check(sessionAt(c.state, c.total, Options{}))
			if c.invariant == "" {
				if v != nil {
					t.Fatalf("want no violation, got %v", v)
				}
				return
			}
			if v == nil || v.Invariant != c.invariant || !strings.Contains(v.Detail, c.detail) {
				t.Fatalf("want %s containing %q, got %v", c.invariant, c.detail, v)
			}
		})
	}
}

func TestBustBelowRule(t *testing.T) {
	st := State{Round: End, Pot: chips(0), NextToAct: 2, Players: []Player{player(1, 40, Busted), player(2, 960, Active, ActNewHand)}}
	opts := Options{Rules: Rules{BustBelow: func(Config) *big.Int { return chips(50) }}}
	if v := check(sessionAt(st, 1000, opts)); v != nil {
		t.Fatalf("a 40-chip stack busts under a 50 small blind; got %v", v)
	}
}

// joinOnly seats joiners with their buy-in and rejects everything else.
func joinOnly(f *fakeTable, a Action) error {
	if a.Action != ActJoin {
		return errors.New("rejected")
	}
	for _, p := range f.state.Players {
		if p.Seat == a.Seat || p.Address == a.Player {
			return errors.New("taken")
		}
	}
	f.state.Players = append(f.state.Players, Player{Address: a.Player, Seat: a.Seat, Stack: new(big.Int).Set(a.Amount), Status: Seated})
	return nil
}

func TestRunHealthyTable(t *testing.T) {
	table := &fakeTable{state: State{Round: Ante, Pot: chips(0)}, apply: joinOnly}
	r, err := Run(engineFor(table), SeedBytes(1, 500), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Violation != nil {
		t.Fatalf("want no violation, got %s", r.Report())
	}
	if len(table.state.Players) == 0 {
		t.Fatal("the session never joined anyone")
	}
}

func TestRunReportsPanics(t *testing.T) {
	table := &fakeTable{state: State{Round: Ante, Pot: chips(0)}, apply: func(f *fakeTable, a Action) error {
		if a.Action == ActJoin && len(f.state.Players) == 1 {
			panic("index out of range")
		}
		return joinOnly(f, a)
	}}
	r, _ := Run(engineFor(table), SeedBytes(2, 500), Options{})
	if r.Violation == nil || r.Violation.Invariant != "no-panic" || !strings.Contains(r.Violation.Detail, "index out of range") {
		t.Fatalf("want a no-panic violation, got %v", r.Violation)
	}
	if !strings.Contains(r.Report(), "<- PANIC") {
		t.Fatalf("the script should mark the panicking action:\n%s", r.Report())
	}
}

func TestKnownIssuesAreCountedNotFailed(t *testing.T) {
	table := &fakeTable{state: State{Round: Ante, Pot: chips(0)}, apply: func(f *fakeTable, a Action) error {
		if a.Action == ActJoin {
			panic("no payout structure")
		}
		return errors.New("rejected")
	}}
	opts := Options{Known: []KnownIssue{{ID: "#1", Match: regexp.MustCompile(`no payout structure`)}}}
	r, _ := Run(engineFor(table), SeedBytes(3, 500), opts)
	if r.Violation != nil {
		t.Fatalf("a known issue must not fail the run: %v", r.Violation)
	}
	if r.Known["#1"] == 0 {
		t.Fatal("the known issue was not counted")
	}
}

func TestRejectGate(t *testing.T) {
	table := &fakeTable{state: State{Round: Ante, Pot: chips(0)}, apply: joinOnly}
	opts := Options{Rules: Rules{Reject: func(_ Config, _ State, a Action) bool { return a.Action == ActJoin }}}
	if _, err := Run(engineFor(table), SeedBytes(4, 500), opts); err != nil {
		t.Fatal(err)
	}
	if len(table.state.Players) != 0 {
		t.Fatal("rejected joins reached the engine")
	}
}

func TestDeckIsAShuffledPack(t *testing.T) {
	d := Deck(7)
	seen := map[string]bool{}
	for _, c := range d {
		seen[c] = true
	}
	if len(d) != 52 || len(seen) != 52 {
		t.Fatalf("want 52 distinct cards, got %d (%d distinct)", len(d), len(seen))
	}
	if strings.Join(Deck(7), "") != strings.Join(d, "") {
		t.Fatal("Deck is not deterministic")
	}
}

func TestScene(t *testing.T) {
	r := &Result{Actions: []Recorded{
		{Action: Action{Player: "player03", Action: ActJoin, Seat: 2, Amount: chips(1000)}},
		{Action: Action{Player: "player03", Action: ActSitIn}, Seat: 2},
		{Action: Action{Player: "player03", Action: ActRaise, Amount: chips(250)}, Seat: 2},
		{Action: Action{Action: ActTimeout}},
		{Action: Action{Player: "player03", Action: ActNewHand, Deck: []string{"AS", "KD"}}, Seat: 2},
	}}
	want := []string{"player03 joins seat 2 with 1000", "seat 2 sits in", "seat 2 raises by 250", "time runs out", "new hand with deck AS-KD"}
	got := r.Scene()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Scene() = %q, want %q", got, want)
	}
}

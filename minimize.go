package sessionfuzz

import (
	"fmt"
	"math/big"
)

// Kind is a violation with its numbers removed ("liveness: nextToAct=N is
// busted in turn"), so two sessions hitting the same bug at different seats or
// amounts compare equal.
func (v *Violation) Kind() string {
	return v.Invariant + ": " + digits.ReplaceAllString(v.Detail, "N")
}

// Minimize shrinks a failing session to the shortest list of actions that
// still produces the same kind of violation, so Result.Scene() becomes a short
// scenario worth committing as a test. It replays subsets of r.Actions on
// fresh tables, dropping chunks while the violation persists (halving the
// chunk size down to single actions). Actions a smaller replay makes illegal
// are rejected by the engine and left out of the result.
//
// target is the violation to keep; nil means r.Violation. A known issue's
// violation can be minimised too: pass it as target.
func Minimize(engine Engine, r *Result, opts Options, target *Violation) (*Result, error) {
	if target == nil {
		target = r.Violation
	}
	if target == nil {
		return nil, fmt.Errorf("sessionfuzz: nothing to minimise: the session has no violation")
	}
	want := target.Kind()
	best, err := replay(engine, r.Config, r.Actions, opts, want)
	if err != nil {
		return nil, err
	}
	if best == nil {
		return nil, fmt.Errorf("sessionfuzz: the recorded actions do not reproduce %q", want)
	}
	for chunk := len(best.Actions) / 2; chunk >= 1; chunk /= 2 {
		for i := 0; i < len(best.Actions); {
			candidate := append(append([]Recorded{}, best.Actions[:i]...), best.Actions[min(i+chunk, len(best.Actions)):]...)
			got, err := replay(engine, r.Config, candidate, opts, want)
			if err != nil {
				return nil, err
			}
			if got != nil && len(got.Actions) < len(best.Actions) {
				best = got
				continue
			}
			i += chunk
		}
	}
	return best, nil
}

// replay plays actions on a fresh table, on the same clock as a fuzz session,
// and stops at the first violation of kind want. It returns nil when that
// violation never happens. Other violations, known or not, are ignored.
func replay(engine Engine, cfg Config, actions []Recorded, opts Options, want string) (*Result, error) {
	table, err := engine.NewTable(cfg)
	if err != nil {
		return nil, fmt.Errorf("sessionfuzz: new table: %w", err)
	}
	s := &session{
		opts: &opts, cfg: cfg, table: table, state: table.State(), addrs: opts.addresses(),
		clock: 1_700_000_000_000, chips: big.NewInt(0),
		res: &Result{Config: cfg, Known: map[string]int{}, KnownFirst: map[string]*Violation{}},
	}
	s.logf("table %s, %d seats, blinds %s/%s", cfg.Format, cfg.MaxPlayers, cfg.SmallBlind, cfg.BigBlind)
	for _, a := range actions {
		if !s.apply(a.Action) {
			if v := s.panicked; v != nil && v.Kind() == want {
				s.res.Violation = v
				s.res.Final = s.state
				return s.res, nil
			}
			s.panicked = nil
			continue
		}
		if v := check(s); v != nil && v.Kind() == want {
			s.res.Violation = v
			s.res.Final = s.state
			return s.res, nil
		}
	}
	return nil, nil
}

package sessionfuzz

import (
	"fmt"
	"math/big"
	"slices"
)

// The invariants, checked after every accepted action:
//
//   - no-panic: the engine never panics, on any action, legal or not.
//   - status-stack: busted ⇒ the stack is below Rules.BustBelow; all-in ⇒ an
//     empty stack while a hand is live.
//   - chip-conservation: stacks + pot (+ pending top-ups) change only when a
//     player joins, tops up or leaves.
//   - liveness: during a hand, and before the deal once two or more players
//     are active, the nextToAct seat holds a live player with a legal action
//     that moves play on.
//   - positions: in a dealt hand the big blind is a player in the hand, and
//     the small and big blind are different seats.
//   - stall-at-end: between hands, when enough players with chips want to
//     play, someone is offered new-hand.
//   - end-nextToAct: between hands, new-hand is offered to the nextToAct seat
//     (a UI that shows the button to the seat on the clock needs this).
func check(s *session) *Violation {
	st := &s.state
	progress := s.opts.Rules.progress()

	bustBelow := s.opts.Rules.bustBelow(s.cfg)
	total := new(big.Int)
	if st.Round != End && st.Pot != nil {
		total.Set(st.Pot)
	}
	for _, p := range st.Players {
		total.Add(total, p.Stack)
		if p.Pending != nil {
			total.Add(total, p.Pending)
		}
		if p.Status == Busted && p.Stack.Cmp(bustBelow) >= 0 {
			return &Violation{"status-stack", fmt.Sprintf("seat %d is busted with stack %s", p.Seat, p.Stack)}
		}
		if p.Status == AllIn && p.Stack.Sign() != 0 && st.Round != End {
			return &Violation{"status-stack", fmt.Sprintf("seat %d is all-in with stack %s in %s", p.Seat, p.Stack, st.Round)}
		}
	}
	if total.Cmp(s.chips) != 0 {
		return &Violation{"chip-conservation", fmt.Sprintf("stacks + pot = %s, want %s (round %s)", total, s.chips, st.Round)}
	}

	hasProgress := func(p *Player) bool {
		for _, la := range p.Legal {
			if progress[la.Action] {
				return true
			}
		}
		return false
	}

	// During a hand the seat on the clock must be able to move it on.
	if st.Round != End && st.Round != Ante {
		p := s.playerAtSeat(st.NextToAct)
		switch {
		case p == nil:
			return &Violation{"liveness", fmt.Sprintf("nextToAct=%d is an empty seat in %s", st.NextToAct, st.Round)}
		case p.Status == Busted:
			return &Violation{"liveness", fmt.Sprintf("nextToAct=%d is busted in %s", st.NextToAct, st.Round)}
		case !hasProgress(p):
			return &Violation{"liveness", fmt.Sprintf("nextToAct=%d (%s) has no progress action in %s", st.NextToAct, p.Status, st.Round)}
		}
	}

	// Before the deal: once two or more players are active, the seat to act
	// must be able to post its blind or deal, or the hand never starts.
	if st.Round == Ante {
		active := 0
		for _, p := range st.Players {
			if p.Status == Active {
				active++
			}
		}
		if active >= 2 {
			p := s.playerAtSeat(st.NextToAct)
			switch {
			case p == nil:
				return &Violation{"liveness", fmt.Sprintf("nextToAct=%d is an empty seat in ante", st.NextToAct)}
			case !hasProgress(p):
				return &Violation{"liveness", fmt.Sprintf("nextToAct=%d (%s) has no progress action in ante", st.NextToAct, p.Status)}
			}
		}
	}

	// Positions in a dealt hand.
	if st.Round != End && st.Round != Ante && st.BigBlind != 0 {
		bb := s.playerAtSeat(st.BigBlind)
		if bb == nil || bb.Status == Seated || bb.Status == SittingOut || bb.Status == Busted {
			status := "empty"
			if bb != nil {
				status = string(bb.Status)
			}
			return &Violation{"positions", fmt.Sprintf("big blind seat %d is %s in %s", st.BigBlind, status, st.Round)}
		}
		if st.SmallBlind == st.BigBlind {
			return &Violation{"positions", fmt.Sprintf("small and big blind are both seat %d in %s", st.BigBlind, st.Round)}
		}
	}

	// Between hands.
	if st.Round == End {
		willing, offered, statuses := 0, []int{}, []string{}
		for _, p := range st.Players {
			if offers(&p, ActNewHand) {
				offered = append(offered, p.Seat)
			}
			switch p.Status {
			case Seated, SittingOut, Busted:
				continue
			}
			if p.Stack.Sign() > 0 {
				willing++
				statuses = append(statuses, string(p.Status))
			}
		}
		slices.Sort(statuses)
		filling := s.cfg.Format == SitAndGo && len(st.Players) < s.cfg.MaxPlayers
		if willing >= s.opts.Rules.newHandFloor(s.cfg) && !filling && len(offered) == 0 {
			return &Violation{"stall-at-end", fmt.Sprintf("%s, %d seats: players with chips want to play %v but nobody is offered new-hand", s.cfg.Format, s.cfg.MaxPlayers, statuses)}
		}
		if len(offered) > 0 && !slices.Contains(offered, st.NextToAct) {
			return &Violation{"end-nextToAct", fmt.Sprintf("new-hand is offered to seats %v but nextToAct=%d", offered, st.NextToAct)}
		}
	}
	return nil
}

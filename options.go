package sessionfuzz

import (
	"fmt"
	"math/big"
	"regexp"
)

// Options configures a run. The zero value works: it uses [DefaultConfig] and
// the default rules.
type Options struct {
	// Config picks the table for a session from the session's first input byte.
	// Nil means [DefaultConfig].
	Config func(choice byte) Config
	// Rules are the engine's own conventions the invariants depend on.
	Rules Rules
	// Known are open bugs: a matching violation is counted and play continues.
	Known []KnownIssue
	// Addresses is the pool of players a session draws from. Nil means
	// [DefaultAddresses](12). Use more addresses than seats so that a fresh
	// player can take a seat someone left.
	Addresses []string
}

// Rules capture where engines legitimately differ.
type Rules struct {
	// BustBelow is the smallest stack that is NOT busted. Nil means 1: only an
	// empty stack busts. An engine that busts a cash stack too small to post a
	// blind returns the small blind here for cash tables.
	BustBelow func(Config) *big.Int
	// NewHandFloor is how many players with chips must want to play before
	// someone must be offered new-hand. Nil means MinPlayers for cash and 2
	// for a sit-and-go.
	NewHandFloor func(Config) int
	// Reject is the host's own gate, applied before the engine sees an action:
	// return true for actions the host never forwards, so the fuzzer only
	// reaches states the host can. Nil forwards everything.
	Reject func(Config, State, Action) bool
	// Progress are the actions that move a hand or the table on (leave,
	// sit-out and top-up do not). Nil means [DefaultProgress].
	Progress map[string]bool
}

// DefaultProgress is the default set of actions that move play on.
var DefaultProgress = map[string]bool{
	ActPostSmallBlind: true, ActPostBigBlind: true, ActFold: true, ActCheck: true,
	ActCall: true, ActBet: true, ActRaise: true, ActAllIn: true, ActShow: true,
	ActMuck: true, ActDeal: true, ActNewHand: true,
}

// KnownIssue marks violations of an open bug. Match is tested against
// "invariant: detail" (see [Violation.Error]).
type KnownIssue struct {
	ID    string
	Match *regexp.Regexp
}

// DefaultConfig maps a byte to one of ten tables: cash or sit-and-go, with
// 2, 3, 4, 6 or 9 seats, blinds 50/100. Stacks are short (10 big blinds for a
// sit-and-go, with one-minute levels) so busts and blind rises happen inside a
// short session.
func DefaultConfig(choice byte) Config {
	maxPlayers := []int{2, 3, 4, 6, 9}[int(choice)%5]
	if choice&0x08 != 0 {
		return Config{
			Format: SitAndGo, MaxPlayers: maxPlayers, MinPlayers: maxPlayers,
			SmallBlind: big.NewInt(50), BigBlind: big.NewInt(100),
			MinBuyIn: big.NewInt(1_000_000), MaxBuyIn: big.NewInt(1_000_000),
			StartingStack: big.NewInt(1000), BlindLevelMinutes: 1,
		}
	}
	return Config{
		Format: Cash, MaxPlayers: maxPlayers, MinPlayers: 2,
		SmallBlind: big.NewInt(50), BigBlind: big.NewInt(100),
		MinBuyIn: big.NewInt(200), MaxBuyIn: big.NewInt(2000),
	}
}

// DefaultAddresses returns n distinct placeholder player addresses.
func DefaultAddresses(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("player%02d", i+1)
	}
	return out
}

func (o *Options) config(b byte) Config {
	if o.Config != nil {
		return o.Config(b)
	}
	return DefaultConfig(b)
}

func (o *Options) addresses() []string {
	if o.Addresses != nil {
		return o.Addresses
	}
	return DefaultAddresses(12)
}

func (r *Rules) bustBelow(c Config) *big.Int {
	if r.BustBelow != nil {
		return r.BustBelow(c)
	}
	return big.NewInt(1)
}

func (r *Rules) newHandFloor(c Config) int {
	if r.NewHandFloor != nil {
		return r.NewHandFloor(c)
	}
	if c.Format == Cash {
		return c.MinPlayers
	}
	return 2
}

func (r *Rules) progress() map[string]bool {
	if r.Progress != nil {
		return r.Progress
	}
	return DefaultProgress
}

func (o *Options) knownIssue(v *Violation) string {
	for _, k := range o.Known {
		if k.Match.MatchString(v.Error()) {
			return k.ID
		}
	}
	return ""
}

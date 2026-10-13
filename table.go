// Package sessionfuzz fuzzes a poker engine across whole multi-hand sessions.
//
// Hand-history corpora (PHH, site histories) test single hands: every hand
// starts from given seats, button and blinds. The bugs that freeze real tables
// live in the step from one hand to the next — busts, button and blind
// movement, sit-in / sit-out, joins and leaves, tournament eliminations. This
// package plays whole sessions against any engine that implements [Table],
// mostly choosing among each seat's own legal actions, and checks invariants
// that must always hold after every action. There is no external oracle: a
// violation is a bug.
//
// The engine is plugged in through two small interfaces, [Engine] and [Table].
// The vocabulary (actions, statuses, rounds) is plain poker English; an adapter
// maps it to and from the engine's own types.
package sessionfuzz

import "math/big"

// Format is the table format.
type Format string

const (
	Cash     Format = "cash"
	SitAndGo Format = "sit-and-go"
)

// Round is the betting round. Ante covers the time before cards are dealt
// (blinds being posted); End is the gap between hands.
type Round string

const (
	Ante     Round = "ante"
	Preflop  Round = "preflop"
	Flop     Round = "flop"
	Turn     Round = "turn"
	River    Round = "river"
	Showdown Round = "showdown"
	End      Round = "end"
)

// Status is a player's seat status.
type Status string

const (
	// In a hand.
	Active  Status = "active"
	Folded  Status = "folded"
	AllIn   Status = "all-in"
	Showing Status = "showing"
	// Out of the hand.
	Busted     Status = "busted"
	Seated     Status = "seated"      // joined, has not chosen to play yet
	SittingOut Status = "sitting-out" // chose not to play
	// Pending entrants: will be dealt in on a later hand.
	SittingIn          Status = "sitting-in"            // post now, enter next legal hand
	WaitingForBigBlind Status = "waiting-for-big-blind" // enter when the big blind reaches the seat
)

// Action names the fuzzer sends or reasons about. Betting actions are taken
// verbatim from each player's legal actions, so an engine may offer more.
const (
	ActJoin           = "join"
	ActLeave          = "leave"
	ActSitIn          = "sit-in"
	ActSitInAndWait   = "sit-in-and-wait"
	ActSitOut         = "sit-out"
	ActTopUp          = "top-up"
	ActNewHand        = "new-hand"
	ActTimeout        = "timeout"
	ActDeal           = "deal"
	ActPostSmallBlind = "post-small-blind"
	ActPostBigBlind   = "post-big-blind"
	ActFold           = "fold"
	ActCheck          = "check"
	ActCall           = "call"
	ActBet            = "bet"
	ActRaise          = "raise"
	ActAllIn          = "all-in"
	ActShow           = "show"
	ActMuck           = "muck"
)

// Config is one table the fuzzer plays. All amounts are chips except BuyIn
// for a sit-and-go, which is whatever unit the host charges.
type Config struct {
	Format     Format
	MaxPlayers int
	// MinPlayers is the fewest players a cash table deals to. For a sit-and-go
	// the table starts when MaxPlayers have joined.
	MinPlayers int
	SmallBlind *big.Int
	BigBlind   *big.Int
	// Cash: the buy-in range. Sit-and-go: MinBuyIn is the entry price.
	MinBuyIn *big.Int
	MaxBuyIn *big.Int
	// Sit-and-go only.
	StartingStack     *big.Int
	BlindLevelMinutes int
}

// LegalAction is one action a player may take now, with its amount range when
// it carries one (nil for fold / check / leave and the like).
type LegalAction struct {
	Action string
	Min    *big.Int
	Max    *big.Int
}

// Player is one seated player as the engine reports it.
type Player struct {
	Address string
	Seat    int
	Stack   *big.Int
	Status  Status
	// Pending is chips owned by the player but not yet in the stack (a top-up
	// queued during a hand). Nil when none.
	Pending *big.Int
	Legal   []LegalAction
}

// State is the engine's view of the table after an action. Seat numbers are
// 1-based; 0 means "none".
type State struct {
	Round      Round
	HandNumber int
	NextToAct  int
	Dealer     int
	SmallBlind int
	BigBlind   int
	// Pot is every chip in the middle (all streets, all side pots). It is not
	// read at End, where the pot has already been paid out.
	Pot     *big.Int
	Players []Player
}

// Action is one move the fuzzer sends.
type Action struct {
	// Player is the acting player's address; empty for a timeout, which acts
	// for whoever is on the clock.
	Player string
	Action string
	Amount *big.Int
	// Seat is the seat to take (join only).
	Seat int
	// Deck is the next hand's deck, top first (new-hand only). An engine that
	// shuffles itself may ignore it, at the cost of determinism.
	Deck []string
	// Time is the action's timestamp in Unix milliseconds. The fuzzer advances
	// it a second per action, and ten minutes before a timeout.
	Time int64
}

// Table is one live table. Apply must leave the table unchanged when it
// returns an error (a rejected action is normal: the fuzzer tries things).
// A panic in Apply is caught and reported as a violation.
type Table interface {
	State() State
	Apply(Action) error
}

// Engine creates tables.
type Engine interface {
	NewTable(Config) (Table, error)
}

// EngineFunc adapts a function to [Engine].
type EngineFunc func(Config) (Table, error)

func (f EngineFunc) NewTable(c Config) (Table, error) { return f(c) }

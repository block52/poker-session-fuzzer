package sessionfuzz

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"strings"
)

// input is the fuzz byte stream. Reads past the end report ok=false, which
// ends the session.
type input struct {
	data []byte
	pos  int
}

func (in *input) next() (byte, bool) {
	if in.pos >= len(in.data) {
		return 0, false
	}
	b := in.data[in.pos]
	in.pos++
	return b, true
}

// pick returns a choice in [0, n), or ok=false when the input is exhausted.
func (in *input) pick(n int) (int, bool) {
	b, ok := in.next()
	if !ok || n <= 0 {
		return 0, ok
	}
	return int(b) % n, true
}

// Violation is an invariant failure.
type Violation struct {
	Invariant string
	Detail    string
}

func (v *Violation) Error() string { return v.Invariant + ": " + v.Detail }

// Result is one played session.
type Result struct {
	Config Config
	// Script is the readable log of every accepted action — the repro.
	Script []string
	// Final is the table state when the session stopped.
	Final State
	// Violation is the first violation not matched by a known issue, or nil.
	Violation *Violation
	// Known counts violations of each known issue.
	Known map[string]int
}

type session struct {
	opts  *Options
	cfg   Config
	table Table
	state State
	addrs []string
	clock int64
	hand  uint64
	chips *big.Int
	res   *Result
	// panicked holds a panic until the step that caused it returns.
	panicked *Violation
}

// Run plays one session from data and returns what happened. The first input
// byte picks the table; the rest drive the play.
func Run(engine Engine, data []byte, opts Options) (*Result, error) {
	in := &input{data: data}
	choice, ok := in.next()
	if !ok {
		return nil, nil
	}
	cfg := opts.config(choice)
	table, err := engine.NewTable(cfg)
	if err != nil {
		return nil, fmt.Errorf("sessionfuzz: new table: %w", err)
	}
	s := &session{
		opts: &opts, cfg: cfg, table: table, state: table.State(), addrs: opts.addresses(),
		clock: 1_700_000_000_000, chips: big.NewInt(0),
		res: &Result{Config: cfg, Known: map[string]int{}},
	}
	s.logf("table %s, %d seats, blinds %s/%s", cfg.Format, cfg.MaxPlayers, cfg.SmallBlind, cfg.BigBlind)
	for s.step(in) {
		if v := s.panicked; v != nil {
			s.panicked = nil
			if !s.known(v) {
				s.res.Violation = v
				break
			}
		}
		if v := check(s); v != nil && !s.known(v) {
			s.res.Violation = v
			break
		}
	}
	s.res.Final = s.state
	return s.res, nil
}

// known counts v against a known issue and reports whether it matched.
func (s *session) known(v *Violation) bool {
	id := s.opts.knownIssue(v)
	if id == "" {
		return false
	}
	s.res.Known[id]++
	return true
}

func (s *session) logf(format string, args ...any) {
	s.res.Script = append(s.res.Script, fmt.Sprintf(format, args...))
}

// Deck returns a deterministic shuffled 52-card deck ("AS", "TD", …).
func Deck(seed uint64) []string {
	cards := make([]string, 0, 52)
	for _, suit := range "CDHS" {
		for _, rank := range "23456789TJQKA" {
			cards = append(cards, string(rank)+string(suit))
		}
	}
	r := rand.New(rand.NewPCG(seed, 0x2663))
	r.Shuffle(len(cards), func(i, j int) { cards[i], cards[j] = cards[j], cards[i] })
	return cards
}

// apply sends one action. A rejection is normal and leaves the state as it was.
func (s *session) apply(a Action) (accepted bool) {
	if s.opts.Rules.Reject != nil && s.opts.Rules.Reject(s.cfg, s.state, a) {
		return false
	}
	s.clock += 1000
	a.Time = s.clock
	before := s.playerByAddr(a.Player)
	who := a.Player
	if before != nil {
		who = fmt.Sprintf("seat %d", before.Seat)
	}
	if who == "" {
		who = "clock"
	}
	defer func() {
		if r := recover(); r != nil {
			s.logf("%s: %s %s  <- PANIC", who, a.Action, amountString(a.Amount))
			s.panicked = &Violation{"no-panic", fmt.Sprintf("%s by %s: %v", a.Action, who, r)}
			accepted = false
		}
	}()
	if err := s.table.Apply(a); err != nil {
		return false
	}
	next := s.table.State()

	// Chips enter only with a joining player or a top-up, and leave only with
	// a leaving player.
	switch a.Action {
	case ActJoin:
		if p := findPlayer(next.Players, a.Player); p != nil {
			s.chips.Add(s.chips, p.Stack)
		}
	case ActLeave:
		if before != nil {
			s.chips.Sub(s.chips, before.Stack)
			if before.Pending != nil {
				s.chips.Sub(s.chips, before.Pending)
			}
		}
	case ActTopUp:
		s.chips.Add(s.chips, a.Amount)
	}

	line := fmt.Sprintf("%s: %s", who, a.Action)
	if a.Amount != nil && a.Amount.Sign() != 0 {
		line += " " + a.Amount.String()
	}
	if a.Action == ActJoin {
		line = fmt.Sprintf("%s: join seat %d with %s", a.Player, a.Seat, amountString(a.Amount))
	}
	if before != nil && a.Action != ActTimeout && !offers(before, a.Action) {
		// The engine accepted an action it did not offer this player.
		line += "   (not in legal actions)"
	}
	s.logf("%s", line)
	s.state = next
	return true
}

func amountString(a *big.Int) string {
	if a == nil {
		return "0"
	}
	return a.String()
}

func offers(p *Player, action string) bool {
	for _, la := range p.Legal {
		if la.Action == action {
			return true
		}
	}
	return false
}

func (s *session) playerByAddr(addr string) *Player { return findPlayer(s.state.Players, addr) }

func findPlayer(players []Player, addr string) *Player {
	if addr == "" {
		return nil
	}
	for i := range players {
		if players[i].Address == addr {
			return &players[i]
		}
	}
	return nil
}

func (s *session) playerAtSeat(seat int) *Player {
	for i := range s.state.Players {
		if s.state.Players[i].Seat == seat {
			return &s.state.Players[i]
		}
	}
	return nil
}

// step makes one move. It returns false when the input is exhausted.
func (s *session) step(in *input) bool {
	op, ok := in.pick(16)
	if !ok {
		return false
	}
	switch {
	case op < 9:
		// Most steps: the seat to act plays one of its own legal actions.
		if p := s.playerAtSeat(s.state.NextToAct); p != nil {
			return s.playLegal(in, p)
		}
		return s.playAnyone(in)
	case op < 12:
		// Any seat plays any of its legal actions: sit-out, leave, new-hand,
		// sit-in, and out-of-turn moves the engine must refuse.
		return s.playAnyone(in)
	case op < 14:
		return s.join(in)
	case op < 15:
		// Let the turn clock run out, then time the seat out.
		s.clock += 10 * 60 * 1000
		s.apply(Action{Action: ActTimeout})
		return true
	default:
		// new-hand from any seated player, offered or not.
		if len(s.state.Players) == 0 {
			return true
		}
		i, ok := in.pick(len(s.state.Players))
		if !ok {
			return false
		}
		return s.newHand(in, s.state.Players[i].Address)
	}
}

func (s *session) playAnyone(in *input) bool {
	if len(s.state.Players) == 0 {
		return s.join(in)
	}
	i, ok := in.pick(len(s.state.Players))
	if !ok {
		return false
	}
	return s.playLegal(in, &s.state.Players[i])
}

// playLegal plays one of p's legal actions, with an amount in [Min, Max].
func (s *session) playLegal(in *input, p *Player) bool {
	if len(p.Legal) == 0 {
		return true
	}
	i, ok := in.pick(len(p.Legal))
	if !ok {
		return false
	}
	la := p.Legal[i]
	addr := p.Address
	// Leaving and sitting out end a player's session; taken at their raw share
	// of the legal actions, tables churn and never get deep into play.
	if la.Action == ActLeave || la.Action == ActSitOut {
		b, ok := in.next()
		if !ok {
			return false
		}
		if b >= 24 {
			return true
		}
	}
	switch la.Action {
	case ActNewHand:
		return s.newHand(in, addr)
	case ActJoin:
		return s.join(in)
	}
	amount := new(big.Int)
	if la.Min != nil && la.Max != nil && la.Max.Cmp(la.Min) >= 0 {
		// Bias to the edges (min call / min raise / all-in), where the
		// short-stack and side-pot paths are, with the middle in between.
		b, ok := in.next()
		if !ok {
			return false
		}
		amount.Set(la.Min)
		switch {
		case b < 96:
		case b < 160:
			amount.Set(la.Max)
		default:
			span := new(big.Int).Sub(la.Max, la.Min)
			span.Mul(span, big.NewInt(int64(b-160)))
			span.Div(span, big.NewInt(95))
			amount.Add(amount, span)
		}
	}
	s.apply(Action{Player: addr, Action: la.Action, Amount: amount})
	return true
}

func (s *session) join(in *input) bool {
	a, ok := in.pick(len(s.addrs))
	if !ok {
		return false
	}
	seat, ok := in.pick(s.cfg.MaxPlayers)
	if !ok {
		return false
	}
	buyIn := new(big.Int).Set(s.cfg.MinBuyIn)
	if s.cfg.Format == Cash {
		b, ok := in.next()
		if !ok {
			return false
		}
		span := new(big.Int).Sub(s.cfg.MaxBuyIn, s.cfg.MinBuyIn)
		span.Mul(span, big.NewInt(int64(b)))
		span.Div(span, big.NewInt(255))
		buyIn.Add(buyIn, span)
	}
	s.apply(Action{Player: s.addrs[a], Action: ActJoin, Amount: buyIn, Seat: seat + 1})
	return true
}

func (s *session) newHand(in *input, addr string) bool {
	b, ok := in.next()
	if !ok {
		return false
	}
	s.hand++
	s.apply(Action{Player: addr, Action: ActNewHand, Deck: Deck(s.hand<<8 | uint64(b))})
	return true
}

// Tail is how many trailing script lines [Result.Report] prints.
const Tail = 40

// Report is a readable account of a session: the violation, the end of the
// script that led to it, and the final table.
func (r *Result) Report() string {
	var b strings.Builder
	if r.Violation != nil {
		fmt.Fprintf(&b, "invariant %q violated: %s\n\n", r.Violation.Invariant, r.Violation.Detail)
	}
	from := max(0, len(r.Script)-Tail)
	fmt.Fprintf(&b, "session script (last %d of %d lines):\n", len(r.Script)-from, len(r.Script))
	for i := from; i < len(r.Script); i++ {
		fmt.Fprintf(&b, "  %3d  %s\n", i+1, r.Script[i])
	}
	b.WriteString("\nstate:\n")
	b.WriteString(DescribeState(r.Final))
	return b.String()
}

// DescribeState renders a table state, one line per seat.
func DescribeState(st State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  hand=%d round=%s nextToAct=%d dealer=%d sb=%d bb=%d pot=%s\n",
		st.HandNumber, st.Round, st.NextToAct, st.Dealer, st.SmallBlind, st.BigBlind, amountString(st.Pot))
	for _, p := range st.Players {
		legal := make([]string, 0, len(p.Legal))
		for _, la := range p.Legal {
			legal = append(legal, la.Action)
		}
		fmt.Fprintf(&b, "  seat %d: %-22s stack=%-6s legal=%v\n", p.Seat, p.Status, amountString(p.Stack), legal)
	}
	return b.String()
}

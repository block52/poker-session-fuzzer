package sessionfuzz

import (
	"fmt"
	"strings"
)

// Scene renders the session's accepted actions as sentences for
// github.com/block52/poker-scene-dsl, so a fuzz failure becomes a readable,
// replayable test. Players are named by seat ("seat 3 calls"); a joining
// player by address. Amounts are the ones the engine was sent: raises render
// as "raises by N" and bets as "bets N".
func (r *Result) Scene() []string {
	out := make([]string, 0, len(r.Actions))
	for _, a := range r.Actions {
		out = append(out, sceneSentence(a))
	}
	return out
}

var sceneVerbs = map[string]string{
	ActPostSmallBlind: "posts small blind", ActPostBigBlind: "posts big blind",
	ActFold: "folds", ActCheck: "checks", ActCall: "calls", ActAllIn: "goes all-in",
	ActShow: "shows", ActMuck: "mucks", ActSitOut: "sits out", ActSitIn: "sits in",
	ActSitInAndWait: "sits in and waits", ActLeave: "leaves",
}

func sceneSentence(a Recorded) string {
	who := fmt.Sprintf("seat %d", a.Seat)
	switch a.Action.Action {
	case ActJoin:
		return fmt.Sprintf("%s joins seat %d with %s", a.Player, a.Action.Seat, amountString(a.Amount))
	case ActDeal:
		return "deal"
	case ActNewHand:
		if len(a.Deck) == 0 {
			return "new hand"
		}
		return "new hand with deck " + strings.Join(a.Deck, "-")
	case ActTimeout:
		return "time runs out"
	case ActBet:
		return fmt.Sprintf("%s bets %s", who, amountString(a.Amount))
	case ActRaise:
		return fmt.Sprintf("%s raises by %s", who, amountString(a.Amount))
	case ActTopUp:
		return fmt.Sprintf("%s tops up %s", who, amountString(a.Amount))
	}
	if verb, ok := sceneVerbs[a.Action.Action]; ok {
		return who + " " + verb
	}
	return fmt.Sprintf("%s %s %s", who, a.Action.Action, amountString(a.Amount))
}

# poker-session-fuzzer

Fuzz a poker engine across **whole multi-hand sessions** and check, after every action, the invariants that keep a real table alive.

Hand-history corpora (PHH, site histories, solver output) test one hand at a time. Every hand starts from known seats, button and blinds, so the step *between* hands never runs. That step is where tables freeze in production: busts, button and blind movement, sit-in and sit-out, joins and leaves, tournament eliminations. This package plays sessions where all of that happens, mostly choosing among each seat's own legal actions, and needs no oracle. It checks rules that must hold whatever the right answer is.

## Invariants

| Invariant | Checks |
|---|---|
| `no-panic` | The engine never panics, on any action, legal or not. |
| `status-stack` | A busted player has (almost) no chips. An all-in player has none while the hand is live. |
| `chip-conservation` | Stacks + pot (+ queued top-ups) change only when a player joins, tops up or leaves. |
| `liveness` | During a hand, and before the deal once two or more players are active, the seat on the clock can move play on. |
| `positions` | In a dealt hand, the big blind is a player in the hand, and the small and big blind are different seats. |
| `stall-at-end` | Between hands, when enough players with chips want to play, someone is offered `new-hand`. |
| `end-nextToAct` | Between hands, `new-hand` is offered to the seat on the clock. |

## Plug in an engine

Implement two small interfaces. The vocabulary (actions, statuses, rounds) is plain poker English: map it to and from your engine's types.

```go
type Table interface {
	State() sessionfuzz.State      // seats, stacks, statuses, legal actions, positions
	Apply(sessionfuzz.Action) error // reject with an error; leave the table unchanged
}

type Engine interface {
	NewTable(sessionfuzz.Config) (Table, error)
}
```

Then, in a `_test.go` file:

```go
var engine = sessionfuzz.EngineFunc(func(c sessionfuzz.Config) (sessionfuzz.Table, error) {
	return newMyTable(c) // your adapter
})

var opts = sessionfuzz.Options{}

func TestSessionSweep(t *testing.T)  { sessionfuzz.Sweep(t, engine, opts) }  // 200 sessions, plain go test
func TestSessionReplay(t *testing.T) { sessionfuzz.Replay(t, engine, opts) } // SESSIONFUZZ_SEED=n
func FuzzSession(f *testing.F)       { sessionfuzz.Fuzz(f, engine, opts) }   // go test -fuzz
```

```sh
go test -run TestSessionSweep ./...                        # deterministic, for CI
SESSIONFUZZ_SEED=49 go test -run TestSessionReplay -v ./... # print one session in full
go test -run '^$' -fuzz FuzzSession -fuzztime 10m ./...     # coverage-guided
```

A failure prints the session script that led to it. Accepted actions that weren't in the player's legal actions are marked, and the final table is shown:

```
invariant "stall-at-end" violated: cash, 6 seats: players with chips want to play [folded sitting-in] but nobody is offered new-hand

session script (last 40 of 22 lines):
   17  seat 1: post-big-blind 100
   ...
   22  seat 2: leave
state:
  hand=1 round=end nextToAct=1 dealer=2 sb=5 bb=6 pot=200
  seat 1: folded                 stack=1427   legal=[sit-out top-up leave]
  seat 5: sitting-in             stack=1682   legal=[sit-out top-up leave]
```

## From failure to test

Every failure is minimised and printed as a [poker-scene-dsl](https://github.com/block52/poker-scene-dsl) scenario, ready to paste into a test.

`Minimize` replays subsets of the session on fresh tables, dropping actions while the same kind of violation still happens. On our engine, a 113-action stall comes down to 12 sentences:

```go
sc.Act(
	"player02 joins seat 2 with 1527",
	"seat 2 sits in and waits",
	"player06 joins seat 1 with 1527",
	"seat 1 sits in",
	"player05 joins seat 5 with 1682",
	"seat 5 sits in",
	"time runs out",
	"seat 1 posts big blind",
	"deal",
	"seat 2 calls",
	"seat 1 folds",
	"seat 2 leaves",
)
```

`Sweep` prints the minimised scenario for each new kind of failure. `Replay` prints the whole session, the minimised failure, and a minimised scenario for every known issue the session hit. The fuzzer and the DSL tick the same clock, so a scenario replays its session exactly.

You can call it directly too: `sessionfuzz.Minimize(engine, result, opts, nil)` then `.Scene()`. Pass a known issue's violation from `result.KnownFirst` as the last argument to minimise that one.

## Options

- **`Rules`** hold where engines legitimately differ:
  - `BustBelow`: e.g. a cash stack smaller than the small blind counts as busted.
  - `NewHandFloor`
  - `Progress`: which actions move play on.
  - `Reject`: your host's own gates, so the fuzzer only reaches states your host can. For example, a chain that never forwards a tournament leave.
- **`Known`** lists open bugs as `{ID, regexp}`. Matching violations are counted, and play continues, so CI stays green while the bug is open and fails on anything new.
- **`Config`** picks the table from the session's first input byte. The default plays cash and sit-and-go at 2, 3, 4, 6 and 9 seats with short stacks, so busts and blind rises happen quickly.

## License

MIT

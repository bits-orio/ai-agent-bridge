// Sessions: the short shared transcript follow-up questions see
// (docs/design/phase3-spec.md, part 1). A session holds who asked what and
// the answer exactly as the game showed it, never a tool result, so its
// cost is bounded by the byte cap and nothing the model reads is half-cut.
//
// Keyed by scope plus an optional name, so a private team's session and
// the global one never meet, and "#iron" is one session for everyone in the
// same scope.

package agent

import (
	"sort"
	"sync"
	"time"
)

// SessionCaps bounds every session. Zero values take the defaults below.
type SessionCaps struct {
	Idle         time.Duration // an unnamed session ends after this long without a question
	NamedIdle    time.Duration // a named one waits longer: it was asked for on purpose
	ClarifyIdle  time.Duration // widens Idle (or NamedIdle, whichever is longer) while a session awaits the player's reply to an ask-back (docs/design/phase4-spec.md section 12)
	MaxExchanges int           // oldest exchanges drop past this count
	MaxBytes     int           // and past this many bytes of question and answer text
}

const (
	DefaultSessionIdle         = 3 * time.Minute
	DefaultNamedSessionIdle    = 30 * time.Minute
	DefaultClarifyIdle         = 10 * time.Minute
	DefaultSessionMaxExchanges = 10
	DefaultSessionMaxBytes     = 8000
)

func (c SessionCaps) withDefaults() SessionCaps {
	if c.Idle <= 0 {
		c.Idle = DefaultSessionIdle
	}
	if c.NamedIdle <= 0 {
		c.NamedIdle = DefaultNamedSessionIdle
	}
	if c.ClarifyIdle <= 0 {
		c.ClarifyIdle = DefaultClarifyIdle
	}
	if c.MaxExchanges <= 0 {
		c.MaxExchanges = DefaultSessionMaxExchanges
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = DefaultSessionMaxBytes
	}
	return c
}

// Exchange is one question and the answer the game showed for it.
type Exchange struct {
	Asker    string
	Question string
	Answer   string
	At       time.Time
}

func (e Exchange) size() int { return len(e.Asker) + len(e.Question) + len(e.Answer) }

type session struct {
	scope     string
	name      string
	exchanges []Exchange
	startedAt time.Time
	lastAt    time.Time
	// awaitingReply is true from the moment this session's last answer was
	// an ask-back (a notice at level confirmation, docs/design/phase4-spec.md
	// section 12) until the next question lands in it, whatever that
	// question turns out to ask. idleFor reads it to widen the session's
	// idle window to ClarifyIdle in the meantime.
	awaitingReply bool
}

// SessionInfo is one row of the `sessions` listing.
type SessionInfo struct {
	Name      string // "" for the scope's default session
	Exchanges int
	LastAsker string
	Idle      time.Duration
}

type sessions struct {
	mu    sync.Mutex
	caps  SessionCaps
	byKey map[string]*session
}

func newSessions(caps SessionCaps) *sessions {
	return &sessions{caps: caps.withDefaults(), byKey: map[string]*session{}}
}

// sessionKey joins a scope and a name the way the spec writes them:
// "global", "global#iron", "team-3#iron".
func sessionKey(scope, name string) string {
	if name == "" {
		return scope
	}
	return scope + "#" + name
}

// idleFor is how long scope/name may sit without a question before the
// sweep drops it. A session currently awaiting the player's reply to an
// ask-back (Decision 7) gets ClarifyIdle in place of its ordinary window; a
// named session, asked for on purpose, keeps whichever of ClarifyIdle and
// its own NamedIdle is longer, since NamedIdle was already a deliberate
// choice and an ask-back must only ever widen a window, never shrink one.
func (s *sessions) idleFor(name string, awaitingReply bool) time.Duration {
	if name == "" {
		if awaitingReply {
			return s.caps.ClarifyIdle
		}
		return s.caps.Idle
	}
	if awaitingReply && s.caps.ClarifyIdle > s.caps.NamedIdle {
		return s.caps.ClarifyIdle
	}
	return s.caps.NamedIdle
}

// open returns the live session's exchanges for scope and name, starting a
// new one when there is none, when the old one has idled out, or when
// fresh asks for one. The second result says whether it was started just
// now. The third says whether the session was awaiting the player's reply
// to an ask-back (Decision 7) when this question landed: whatever this
// question turns out to ask, its arrival resolves that wait, so the flag is
// cleared here, once, before the question is ever answered.
func (s *sessions) open(scope, name string, now time.Time, fresh bool) ([]Exchange, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(now)
	key := sessionKey(scope, name)
	live := s.byKey[key]
	if live != nil && !fresh {
		wasAwaiting := live.awaitingReply
		live.awaitingReply = false
		out := make([]Exchange, len(live.exchanges))
		copy(out, live.exchanges)
		return out, false, wasAwaiting
	}
	s.byKey[key] = &session{scope: scope, name: name, startedAt: now, lastAt: now}
	s.trim()
	return nil, true, false
}

// maxLiveSessions bounds the map: a player inventing a new #name on every
// question would otherwise grow it until the idle sweep caught up.
const maxLiveSessions = 200

// trim drops the longest-idle sessions past the cap. Callers hold the lock.
func (s *sessions) trim() {
	for len(s.byKey) > maxLiveSessions {
		var oldestKey string
		var oldest time.Time
		for key, live := range s.byKey {
			if oldestKey == "" || live.lastAt.Before(oldest) {
				oldestKey, oldest = key, live.lastAt
			}
		}
		delete(s.byKey, oldestKey)
	}
}

// end drops the session for scope and name, so the next question starts a
// new one. Reports whether there was one to drop.
func (s *sessions) end(scope, name string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(now)
	key := sessionKey(scope, name)
	_, had := s.byKey[key]
	delete(s.byKey, key)
	return had
}

// record appends one exchange to the live session, dropping the oldest
// exchanges whole until the count and byte caps hold, and sets whether this
// exchange leaves the session awaiting the player's reply to an ask-back
// (Decision 7). awaitingReply is the caller's own answer to "was this
// exchange's answer itself an ask-back": setting it unconditionally, true
// or false, is what makes a second ask-back in the same session renew the
// window (open already cleared the flag before this question ran, so a
// true here is this question's own doing, not a leftover) and what makes an
// ordinary follow-up let the window fall back to normal.
func (s *sessions) record(scope, name string, e Exchange, awaitingReply bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(scope, name)
	live := s.byKey[key]
	if live == nil {
		live = &session{scope: scope, name: name, startedAt: e.At}
		s.byKey[key] = live
	}
	live.exchanges = append(live.exchanges, e)
	live.lastAt = e.At
	live.awaitingReply = awaitingReply
	total := 0
	for _, x := range live.exchanges {
		total += x.size()
	}
	for len(live.exchanges) > 1 && (len(live.exchanges) > s.caps.MaxExchanges || total > s.caps.MaxBytes) {
		total -= live.exchanges[0].size()
		live.exchanges = live.exchanges[1:]
	}
	if len(live.exchanges) > s.caps.MaxExchanges {
		live.exchanges = live.exchanges[len(live.exchanges)-s.caps.MaxExchanges:]
	}
}

// list is every live session in one scope, the default first, then named
// ones by name.
func (s *sessions) list(scope string, now time.Time) []SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(now)
	var out []SessionInfo
	for _, live := range s.byKey {
		if live.scope != scope {
			continue
		}
		info := SessionInfo{Name: live.name, Exchanges: len(live.exchanges), Idle: now.Sub(live.lastAt)}
		if n := len(live.exchanges); n > 0 {
			info.LastAsker = live.exchanges[n-1].Asker
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == "") != (out[j].Name == "") {
			return out[i].Name == ""
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// sweep drops idled-out sessions. Callers hold the lock.
func (s *sessions) sweep(now time.Time) {
	for key, live := range s.byKey {
		if now.Sub(live.lastAt) >= s.idleFor(live.name, live.awaitingReply) {
			delete(s.byKey, key)
		}
	}
}

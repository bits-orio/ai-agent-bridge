// Package chatscope carries a question's scope (CONTEXT.md "Scope") from the
// agent down to every history read, so the history store can tell which rows
// the answer may be built from. It sits on its own so the agent and the
// history store can both import it without importing each other.
//
// Reading is fail-closed. A context that carries no scope reads as a public
// question, and a public question sees public rows only.
package chatscope

import "context"

// GlobalKey is the key of the public scope: what every question gets when
// no scope provider answers, and what a provider says for its open channel.
const GlobalKey = "global"

// Scope is who will hear an answer: the key a scope provider resolved and
// whether the answer stays inside that key's audience. The zero value is a
// public question.
type Scope struct {
	Key     string
	Private bool
}

// PrivateKey is the key whose private rows a question in this scope may
// read, and false when it may read none. Only a private scope with a key of
// its own qualifies. A provider that answered private with no key, or with
// the global key, gets the public view: otherwise every keyless private
// question would share one bucket and read each other's rows.
func (s Scope) PrivateKey() (string, bool) {
	if !s.Private || s.Key == "" || s.Key == GlobalKey {
		return "", false
	}
	return s.Key, true
}

type ctxKey struct{}

// With marks ctx with the scope of the question it serves. Set it before the
// first read: a read made on a context without it sees public rows only.
func With(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// From is the scope ctx carries, or the public scope when it carries none.
func From(ctx context.Context) Scope {
	s, _ := ctx.Value(ctxKey{}).(Scope)
	return s
}

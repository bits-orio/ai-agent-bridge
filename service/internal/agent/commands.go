// The two lines that never reach the model: "sessions" and a bare "new".

package agent

import (
	"fmt"
	"time"
)

const (
	noSessionsNotice    = "No sessions in your scope right now. Ask something and one starts."
	emptyQuestionNotice = "Ask a question after the session words, for example: #iron what is our plate rate"
)

func (a *Agent) command(q Question, req Request, mark SessionMark, now time.Time) Result {
	switch req.Command {
	case CommandSessions:
		return Result{Artifact: sessionsArtifact(a.sess.list(q.scope(), now)), Session: mark}
	case CommandNew:
		a.sess.end(q.scope(), req.Name, now)
		mark.Fresh = true
		label := "session"
		if req.Name != "" {
			label = "session #" + req.Name
		}
		art := Notice(LevelConfirmation, "Started a new "+label+". The next question begins it.")
		art.Session = &mark
		return Result{Artifact: art, Session: mark}
	default:
		return Result{Artifact: refusal(emptyQuestionNotice), Session: mark}
	}
}

func sessionsArtifact(rows []SessionInfo) Artifact {
	if len(rows) == 0 {
		return Notice(LevelConfirmation, noSessionsNotice)
	}
	items := make([]string, 0, len(rows))
	for _, r := range rows {
		name := "(default)"
		if r.Name != "" {
			name = "#" + r.Name
		}
		line := fmt.Sprintf("%s: %d exchange%s, idle %s", name, r.Exchanges, plural(r.Exchanges), idleText(r.Idle))
		if r.LastAsker != "" {
			line += ", last from " + r.LastAsker
		}
		items = append(items, line)
	}
	return Artifact{Shape: ShapeList, Title: "Sessions", Items: items}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func idleText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

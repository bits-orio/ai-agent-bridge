// Read/ReadRange are what `aab stats` runs on: pure arithmetic over
// JSONL already on disk, nothing that calls the model or RCON (section 4).
// A truncated final line (a process killed mid-write leaves exactly one)
// and a malformed line anywhere else are both skipped rather than failing
// the whole read, and both are counted: silently dropping data is the one
// thing this package exists to prevent.

package ledger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Records is one directory's (or date range's) worth of ledger lines,
// split by kind, plus how many lines could not be read back as either
// kind.
type Records struct {
	Questions []QuestionRecord
	Rounds    []RoundRecord
	Skipped   int
}

var ledgerFileRE = regexp.MustCompile(`^ledger-(\d{4}-\d{2}-\d{2})\.jsonl$`)

// Read reads every ledger file directly under dir. A dir that does not
// exist yet (a server that enabled the ledger but has answered nothing)
// reads as empty, not an error.
func Read(dir string) (Records, error) {
	return ReadRange(dir, time.Time{}, time.Time{})
}

// ReadRange reads only the ledger files whose UTC calendar day falls in
// [from, to]; a zero from or to leaves that end of the range open. Files
// are read in calendar order, which is lexical order on their zero-padded
// names.
func ReadRange(dir string, from, to time.Time) (Records, error) {
	files, err := filesInRange(dir, from, to)
	if err != nil {
		return Records{}, err
	}

	var out Records
	for _, f := range files {
		if err := readFile(filepath.Join(dir, f.name), &out); err != nil {
			return Records{}, fmt.Errorf("ledger: read %s: %w", f.name, err)
		}
	}
	return out, nil
}

// Days lists the calendar days dir holds a ledger-YYYY-MM-DD.jsonl file
// for, filtered to [from, to] the same way ReadRange filters which files
// it reads (a zero from or to leaves that end open), sorted oldest first.
// It runs the same directory scan Read and ReadRange do, without decoding
// a single line, so a caller that only wants the day list (aab stats'
// per-day walk) does not pay to parse records it is about to re-read
// itself. A dir that does not exist yet reads as no days, not an error,
// the same as Read.
func Days(dir string, from, to time.Time) ([]time.Time, error) {
	files, err := filesInRange(dir, from, to)
	if err != nil {
		return nil, err
	}
	days := make([]time.Time, len(files))
	for i, f := range files {
		days[i] = f.day
	}
	return days, nil
}

// dayFile is one ledger file matched against dayFormat, its parsed day kept
// alongside its name so Days can hand back the day without re-parsing what
// filesInRange already parsed once.
type dayFile struct {
	name string
	day  time.Time
}

// filesInRange is the one directory scan Read, ReadRange and Days all
// share: list dir, keep only names matching ledger-YYYY-MM-DD.jsonl, parse
// each into a real calendar date, drop anything outside [from, to], and
// return the rest in calendar order (lexical order on the zero-padded
// name, which is the same order). A dir that does not exist yet returns no
// files and no error.
func filesInRange(dir string, from, to time.Time) ([]dayFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ledger: read %s: %w", dir, err)
	}

	var out []dayFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := ledgerFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		day, err := time.Parse(dayFormat, m[1])
		if err != nil {
			continue // matched the filename shape but not a real calendar date
		}
		if !from.IsZero() && day.Before(truncateToDay(from)) {
			continue
		}
		if !to.IsZero() && day.After(truncateToDay(to)) {
			continue
		}
		out = append(out, dayFile{name: e.Name(), day: day})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func truncateToDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// readFile appends one file's records into out. A line that fails to
// decode as either record kind is skipped and counted, never fails the
// read; that covers a malformed line anywhere in the file and a truncated
// one at the end alike, since ReadString returns a final unterminated line
// alongside io.EOF instead of dropping it.
func readFile(path string, out *Records) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if !decodeLine(trimmed, out) {
				out.Skipped++
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// decodeLine classifies one line by which record-only key it carries
// ("tool_calls" for a round, "shape" for a question, both spec fields no
// other record kind has) and decodes it as that kind. It reports whether
// the line decoded cleanly.
func decodeLine(line string, out *Records) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &probe); err != nil {
		return false
	}
	switch {
	case probe["tool_calls"] != nil:
		var rec RoundRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return false
		}
		out.Rounds = append(out.Rounds, rec)
	case probe["shape"] != nil:
		var rec QuestionRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return false
		}
		out.Questions = append(out.Questions, rec)
	default:
		return false
	}
	return true
}

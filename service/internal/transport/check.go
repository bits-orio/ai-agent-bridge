// Preflight for the events file: can the service reach it, and is it there?
// Used by `aab check` before a first run against a new server, where the
// path over SFTP is the thing most likely to be wrong.

package transport

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
)

// FileReport is what a preflight found out about the events file.
type FileReport struct {
	Reachable bool     // the host (or the local disk) answered
	Exists    bool     // the file itself is there
	Size      int64    // its size when it is
	Siblings  []string // when it is not: what the parent directory holds, to spot a wrong path
	Err       error    // why the host could not be reached, when it could not
}

// CheckLocal reports on a local events file.
func CheckLocal(p string) FileReport {
	fi, err := os.Stat(p)
	switch {
	case err == nil:
		return FileReport{Reachable: true, Exists: true, Size: fi.Size()}
	case errors.Is(err, os.ErrNotExist):
		return FileReport{Reachable: true, Siblings: localSiblings(path.Dir(p))}
	default:
		return FileReport{Err: err}
	}
}

func localSiblings(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return capped(names)
}

// CheckSFTP connects, authenticates and stats the events file on the host.
// A file that is not there yet is not an error: the mod creates it when the
// first event happens. The parent directory's entries come back in that
// case, so a wrong path shows itself.
func CheckSFTP(cfg SFTPConfig, p string) FileReport {
	r := newSFTPReader(cfg, p)
	defer r.Close()
	if err := r.ensure(); err != nil {
		return FileReport{Err: err}
	}
	fi, err := r.sftp.Stat(p)
	if err == nil {
		return FileReport{Reachable: true, Exists: true, Size: fi.Size()}
	}
	if !errors.Is(err, os.ErrNotExist) {
		return FileReport{Reachable: true, Err: fmt.Errorf("stat %s: %w", p, err)}
	}
	report := FileReport{Reachable: true}
	dir := path.Dir(p)
	if dir == "." || dir == "" {
		dir = "."
	}
	if entries, err := r.sftp.ReadDir(dir); err == nil {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		report.Siblings = capped(names)
	} else if entries, err := r.sftp.ReadDir("."); err == nil {
		// The parent is not there either: show the login root instead.
		var names []string
		for _, e := range entries {
			names = append(names, "(root) "+e.Name())
		}
		report.Siblings = capped(names)
	}
	return report
}

func capped(names []string) []string {
	sort.Strings(names)
	if len(names) > 20 {
		names = append(names[:20], "...")
	}
	return names
}

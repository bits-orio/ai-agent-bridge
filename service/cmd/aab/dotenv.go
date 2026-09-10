// Copied from open-discord-bridge/bridge/cmd/bridge/dotenv.go, unchanged beyond this
// header and the "bridge"/"ODB_CONFIG" wording below (now "service"/"AAB_CONFIG").

package main

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// loadDotEnv loads KEY=VALUE pairs from a .env file in dir into the process environment,
// but only for keys that are not already set, so a real exported environment (e.g. a
// hosting panel's injected variables) always wins. The service reads its secrets from the
// environment, and the setup wizard writes a .env next to aab.yaml; loading it here lets
// `aab -config aab.yaml` work without the caller having to `source .env` first.
// A missing or unreadable .env is not an error.
func loadDotEnv(dir string) {
	path := filepath.Join(dir, ".env")
	f, err := os.Open(path)
	if err != nil {
		return // no .env, fine: the env may be populated directly
	}
	defer f.Close()

	loaded := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, set := os.LookupEnv(key); set {
			continue // already in the environment, do not override
		}
		if err := os.Setenv(key, unquote(strings.TrimSpace(val))); err == nil {
			loaded++
		}
	}
	if loaded > 0 {
		log.Printf("config: loaded %d secret(s)/var(s) from %s", loaded, path)
	}
}

// unquote strips a single pair of matching surrounding quotes, if present. Values are
// otherwise taken literally (no inline-comment stripping: a '#' may be part of a secret).
func unquote(s string) string {
	if len(s) >= 2 {
		if c := s[0]; (c == '"' || c == '\'') && s[len(s)-1] == c {
			return s[1 : len(s)-1]
		}
	}
	return s
}

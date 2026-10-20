package smoke

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/itsKontra/ddash/internal/scrub"
)

// ReadEnv reads smoke.env: one NAME=value per line, an optional "export "
// in front, the value optionally in single or double quotes (taken as is,
// no escapes). Blank lines and lines starting with # are skipped.
func ReadEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	env := map[string]string{}
	lines := bufio.NewScanner(f)
	lines.Buffer(nil, 1<<20)
	for n := 1; lines.Scan(); n++ {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, " \t\"'") {
			return nil, fmt.Errorf("%s line %d: want NAME=value", path, n)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		env[name] = value
	}
	return env, lines.Err()
}

// Scrubber builds the recording's Scrubber from smoke.env (SCRUB_KEY,
// SCRUB_SUBNETS, SCRUB_DOMAINS) and the scrub.map at mapPath, which must
// exist (it may be empty).
func Scrubber(env map[string]string, mapPath string) (*scrub.Scrubber, error) {
	if env["SCRUB_KEY"] == "" {
		return nil, errors.New("recording needs SCRUB_KEY in smoke.env: the key MACs and other addresses are hashed with")
	}
	subnets, err := scrub.ParseSubnets(env["SCRUB_SUBNETS"])
	if err != nil {
		return nil, err
	}
	f, err := os.Open(mapPath)
	if err != nil {
		return nil, fmt.Errorf("recording needs scrub.map (gitignored; it may be empty): %w", err)
	}
	defer f.Close()
	literals, err := scrub.ParseMap(f)
	if err != nil {
		return nil, err
	}
	return scrub.New(scrub.Config{
		Key:      []byte(env["SCRUB_KEY"]),
		Subnets:  subnets,
		Domains:  strings.FieldsFunc(env["SCRUB_DOMAINS"], func(r rune) bool { return r == ',' || r == ' ' }),
		Literals: literals,
	})
}

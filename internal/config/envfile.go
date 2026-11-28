package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DefaultEnvFile is the local secrets file loaded beside plat5.local.yml when present.
const DefaultEnvFile = ".env.local"

// LoadEnvFile parses a .env file (KEY=VALUE lines, comments, optional export
// prefix, single- or double-quoted values).
func LoadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(rest)
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNum)
		}
		key = strings.TrimSpace(key)
		if !isValidEnvKey(key) {
			return nil, fmt.Errorf("%s:%d: invalid env key %q", path, lineNum, key)
		}
		val, err := parseEnvValue(raw)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNum, err)
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func isValidEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		letter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
		digit := i > 0 && c >= '0' && c <= '9'
		if !letter && !digit {
			return false
		}
	}
	return true
}

func parseEnvValue(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if s[0] == '\'' {
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return s[1 : 1+end], nil
	}
	if s[0] == '"' {
		var b strings.Builder
		escaped := false
		for i := 1; i < len(s); i++ {
			c := s[i]
			if escaped {
				switch c {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				case '"', '\\':
					b.WriteByte(c)
				default:
					b.WriteByte('\\')
					b.WriteByte(c)
				}
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				return b.String(), nil
			}
			b.WriteByte(c)
		}
		return "", fmt.Errorf("unterminated double-quoted value")
	}
	// Unquoted: strip trailing inline comment preceded by whitespace.
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
			return strings.TrimRight(s[:i], " \t"), nil
		}
	}
	return s, nil
}

// expandValue resolves ${VAR} and ${VAR:-default} expressions in s using process
// env first, then fileEnv. Unresolved ${VAR} without a default returns an error.
func expandValue(field, s string, fileEnv map[string]string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		idx := strings.Index(s[i:], "${")
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+idx])
		start := i + idx + 2
		end := strings.IndexByte(s[start:], '}')
		if end < 0 {
			return "", fmt.Errorf("%s: unterminated ${...} expression in %q", field, s)
		}
		expr := s[start : start+end]
		i = start + end + 1

		name, def, hasDefault := strings.Cut(expr, ":-")
		name = strings.TrimSpace(name)
		if !isValidEnvKey(name) {
			return "", fmt.Errorf("%s: invalid env var %q in %q", field, name, s)
		}
		if v, ok := lookupEnv(name, fileEnv); ok && (v != "" || !hasDefault) {
			b.WriteString(v)
			continue
		}
		if hasDefault {
			b.WriteString(def)
			continue
		}
		return "", fmt.Errorf("%s: unresolved env var ${%s} (set it in %s or your environment)", field, name, DefaultEnvFile)
	}
	return b.String(), nil
}

func lookupEnv(name string, fileEnv map[string]string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	if fileEnv != nil {
		if v, ok := fileEnv[name]; ok {
			return v, true
		}
	}
	return "", false
}

func expandSlice(field string, in []string, fileEnv map[string]string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		v, err := expandValue(field, s, fileEnv)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

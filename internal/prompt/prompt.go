package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Interactive reports whether stdin is a terminal.
func Interactive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ExpandPath trims space and expands a leading ~/.
func ExpandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// String asks for a line. Empty input returns defaultValue.
func String(in io.Reader, out io.Writer, label, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		if defaultValue != "" {
			return defaultValue, nil
		}
		return "", io.EOF
	}
	line := strings.TrimSpace(scanner.Text())
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

// YesNo asks y/n. Empty input returns defaultYes.
func YesNo(in io.Reader, out io.Writer, label string, defaultYes bool) (bool, error) {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	fmt.Fprintf(out, "%s [%s]: ", label, hint)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return defaultYes, nil
	}
	line := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if line == "" {
		return defaultYes, nil
	}
	switch line {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("please answer y or n")
	}
}

// reader holds a single scanner so multi-prompt flows share stdin lines.
type reader struct {
	sc *bufio.Scanner
}

func newReader(in io.Reader) *reader {
	return &reader{sc: bufio.NewScanner(in)}
}

func (r *reader) line(out io.Writer, label, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return "", err
		}
		if defaultValue != "" {
			return defaultValue, nil
		}
		return "", io.EOF
	}
	line := strings.TrimSpace(r.sc.Text())
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

// Session reads multiple prompts from one input stream.
type Session struct {
	in  *reader
	out io.Writer
}

// NewSession creates a prompt session over in/out.
func NewSession(in io.Reader, out io.Writer) *Session {
	return &Session{in: newReader(in), out: out}
}

// String prompts for a line.
func (s *Session) String(label, defaultValue string) (string, error) {
	return s.in.line(s.out, label, defaultValue)
}

// Choice prints a numbered list and returns the selected index.
// defaultIdx is used on empty input; must be in [0, len(options)).
// Options are shown as "  1) …" (1-based). User may enter the number or exact label.
func (s *Session) Choice(label string, options []string, defaultIdx int) (int, error) {
	if len(options) == 0 {
		return 0, fmt.Errorf("no options")
	}
	if defaultIdx < 0 || defaultIdx >= len(options) {
		defaultIdx = 0
	}
	for i, opt := range options {
		fmt.Fprintf(s.out, "  %d) %s\n", i+1, opt)
	}
	defDisplay := fmt.Sprintf("%d", defaultIdx+1)
	for {
		line, err := s.in.line(s.out, label, defDisplay)
		if err != nil {
			if err == io.EOF {
				return defaultIdx, nil
			}
			return 0, err
		}
		if line == "" || line == defDisplay {
			return defaultIdx, nil
		}
		// numeric (1-based)
		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		// exact label, or name before " — " (template menus)
		for i, opt := range options {
			if opt == line {
				return i, nil
			}
			if name, _, ok := strings.Cut(opt, " — "); ok && name == line {
				return i, nil
			}
		}
		fmt.Fprintf(s.out, "  enter 1–%d\n", len(options))
	}
}

// YesNo prompts y/n, re-asking on invalid input until EOF.
func (s *Session) YesNo(label string, defaultYes bool) (bool, error) {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	for {
		// label shows hint as the bracket default display
		line, err := s.in.line(s.out, label, hint)
		if err != nil {
			if err == io.EOF {
				return defaultYes, nil
			}
			return false, err
		}
		if line == "" || line == hint {
			return defaultYes, nil
		}
		switch strings.ToLower(line) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(s.out, "  please answer y or n")
		}
	}
}

// Path prompts for a path until validate passes.
func (s *Session) Path(label, defaultValue string, allowEmpty bool, validate func(string) error) (string, error) {
	for {
		raw, err := s.in.line(s.out, label, defaultValue)
		if err != nil {
			if err == io.EOF {
				if raw != "" {
					// fall through with default
				} else if allowEmpty {
					return "", nil
				} else {
					return "", fmt.Errorf("%s: required", label)
				}
			} else {
				return "", err
			}
		}
		if raw == "" {
			if allowEmpty {
				return "", nil
			}
			fmt.Fprintln(s.out, "  path is required")
			continue
		}
		expanded := ExpandPath(raw)
		abs, err := filepath.Abs(expanded)
		if err != nil {
			fmt.Fprintf(s.out, "  %v\n", err)
			continue
		}
		if validate != nil {
			if err := validate(abs); err != nil {
				fmt.Fprintf(s.out, "  %v\n", err)
				continue
			}
		}
		return abs, nil
	}
}

// Path is a one-shot path prompt (own scanner). Prefer Session for multi-step flows.
func Path(in io.Reader, out io.Writer, label, defaultValue string, allowEmpty bool, validate func(string) error) (string, error) {
	return NewSession(in, out).Path(label, defaultValue, allowEmpty, validate)
}

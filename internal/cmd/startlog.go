package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/plat5dev/cli/internal/compose"
)

// startLog is where plat5 start sends docker compose's output: the terminal
// with --verbose, otherwise start.log in the stack dir, shown on failure.
type startLog struct {
	w    io.Writer // nil: the terminal
	file *os.File
}

func newStartLog(dir string, verbose bool) (*startLog, error) {
	if verbose {
		return &startLog{}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(dir, "start.log"))
	if err != nil {
		return nil, err
	}
	return &startLog{w: f, file: f}, nil
}

func (l *startLog) Close() {
	if l.file != nil {
		_ = l.file.Close()
	}
}

// failed is err with the log's last lines, so a failure is visible without --verbose.
func (l *startLog) failed(err error, logsCmd string) error {
	if l.file == nil {
		return fmt.Errorf("%w\n  See what failed: %s", err, logsCmd)
	}
	data, _ := os.ReadFile(l.file.Name())
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 25 {
		lines = lines[len(lines)-25:]
	}
	return fmt.Errorf("%w\n\n%s\n\n  Full output: %s\n  Container logs: %s", err, strings.Join(lines, "\n"), l.file.Name(), logsCmd)
}

// pullMissing pulls the images r needs and doesn't have, one line per image.
func pullMissing(r compose.Runner, out *startLog) error {
	missing, err := r.MissingImages()
	if err != nil || len(missing) == 0 {
		return err
	}
	fmt.Printf("  Pulling %d image(s), first run only:\n", len(missing))
	for _, img := range missing {
		fmt.Printf("    %s\n", img)
	}
	if err := r.Pull(); err != nil {
		return out.failed(err, "plat5 start -v")
	}
	return nil
}

package compose

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveDir validates an exact compose directory path (must contain a compose file).
// No layout guessing — path mode points at the directory that holds docker-compose.yml.
func ResolveDir(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("compose path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := validateComposeDir(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// ValidateComposeDir reports whether dir contains a compose file.
func ValidateComposeDir(dir string) error {
	return validateComposeDir(dir)
}

func validateComposeDir(dir string) error {
	for _, name := range []string{"docker-compose.yml", "compose.yml"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return nil
		}
	}
	return fmt.Errorf("no docker-compose.yml in %s", dir)
}

// BaseFile returns the compose file name in dir.
func BaseFile(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err == nil {
		return "docker-compose.yml"
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yml")); err == nil {
		return "compose.yml"
	}
	return "docker-compose.yml"
}

// Runner executes docker compose for one stack.
type Runner struct {
	Dir           string
	ProjectName   string
	OverrideFiles []string // absolute paths
}

func (r Runner) fileArgs() []string {
	var args []string
	if r.ProjectName != "" {
		args = append(args, "-p", r.ProjectName)
	}
	base := BaseFile(r.Dir)
	args = append(args, "-f", base)
	for _, f := range r.OverrideFiles {
		if f != "" {
			args = append(args, "-f", f)
		}
	}
	return args
}

func (r Runner) cmd(args ...string) *exec.Cmd {
	all := append([]string{"compose"}, r.fileArgs()...)
	all = append(all, args...)
	c := exec.Command("docker", all...)
	c.Dir = r.Dir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c
}

// Up runs docker compose up.
func (r Runner) Up(detach, build bool, extraEnv []string) error {
	args := []string{"up"}
	if detach {
		args = append(args, "-d")
	}
	if build {
		args = append(args, "--build")
	}
	c := r.cmd(args...)
	if len(extraEnv) > 0 {
		c.Env = append(os.Environ(), extraEnv...)
	}
	return run(c)
}

// Down runs docker compose down.
func (r Runner) Down() error {
	return run(r.cmd("down"))
}

// Logs runs docker compose logs.
func (r Runner) Logs(follow bool, services ...string) error {
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, services...)
	return run(r.cmd(args...))
}

// Ps runs docker compose ps -a and returns combined output.
func (r Runner) Ps() (string, error) {
	c := r.cmd("ps", "-a", "--format", "table {{.Name}}\t{{.Status}}\t{{.Ports}}")
	c.Stdout = nil
	c.Stderr = nil
	out, err := c.CombinedOutput()
	return string(out), err
}

// Running reports whether any compose service container is running.
func (r Runner) Running() (bool, error) {
	c := r.cmd("ps", "-q", "--status", "running")
	c.Stdout = nil
	c.Stderr = nil
	out, err := c.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "No such") || strings.Contains(string(out), "not found") {
			return false, nil
		}
		if len(strings.TrimSpace(string(out))) == 0 {
			c2 := r.cmd("ps", "-q")
			c2.Stdout = nil
			c2.Stderr = nil
			out2, err2 := c2.CombinedOutput()
			if err2 != nil {
				return false, nil
			}
			return len(strings.TrimSpace(string(out2))) > 0, nil
		}
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

func run(c *exec.Cmd) error {
	if err := c.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(c.Args[1:], " "), err)
	}
	return nil
}

// DockerAvailable checks docker and compose plugin.
func DockerAvailable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found on PATH")
	}
	c := exec.Command("docker", "compose", "version")
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose not available: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

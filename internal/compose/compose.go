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
	// Env is applied to every docker compose invocation. Keys here override the
	// process environment. Compose interpolates the file on restart, down, logs,
	// and ps — not only up — so required pins (PLAT5_VERSION, AUTH_VERSION,
	// OPERATOR_VERSION) belong here.
	Env []string
	// Wait adds --wait to detached up: return once services are running and healthy.
	Wait bool
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
	if len(r.Env) > 0 {
		c.Env = overlayEnv(os.Environ(), r.Env)
	}
	return c
}

// overlayEnv returns base with overlay keys replaced. Overlay wins, including
// over a stale shell value of the same name. Duplicate overlay keys keep the last.
func overlayEnv(base, overlay []string) []string {
	over := make(map[string]string, len(overlay))
	order := make([]string, 0, len(overlay))
	for _, e := range overlay {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			continue
		}
		if _, seen := over[k]; !seen {
			order = append(order, k)
		}
		over[k] = v
	}
	out := make([]string, 0, len(base)+len(order))
	for _, e := range base {
		k, _, ok := strings.Cut(e, "=")
		if ok {
			if _, drop := over[k]; drop {
				continue
			}
		}
		out = append(out, e)
	}
	for _, k := range order {
		out = append(out, k+"="+over[k])
	}
	return out
}

// Up runs docker compose up.
func (r Runner) Up(detach, build bool) error {
	args := []string{"up"}
	if detach {
		args = append(args, "-d")
		if r.Wait {
			args = append(args, "--wait")
		}
	}
	if build {
		args = append(args, "--build")
	}
	return run(r.cmd(args...))
}

// Restart runs docker compose restart for services.
func (r Runner) Restart(services ...string) error {
	return run(r.cmd(append([]string{"restart"}, services...)...))
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

// ProjectRunning reports whether compose project name has a running container.
// It needs no compose files: docker compose finds the project by its labels.
func ProjectRunning(name string) bool {
	if name == "" {
		return false
	}
	out, err := exec.Command("docker", "compose", "-p", name, "ps", "-q", "--status", "running").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
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

// Package compose runs docker compose for one project.
package compose

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Runner runs docker compose for one project.
type Runner struct {
	// Dir is the project directory: relative paths in Files resolve against it.
	Dir         string
	ProjectName string
	// Files are compose files, in merge order. Empty: the directory's default file.
	Files []string
	// Env is applied to every invocation, over the process environment. Compose
	// interpolates the files on every command (ps, logs, down too), so everything
	// the files need belongs here.
	Env []string
	// Quiet sends output nowhere; for probes.
	Quiet bool
}

func (r Runner) args(args ...string) []string {
	all := []string{"compose"}
	if r.ProjectName != "" {
		all = append(all, "-p", r.ProjectName)
	}
	if r.Dir != "" {
		all = append(all, "--project-directory", r.Dir)
	}
	for _, f := range r.Files {
		all = append(all, "-f", f)
	}
	return append(all, args...)
}

func (r Runner) cmd(args ...string) *exec.Cmd {
	c := exec.Command("docker", r.args(args...)...)
	c.Dir = r.Dir
	if !r.Quiet {
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
	}
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
		if k, _, ok := strings.Cut(e, "="); ok {
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

// UpOptions are docker compose up flags.
type UpOptions struct {
	Build bool
	// Wait returns once every service is running and healthy, and one-shot
	// services have exited 0. Zero: don't wait.
	Wait time.Duration
	// RemoveOrphans removes services no longer in Files (a module turned off).
	RemoveOrphans bool
}

// Up runs docker compose up -d.
func (r Runner) Up(o UpOptions) error {
	args := []string{"up", "-d"}
	if o.Build {
		args = append(args, "--build")
	}
	if o.Wait > 0 {
		args = append(args, "--wait", "--wait-timeout", strconv.Itoa(int(o.Wait.Seconds())))
	}
	if o.RemoveOrphans {
		args = append(args, "--remove-orphans")
	}
	return run(r.cmd(args...))
}

// Down runs docker compose down. Volumes are kept.
func (r Runner) Down() error {
	return run(r.cmd("down", "--remove-orphans"))
}

// Logs runs docker compose logs.
func (r Runner) Logs(follow bool, services ...string) error {
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	return run(r.cmd(append(args, services...)...))
}

// Ps returns docker compose ps as a table.
func (r Runner) Ps() (string, error) {
	c := r.cmd("ps", "-a", "--format", "table {{.Service}}\t{{.Status}}\t{{.Ports}}")
	c.Stdout, c.Stderr = nil, nil
	out, err := c.CombinedOutput()
	return string(out), err
}

// Running reports whether the project has a running container.
// It needs no compose files: compose finds the project by its labels.
func Running(project string) bool {
	if project == "" {
		return false
	}
	out, err := exec.Command("docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+project).Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// publishedRE matches a host port, or a range docker collapsed consecutive ports
// into: "127.0.0.1:4317-4318->4317-4318/tcp".
var publishedRE = regexp.MustCompile(`:(\d+)(?:-(\d+))?->`)

// PublishedPorts is the host ports a project's running containers publish.
func PublishedPorts(project string) map[int]bool {
	out, err := exec.Command("docker", "ps", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Ports}}").Output()
	if err != nil {
		return nil
	}
	return parsePublished(string(out))
}

func parsePublished(s string) map[int]bool {
	ports := map[int]bool{}
	for _, m := range publishedRE.FindAllStringSubmatch(s, -1) {
		lo, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		hi := lo
		if m[2] != "" {
			if hi, err = strconv.Atoi(m[2]); err != nil || hi < lo {
				continue
			}
		}
		for p := lo; p <= hi; p++ {
			ports[p] = true
		}
	}
	return ports
}

func run(c *exec.Cmd) error {
	if err := c.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(c.Args[1:], " "), err)
	}
	return nil
}

// DockerAvailable checks for docker, a running daemon, and the compose plugin.
func DockerAvailable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found. Install Docker Desktop (or Docker Engine) first")
	}
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("docker is not running: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose not available: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

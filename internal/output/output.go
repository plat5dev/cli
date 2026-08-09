package output

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ProbeHTTP returns "up" or "down" for a URL.
func ProbeHTTP(url string, timeout time.Duration) string {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return "down"
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 500 {
		return "down"
	}
	return "up"
}

// PrintStatus prints a Supabase-style status block.
func PrintStatus(w io.Writer, lines []string) {
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintln(w, "Plat5 local")
	for _, line := range lines {
		fmt.Fprintln(w, "  "+line)
	}
}

// TokenSource describes where admin token came from (for display only).
func TokenSource(token, defaultToken string) string {
	if token == defaultToken {
		return "default"
	}
	if os.Getenv("PLAT5_ADMIN_TOKEN") != "" {
		return "env PLAT5_ADMIN_TOKEN"
	}
	if os.Getenv("ADMIN_TOKEN") != "" {
		return "env ADMIN_TOKEN"
	}
	return "config/flag"
}

// JoinNames joins service names.
func JoinNames(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

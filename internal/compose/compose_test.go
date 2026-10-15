package compose

import (
	"strings"
	"testing"
	"time"
)

func envValue(env []string, key string) string {
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func TestOverlayEnvRunnerWins(t *testing.T) {
	base := []string{"PLAT5_VERSION=from-shell", "KEEP=1"}
	got := overlayEnv(base, []string{"PLAT5_VERSION=from-runner", "GATEWAY_PORT=5400"})
	if envValue(got, "PLAT5_VERSION") != "from-runner" || envValue(got, "KEEP") != "1" || envValue(got, "GATEWAY_PORT") != "5400" {
		t.Fatalf("%v", got)
	}
	if strings.Count(strings.Join(got, "\n"), "PLAT5_VERSION=") != 1 {
		t.Fatalf("duplicate key: %v", got)
	}
}

func TestArgs(t *testing.T) {
	r := Runner{Dir: "/state/stack", ProjectName: "plat5-demo", Files: []string{"core.yml", "auth.yml"}}
	got := strings.Join(r.args("up", "-d"), " ")
	want := "compose -p plat5-demo --project-directory /state/stack -f core.yml -f auth.yml up -d"
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestUpArgs(t *testing.T) {
	r := Runner{Quiet: true}
	c := r.cmd("up")
	if c.Stdout != nil {
		t.Fatal("quiet runner must not attach stdout")
	}
	_ = UpOptions{Wait: time.Minute}
}

func TestParsePublished(t *testing.T) {
	got := parsePublished("0.0.0.0:5400->5001/tcp, [::]:5400->5001/tcp\n127.0.0.1:5403->3000/tcp\n5432/tcp\n127.0.0.1:4317-4318->4317-4318/tcp\n")
	if !got[5400] || !got[5403] || !got[4317] || !got[4318] || got[5432] || len(got) != 4 {
		t.Fatalf("%v", got)
	}
}

func TestParseConfigPorts(t *testing.T) {
	data := []byte(`{"services":{
		"api":{"networks":{"default":null,"backend":null},"ports":[{"target":3000,"published":"3000","host_ip":"127.0.0.1"}]},
		"web":{"ports":[{"target":8080,"published":3001}]},
		"db":{"ports":[{"target":5432}]}}}`)
	got, err := parseConfigPorts(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 published ports, got %+v", got)
	}
	api := got[3000]
	if api.Service != "api" || api.Target != 3000 || strings.Join(api.Networks, ",") != "backend,default" {
		t.Fatalf("api: %+v", api)
	}
	if web := got[3001]; web.Service != "web" || web.Target != 8080 {
		t.Fatalf("web: %+v", web)
	}
}

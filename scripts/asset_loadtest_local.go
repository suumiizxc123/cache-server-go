package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Local wrapper for asset_loadtest.go:
// - Forces IPv4 localhost defaults to avoid ::1 connection resets.
// - Keeps dev path simple: uploads/reads through Go server :8080.
func main() {
	args := os.Args[1:]

	if hasAny(args, "-ha", "-ha=true", "--ha", "--ha=true",
		"-varnish", "-varnish=true", "--varnish", "--varnish=true",
		"-nginx", "-nginx=true", "--nginx", "--nginx=true") {
		fmt.Fprintln(os.Stderr, "local wrapper supports dev mode only (no -ha/-varnish/-nginx). Use asset_loadtest.go for proxy modes.")
		os.Exit(2)
	}

	effectiveHost := getFlagValue(args, "host")
	if effectiveHost == "" {
		effectiveHost = "127.0.0.1"
		args = append(args, "-host="+effectiveHost)
	}

	// If provided host is unreachable, fallback to local dev endpoint.
	if !serverReachable(effectiveHost) && effectiveHost != "127.0.0.1" && serverReachable("127.0.0.1") {
		fmt.Fprintf(os.Stderr, "NOTE: %s:8080 unreachable, falling back to 127.0.0.1:8080\n", effectiveHost)
		effectiveHost = "127.0.0.1"
		args = upsertFlag(args, "host", effectiveHost)
	}

	if !hasFlag(args, "upload") {
		args = append(args, "-upload=http://"+effectiveHost+":8080")
	}

	cmdArgs := append([]string{"run", "./scripts/asset_loadtest.go"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "failed to run asset_loadtest.go: %v\n", err)
		os.Exit(1)
	}
}

func hasFlag(args []string, name string) bool {
	short := "-" + name
	long := "--" + name
	for _, a := range args {
		if a == short || a == long || strings.HasPrefix(a, short+"=") || strings.HasPrefix(a, long+"=") {
			return true
		}
	}
	return false
}

func getFlagValue(args []string, name string) string {
	short := "-" + name + "="
	long := "--" + name + "="
	for i, a := range args {
		if strings.HasPrefix(a, short) {
			return strings.TrimPrefix(a, short)
		}
		if strings.HasPrefix(a, long) {
			return strings.TrimPrefix(a, long)
		}
		if (a == "-"+name || a == "--"+name) && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasAny(args []string, values ...string) bool {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	for _, a := range args {
		if _, ok := set[a]; ok {
			return true
		}
	}
	return false
}

func upsertFlag(args []string, name, value string) []string {
	shortEq := "-" + name + "="
	longEq := "--" + name + "="
	short := "-" + name
	long := "--" + name
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, shortEq) {
			args[i] = shortEq + value
			return args
		}
		if strings.HasPrefix(a, longEq) {
			args[i] = longEq + value
			return args
		}
		if (a == short || a == long) && i+1 < len(args) {
			args[i+1] = value
			return args
		}
	}
	return append(args, shortEq+value)
}

func serverReachable(host string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + host + ":8080/health")
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

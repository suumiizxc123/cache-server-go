package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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

	if !hasFlag(args, "host") {
		args = append(args, "-host=127.0.0.1")
	}
	if !hasFlag(args, "upload") {
		args = append(args, "-upload=http://127.0.0.1:8080")
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

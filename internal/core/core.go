// Package core defines the Core interface and registry for proxy-core runners.
// Each runner starts and stops a proxy process (sing-box, xray, hiddify-core, …).
package core

import (
	"context"
	"io"
	"strings"
)

// Core manages the lifecycle of one proxy process instance.
type Core interface {
	// Name is the stable identifier: "sing-box", "xray", "hiddify-core".
	Name() string
	// Start spawns the process using the given (already-rendered) config file.
	// Output is written to out (nil = discard).
	Start(ctx context.Context, configPath string, out io.Writer) error
	// Stop terminates the process and waits for it to exit.
	Stop() error
	// Version returns the binary's version string (from --version), or "".
	Version() string
}

// Factory creates a Core for a given binary path.
type Factory func(binPath string) Core

var registry = map[string]Factory{}

// Register adds a factory under name. Called from each runner's init().
func Register(name string, f Factory) {
	registry[name] = f
}

// New returns a Core for the given core name and binary path.
// Returns nil if the name is not registered.
func New(name, binPath string) Core {
	f, ok := registry[name]
	if !ok {
		return nil
	}
	return f(binPath)
}

// NewRaw constructs a Core from an explicit binary path and arg template.
//
// If any element of args contains "{{CONFIG_PATH}}", it is replaced with the
// rendered config file path at Start time. If no element contains the
// placeholder, the config path is appended at the end (backward compat).
func NewRaw(binPath string, args []string) Core {
	argsCopy := make([]string, len(args))
	copy(argsCopy, args)
	return &processCore{
		name:    binPath,
		binPath: binPath,
		runArgs: buildRunArgs(argsCopy),
	}
}

// buildRunArgs returns a runArgs function that substitutes {{CONFIG_PATH}}.
// If no placeholder present, config path is appended at end.
func buildRunArgs(args []string) func(string) []string {
	const placeholder = "{{CONFIG_PATH}}"
	hasPlaceholder := false
	for _, a := range args {
		if strings.Contains(a, placeholder) {
			hasPlaceholder = true
			break
		}
	}
	return func(configPath string) []string {
		if !hasPlaceholder {
			return append(append([]string{}, args...), configPath)
		}
		out := make([]string, len(args))
		for i, a := range args {
			out[i] = strings.ReplaceAll(a, placeholder, configPath)
		}
		return out
	}
}

// Names returns all registered core names.
func Names() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

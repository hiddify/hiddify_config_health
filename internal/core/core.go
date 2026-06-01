// Package core defines the Core interface and registry for proxy-core runners.
// Each runner starts and stops a proxy process (sing-box, xray, hiddify-core, …).
package core

import (
	"context"
	"io"
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

// NewRaw constructs a Core from an explicit binary path and a fixed arg prefix.
// The config file path is appended after args when Start is called.
func NewRaw(binPath string, args []string) Core {
	argsCopy := make([]string, len(args))
	copy(argsCopy, args)
	return &processCore{
		name:    binPath,
		binPath: binPath,
		runArgs: func(cfg string) []string {
			return append(argsCopy, cfg)
		},
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

package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// processCore is the base implementation for any binary that accepts a config
// file path as its last argument and supports a --version flag.
type processCore struct {
	name    string
	binPath string
	runArgs func(configPath string) []string // builds CLI args list
	checkArgs []string                       // args to validate config (nil = skip)

	mu      sync.Mutex
	cmd     *exec.Cmd
	version string
}

func (p *processCore) Name() string { return p.name }

func (p *processCore) Version() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.version != "" {
		return p.version
	}
	bin := p.binPath
	if bin == "" {
		var err error
		bin, err = exec.LookPath(p.name)
		if err != nil {
			return ""
		}
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		out, err = exec.Command(bin, "--version").Output()
	}
	if err == nil {
		p.version = strings.TrimSpace(string(out))
		// trim to first line
		if idx := strings.IndexByte(p.version, '\n'); idx > 0 {
			p.version = p.version[:idx]
		}
	}
	return p.version
}

func (p *processCore) Start(ctx context.Context, configPath string, out io.Writer) error {
	bin := p.binPath
	if bin == "" {
		var err error
		bin, err = exec.LookPath(p.name)
		if err != nil {
			return fmt.Errorf("core %s: binary not found in PATH; set %s_BIN env", p.name, strings.ToUpper(strings.ReplaceAll(p.name, "-", "_")))
		}
	}

	args := p.runArgs(configPath)
	cmd := exec.CommandContext(ctx, bin, args...)
	if out != nil {
		cmd.Stdout = out
		cmd.Stderr = out
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}

	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()

	return cmd.Start()
}

func (p *processCore) Stop() error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(os.Interrupt)
	return cmd.Wait()
}

// Check validates the config file without starting the process.
func (p *processCore) Check(configPath string) error {
	if len(p.checkArgs) == 0 {
		return nil
	}
	bin := p.binPath
	if bin == "" {
		var err error
		bin, err = exec.LookPath(p.name)
		if err != nil {
			return fmt.Errorf("core %s not found", p.name)
		}
	}
	args := append(p.checkArgs, configPath)
	var buf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("check failed: %s", buf.String())
	}
	return nil
}

// binFromEnv returns the env-override or empty string.
func binFromEnv(envKey string) string {
	return os.Getenv(envKey)
}

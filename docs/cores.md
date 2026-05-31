# Core Setup

A *core* is a proxy binary that hiddify-health starts and stops around each test.
Three cores ship out of the box; adding a new one requires one small Go file.

## sing-box

**Binary detection order:**
1. `SINGBOX_BIN` env var
2. `sing-box` in `PATH`

```bash
# Install (macOS)
brew install sing-box

# Or download a release binary
curl -L https://github.com/SagerNet/sing-box/releases/latest/download/sing-box-linux-amd64.tar.gz | tar xz
export SINGBOX_BIN=$(pwd)/sing-box
```

**CLI used by the runner:**
```
sing-box run -c <config>      # start
sing-box check -c <config>    # validate (used by `hiddify-health check`)
```

**Build tags** — if building sing-box from source, include the tags your
protocols need:

```bash
go build -tags "with_gvisor,with_wireguard,with_quic" -o sing-box ./cmd/sing-box
```

---

## xray-core

**Binary detection order:**
1. `XRAY_BIN` env var
2. `xray` in `PATH`

```bash
# Download release
bash <(curl -L https://github.com/XTLS/Xray-core/releases/latest/download/Xray-linux-64.zip)
export XRAY_BIN=/usr/local/bin/xray
```

**CLI used by the runner:**
```
xray run -c <config>           # start
xray run -test -c <config>     # validate
```

---

## hiddify-core

**Binary detection order:**
1. `HIDDIFY_BIN` env var
2. `hiddify-core` in `PATH`

```bash
export HIDDIFY_BIN=/usr/local/bin/hiddify-core
```

**CLI used by the runner:**
```
hiddify-core run <config>
```

No check subcommand is defined yet; `hiddify-health check` skips validation
for hiddify-core configs.

---

## Adding a new core

Create `internal/core/mycore.go`:

```go
package core

func init() {
    Register("my-core", func(binPath string) Core {
        if binPath == "" {
            binPath = binFromEnv("MY_CORE_BIN")
        }
        return &processCore{
            name:    "my-core",
            binPath: binPath,
            // Build the CLI args for "start" mode.
            runArgs: func(cfg string) []string {
                return []string{"run", "--config", cfg}
            },
            // Args for "validate" mode (nil = skip validation).
            checkArgs: []string{"check", "--config"},
        }
    })
}
```

Then use `"core": "my-core"` in `run.json`. The runner auto-detects the binary
from `MY_CORE_BIN` env or `PATH`.

## Version reporting

Each core runner calls `<binary> version` (then `<binary> --version` as
fallback) on first use to capture the version string. It appears in CLI output
and is stored in the SQLite history so you can diff results across versions.

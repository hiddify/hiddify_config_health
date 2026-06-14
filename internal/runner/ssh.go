package runner

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type sshConn struct {
	client *ssh.Client
	sftp   *sftp.Client
}

func (s *sshConn) Close() {
	if s.sftp != nil {
		_ = s.sftp.Close()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
}

// dialSSH connects to rawURL (ssh://[user[:pass]@]host[:port]).
//
// Authentication order:
//  1. Password from URL (ssh://user:pass@host) if present
//  2. SSH agent (SSH_AUTH_SOCK)
//  3. Key files: ~/.ssh/id_ed25519, id_rsa, id_ecdsa
// sshHostFromURL extracts the hostname from an ssh:// deploy URL, so a
// deployed server's client can be pointed at the same host instead of
// 127.0.0.1.
func sshHostFromURL(rawURL string) string {
	if !strings.Contains(rawURL, "://") {
		rawURL = "ssh://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func dialSSH(rawURL string) (*sshConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("ssh: parse URL %q: %w", rawURL, err)
	}
	user := "root"
	if u.User != nil && u.User.Username() != "" {
		user = u.User.Username()
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "22"
	}
	addr := net.JoinHostPort(host, port)

	var authMethods []ssh.AuthMethod

	// Password from URL takes highest priority.
	if u.User != nil {
		if pass, ok := u.User.Password(); ok && pass != "" {
			authMethods = append(authMethods, ssh.Password(pass))
			// Also try keyboard-interactive in case server requires it.
			authMethods = append(authMethods, ssh.KeyboardInteractive(
				func(name, instruction string, questions []string, echos []bool) ([]string, error) {
					answers := make([]string, len(questions))
					for i := range questions {
						answers[i] = pass
					}
					return answers, nil
				}))
		}
	}

	// SSH agent.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			authMethods = append(authMethods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Key files.
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		path := filepath.Join(os.Getenv("HOME"), ".ssh", name)
		if signer, err := loadSigner(path); err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("ssh: no authentication methods available (provide password in URL or add key to ~/.ssh/)")
	}

	knownHostsPath := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
	var hostKeyCallback ssh.HostKeyCallback
	if kh, err := knownhosts.New(knownHostsPath); err == nil {
		hostKeyCallback = kh
	} else {
		//nolint:gosec // InsecureIgnoreHostKey only as fallback when no known_hosts
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}

	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("sftp: %w", err)
	}

	return &sshConn{client: client, sftp: sftpClient}, nil
}

// scpFile uploads a local file to a remote path via SFTP.
func scpFile(sc *sshConn, localPath, remotePath string) error {
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	if err := sc.sftp.MkdirAll(filepath.Dir(remotePath)); err != nil {
		return fmt.Errorf("sftp mkdir: %w", err)
	}

	dst, err := sc.sftp.Create(remotePath)
	if err != nil {
		return fmt.Errorf("sftp create %s: %w", remotePath, err)
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// sshExec runs a command on the remote host and returns an error if it fails.
func sshExec(sc *sshConn, cmd string) error {
	sess, err := sc.client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	var buf strings.Builder
	sess.Stdout = &buf
	sess.Stderr = &buf
	if err := sess.Run(cmd); err != nil {
		return fmt.Errorf("ssh exec %q: %s: %w", cmd, buf.String(), err)
	}
	return nil
}

// sshOutput runs cmd remotely and returns its trimmed combined output.
func sshOutput(sc *sshConn, cmd string) (string, error) {
	sess, err := sc.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var buf strings.Builder
	sess.Stdout = &buf
	sess.Stderr = &buf
	err = sess.Run(cmd)
	return strings.TrimSpace(buf.String()), err
}

// resolveRemoteBin finds a usable core binary ON THE REMOTE host. The local
// binary path (e.g. a macOS Homebrew path) is meaningless on the server, so we
// must locate the remote core. Resolution order:
//  1. env HCH_REMOTE_<CORE>_BIN (an absolute remote path), e.g. HCH_REMOTE_SINGBOX_BIN
//  2. `command -v <core>` on the remote (PATH lookup)
//  3. common install locations
// Returns "" if none found.
func resolveRemoteBin(sc *sshConn, core string) string {
	envKey := "HCH_REMOTE_" + strings.ToUpper(strings.ReplaceAll(core, "-", "")) + "_BIN"
	if p := os.Getenv(envKey); p != "" {
		return p
	}
	// command -v respects the remote PATH; quote core defensively.
	if out, err := sshOutput(sc, "command -v "+core+" 2>/dev/null"); err == nil && out != "" {
		return out
	}
	for _, p := range []string{
		"/usr/local/bin/" + core,
		"/usr/bin/" + core,
		"/opt/" + core + "/" + core,
		"/root/" + core,
		"/tmp/hch/" + core, // a previous auto-install
	} {
		if out, err := sshOutput(sc, "test -x "+p+" && echo "+p); err == nil && out != "" {
			return out
		}
	}
	return ""
}

// installRemoteCore downloads the official release of the given core for the
// remote host's architecture and installs it to /tmp/hch/<core>. Returns the
// remote path, or an error (e.g. no internet on the server, unknown arch).
//
// Needs outbound HTTPS on the remote. Uses curl or wget, whichever exists.
func installRemoteCore(sc *sshConn, core string) (string, error) {
	arch, err := sshOutput(sc, "uname -m")
	if err != nil {
		return "", fmt.Errorf("remote uname: %w", err)
	}
	goArch := map[string]string{
		"x86_64": "amd64", "amd64": "amd64",
		"aarch64": "arm64", "arm64": "arm64",
	}[strings.TrimSpace(arch)]
	if goArch == "" {
		return "", fmt.Errorf("unsupported remote arch %q", arch)
	}

	script, err := installScript(core, goArch)
	if err != nil {
		return "", err
	}
	out, err := sshOutput(sc, script)
	if err != nil {
		return "", fmt.Errorf("remote install %s failed: %s: %w", core, out, err)
	}
	remoteBin := "/tmp/hch/" + core
	if v, verr := sshOutput(sc, "test -x "+remoteBin+" && echo ok"); verr != nil || v != "ok" {
		return "", fmt.Errorf("remote install %s: binary not present after install: %s", core, out)
	}
	return remoteBin, nil
}

// installScript returns a POSIX-sh script that downloads and extracts the
// official release of core (linux/goArch) into /tmp/hch/<core>.
func installScript(core, goArch string) (string, error) {
	const sbVer = "1.13.13"
	const xrVer = "26.3.27"
	var url, extract string
	switch core {
	case "sing-box":
		// sing-box release tarball: sing-box-<ver>-linux-<arch>.tar.gz, binary nested in a dir.
		url = fmt.Sprintf("https://github.com/SagerNet/sing-box/releases/download/v%s/sing-box-%s-linux-%s.tar.gz", sbVer, sbVer, goArch)
		extract = "tar -xzf c.tgz && cp sing-box-*/sing-box ./sing-box"
	case "xray":
		// xray release zip: Xray-linux-64.zip (amd64) / Xray-linux-arm64-v8a.zip.
		zipArch := "64"
		if goArch == "arm64" {
			zipArch = "arm64-v8a"
		}
		url = fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/v%s/Xray-linux-%s.zip", xrVer, zipArch)
		extract = "unzip -o c.tgz xray && true"
	default:
		return "", fmt.Errorf("auto-install not supported for core %q", core)
	}
	// Use curl or wget; unzip/tar as needed; clean exit if binary lands.
	script := strings.Join([]string{
		"set -e",
		"mkdir -p /tmp/hch && cd /tmp/hch",
		"if command -v curl >/dev/null 2>&1; then DL='curl -fsSL -o c.tgz'; " +
			"elif command -v wget >/dev/null 2>&1; then DL='wget -qO c.tgz'; " +
			"else echo NO_DOWNLOADER; exit 1; fi",
		"$DL '" + url + "'",
		extract,
		"chmod +x " + core,
		"rm -f c.tgz",
		"echo INSTALLED " + core,
	}, "\n")
	return script, nil
}

func loadSigner(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(b)
}

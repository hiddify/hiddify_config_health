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

// dialSSH connects to rawURL (ssh://user@host:port).
// Authentication order: SSH agent → ~/.ssh/id_ed25519 → ~/.ssh/id_rsa.
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

	// Try SSH agent first.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			authMethods = append(authMethods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Try common key files.
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		path := filepath.Join(os.Getenv("HOME"), ".ssh", name)
		if signer, err := loadSigner(path); err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("ssh: no authentication methods available")
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

func loadSigner(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(b)
}

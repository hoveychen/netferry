package sshconn

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// AuthConfig holds SSH authentication parameters.
type AuthConfig struct {
	// IdentityFile is the path to a private key file. Empty = auto-detect.
	IdentityFile string

	// ExtraOptions is a freeform string of "Key=Value" pairs parsed from --extra-ssh-opts.
	ExtraOptions string
}

// BuildSSHConfig builds an *ssh.ClientConfig from AuthConfig + user name.
func BuildSSHConfig(user string, ac AuthConfig) (*ssh.ClientConfig, error) {
	// Build auth methods — priority: agent → explicit key → default keys.
	var authMethods []ssh.AuthMethod

	// 1. SSH Agent
	if agentAuth := agentAuthMethod(); agentAuth != nil {
		authMethods = append(authMethods, agentAuth)
	}

	// 2. Explicit identity file
	if ac.IdentityFile != "" {
		expanded := expandHome(ac.IdentityFile)
		signers, err := signersFromFile(expanded)
		if err != nil {
			return nil, fmt.Errorf("identity file %q: %w", expanded, err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signers...))
	}

	// 3. Default identity files
	if ac.IdentityFile == "" {
		for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
			path := filepath.Join(expandHome("~"), ".ssh", name)
			signers, err := signersFromFile(path)
			if err == nil && len(signers) > 0 {
				authMethods = append(authMethods, ssh.PublicKeys(signers...))
			}
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available (no agent and no key found)")
	}

	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         0, // no connect timeout here; use net.DialTimeout
	}, nil
}

// agentAuthMethod returns an ssh.AuthMethod backed by the running SSH agent,
// or nil if SSH_AUTH_SOCK is not set / not reachable.
func agentAuthMethod() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	return ssh.PublicKeysCallback(agent.NewClient(conn).Signers)
}

// signersFromFile reads a private key file and returns signers.
// Returns empty slice (not error) if the file doesn't exist.
func signersFromFile(path string) ([]ssh.Signer, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			return nil, fmt.Errorf(
				"private key %q is passphrase-protected; use ssh-agent or add to macOS Keychain:\n"+
					"  ssh-add %s",
				path, path,
			)
		}
		return nil, err
	}
	return []ssh.Signer{signer}, nil
}


func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	return path
}

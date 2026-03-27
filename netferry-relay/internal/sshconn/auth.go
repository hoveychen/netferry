package sshconn

import (
	"fmt"
	"log"
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

	// IdentityPEM is inline PEM key material passed via environment variable
	// by the Tauri app. Takes precedence over IdentityFile when set.
	// Never written to disk.
	IdentityPEM string

	// ExtraOptions is a freeform string of "Key=Value" pairs parsed from --extra-ssh-opts.
	ExtraOptions string
}

// BuildSSHConfig builds an *ssh.ClientConfig from AuthConfig + user name.
func BuildSSHConfig(user string, ac AuthConfig) (*ssh.ClientConfig, error) {
	log.Printf("ssh-auth: building config for user=%q identityFile=%q", user, ac.IdentityFile)

	// Collect all signers into a single list so they are presented as one
	// "publickey" auth method.  The SSH auth loop marks each *method name*
	// as tried after a failure, so separate PublicKeys / PublicKeysCallback
	// entries would cause the second "publickey" method to be skipped once
	// the first one fails.
	var allSigners []ssh.Signer

	// 1. SSH Agent
	agentSigners := agentSigners()
	if len(agentSigners) > 0 {
		allSigners = append(allSigners, agentSigners...)
		log.Printf("ssh-auth: added %d key(s) from SSH agent", len(agentSigners))
	} else {
		log.Printf("ssh-auth: no SSH agent available (SSH_AUTH_SOCK=%q)", os.Getenv("SSH_AUTH_SOCK"))
	}

	// 2. Inline PEM key (passed via env var by the Tauri app; never written to disk)
	if ac.IdentityPEM != "" {
		signer, err := ssh.ParsePrivateKey([]byte(ac.IdentityPEM))
		if err != nil {
			return nil, fmt.Errorf("inline identity PEM: %w", err)
		}
		log.Printf("ssh-auth: loaded inline key type=%s fingerprint=%s",
			signer.PublicKey().Type(), ssh.FingerprintSHA256(signer.PublicKey()))
		allSigners = append(allSigners, signer)
	}

	// 3. Explicit identity file
	if ac.IdentityFile != "" {
		expanded := expandHome(ac.IdentityFile)
		signers, err := signersFromFile(expanded)
		if err != nil {
			return nil, fmt.Errorf("identity file %q: %w", expanded, err)
		}
		if len(signers) == 0 {
			log.Printf("ssh-auth: identity file %q not found or empty", expanded)
		} else {
			log.Printf("ssh-auth: loaded key from %q type=%s fingerprint=%s",
				expanded, signers[0].PublicKey().Type(),
				ssh.FingerprintSHA256(signers[0].PublicKey()))
			allSigners = append(allSigners, signers...)
		}
	}

	// 4. Default identity files (only when no explicit file or inline PEM is given)
	if ac.IdentityFile == "" && ac.IdentityPEM == "" {
		for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
			path := filepath.Join(expandHome("~"), ".ssh", name)
			signers, err := signersFromFile(path)
			if err == nil && len(signers) > 0 {
				log.Printf("ssh-auth: loaded default key %q type=%s", path, signers[0].PublicKey().Type())
				allSigners = append(allSigners, signers...)
			}
		}
	}

	log.Printf("ssh-auth: total signers: %d", len(allSigners))

	if len(allSigners) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available (no agent and no key found)")
	}

	return &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(allSigners...)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         0, // no connect timeout here; use net.DialTimeout
	}, nil
}

// agentSigners returns signers from the running SSH agent,
// or nil if SSH_AUTH_SOCK is not set / not reachable / has no keys.
func agentSigners() []ssh.Signer {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		conn.Close()
		return nil
	}
	if len(signers) == 0 {
		conn.Close()
		return nil
	}
	return signers
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

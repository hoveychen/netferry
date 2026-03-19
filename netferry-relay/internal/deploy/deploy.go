// Package deploy handles remote server binary deployment via SSH.
// Flow: detect arch → version check → conditional upload → return remote path.
package deploy

import (
	"bytes"
	"fmt"
	"io/fs"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ServerBinaries is the embed.FS provided by the tunnel binary containing
// all cross-compiled server binaries.
// It must be set by cmd/tunnel/embed.go before calling EnsureServer.
var ServerBinaries fs.ReadFileFS

// EnsureServer ensures the correct server binary is deployed on the remote host.
// Returns the remote path to execute.
func EnsureServer(client *ssh.Client, version string) (string, error) {
	// Step 1: detect remote arch.
	arch, err := detectArch(client)
	if err != nil {
		return "", fmt.Errorf("detect arch: %w", err)
	}

	// Step 2: build remote path.
	remotePath, err := remoteCachePath(client, version, arch)
	if err != nil {
		return "", fmt.Errorf("remote path: %w", err)
	}

	// Step 3: check if already deployed.
	if remoteFileExists(client, remotePath) {
		return remotePath, nil
	}

	// Step 4: upload binary.
	if err := upload(client, arch, remotePath); err != nil {
		return "", fmt.Errorf("upload server binary: %w", err)
	}

	// Step 5: clean up old versions (best-effort).
	go cleanOldVersions(client, arch, version)

	return remotePath, nil
}

// RemoteIP returns the remote IP address of the SSH connection (without port).
func RemoteIP(client *ssh.Client) string {
	addr := client.RemoteAddr().String()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// detectArch runs "uname -ms" on the remote host and maps it to GOOS-GOARCH.
func detectArch(client *ssh.Client) (string, error) {
	out, err := runSession(client, "uname -ms")
	if err != nil {
		return "", err
	}
	return parseUname(strings.TrimSpace(out)), nil
}

// parseUname converts "Linux x86_64" → "linux-amd64" etc.
func parseUname(s string) string {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return "linux-amd64" // safe default
	}
	os := strings.ToLower(fields[0])
	arch := fields[1]

	archMap := map[string]string{
		"x86_64":  "amd64",
		"amd64":   "amd64",
		"aarch64": "arm64",
		"arm64":   "arm64",
		"mips":    "mips",
		"mipsel":  "mipsle",
		"mipsle":  "mipsle",
	}
	goArch, ok := archMap[arch]
	if !ok {
		goArch = strings.ToLower(arch)
	}
	return os + "-" + goArch
}

// remoteCachePath returns the path where the server binary should live.
// Tries $HOME/.cache/netferry first, falls back to /tmp/.netferry.
func remoteCachePath(client *ssh.Client, version, arch string) (string, error) {
	// Try to use $HOME/.cache/netferry.
	home, err := runSession(client, "echo $HOME")
	if err != nil {
		home = ""
	}
	home = strings.TrimSpace(home)

	binaryName := "/server-" + version + "-" + arch

	if home != "" && home != "/" {
		dir := home + "/.cache/netferry"
		// Test if we can create the directory.
		if _, err := runSession(client, "mkdir -p "+shellQuote(dir)); err == nil {
			return dir + binaryName, nil
		}
	}

	// Fallback to /tmp/.netferry.
	dir := "/tmp/.netferry"
	if _, err := runSession(client, "mkdir -p "+shellQuote(dir)); err != nil {
		return "", fmt.Errorf("cannot create cache dir: %w", err)
	}
	return dir + binaryName, nil
}

// remoteFileExists checks whether a file exists and is executable.
func remoteFileExists(client *ssh.Client, path string) bool {
	_, err := runSession(client, "test -x "+shellQuote(path))
	return err == nil
}

// upload reads the embedded server binary for the given arch and uploads it.
func upload(client *ssh.Client, arch, remotePath string) error {
	if ServerBinaries == nil {
		return fmt.Errorf("ServerBinaries not set (embed not initialised)")
	}
	binaryName := "binaries/server-" + arch
	data, err := ServerBinaries.ReadFile(binaryName)
	if err != nil {
		return fmt.Errorf("no embedded binary for arch %q (file %q): %w", arch, binaryName, err)
	}

	// Upload atomically (write to .tmp, then mv).
	// The directory was already created by remoteCachePath.
	tmpPath := remotePath + ".tmp"

	// Stream binary via stdin of "cat > tmpPath".
	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()
	sess.Stdin = bytes.NewReader(data)
	cmd := fmt.Sprintf("cat > %s && chmod +x %s && mv %s %s",
		shellQuote(tmpPath), shellQuote(tmpPath),
		shellQuote(tmpPath), shellQuote(remotePath))
	if err := sess.Run(cmd); err != nil {
		return fmt.Errorf("upload command: %w", err)
	}
	return nil
}

// cleanOldVersions removes server binaries for the same arch but different versions.
func cleanOldVersions(client *ssh.Client, arch, currentVersion string) {
	dir := "~/.cache/netferry"
	// List all server-*-ARCH files, remove those not matching currentVersion.
	cmd := fmt.Sprintf(
		"ls %s/server-*-%s 2>/dev/null | grep -v %s | xargs rm -f 2>/dev/null; true",
		dir, shellQuote(arch), shellQuote(currentVersion),
	)
	runSession(client, cmd) //nolint:errcheck
}

// runSession runs a command on the remote host and returns stdout.
func runSession(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.Output(cmd)
	return string(out), err
}

// shellQuote wraps a string in single quotes, escaping any existing single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

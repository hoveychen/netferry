// Package deploy handles remote server binary deployment via SSH.
// Flow: detect arch → version check → conditional upload → return remote path.
package deploy

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log"
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

	// Step 3: read embedded binary to get its size.
	if ServerBinaries == nil {
		return "", fmt.Errorf("ServerBinaries not set (embed not initialised)")
	}
	binaryName := "binaries/server-" + arch
	localData, err := ServerBinaries.ReadFile(binaryName)
	if err != nil {
		return "", fmt.Errorf("no embedded binary for arch %q (file %q): %w", arch, binaryName, err)
	}

	// Step 4: check if this version already exists on remote.
	remoteSize := remoteFileSize(client, remotePath)
	localSize := int64(len(localData))
	if shouldReuseRemote(remoteSize, localSize) {
		log.Printf("deploy-reason: up-to-date")
		return remotePath, nil
	}
	if remoteSize < 0 {
		log.Printf("deploy-reason: first-deploy")
	} else {
		log.Printf("deploy-reason: size-mismatch remote=%d local=%d", remoteSize, localSize)
	}

	// Step 5: upload binary.
	if err := uploadData(client, localData, remotePath); err != nil {
		return "", fmt.Errorf("upload server binary: %w", err)
	}

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

// shouldReuseRemote reports whether the remote cached binary can be executed
// as-is instead of being re-uploaded. remoteSize < 0 means the file does not
// exist. A size match is required because the path alone (which includes the
// version string) does not catch truncated uploads or stale files left behind
// by earlier builds that reused the same version identifier.
func shouldReuseRemote(remoteSize, localSize int64) bool {
	return remoteSize == localSize
}

// remoteFileSize returns the size of a remote file, or -1 if it doesn't exist.
func remoteFileSize(client *ssh.Client, path string) int64 {
	out, err := runSession(client, "wc -c < "+shellQuote(path))
	if err != nil {
		return -1
	}
	out = strings.TrimSpace(out)
	var size int64
	if _, err := fmt.Sscanf(out, "%d", &size); err != nil {
		return -1
	}
	return size
}

// uploadData uploads the given binary data to the remote path.
func uploadData(client *ssh.Client, data []byte, remotePath string) error {
	// Upload atomically (write to .tmp, then mv).
	// The directory was already created by remoteCachePath.
	tmpPath := remotePath + ".tmp"

	// Stream binary via stdin of "cat > tmpPath", writing in chunks
	// so we can report upload progress to stderr.
	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	stdinPipe, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	cmd := fmt.Sprintf("cat > %s && chmod +x %s && mv %s %s",
		shellQuote(tmpPath), shellQuote(tmpPath),
		shellQuote(tmpPath), shellQuote(remotePath))
	if err := sess.Start(cmd); err != nil {
		return fmt.Errorf("start upload command: %w", err)
	}

	total := int64(len(data))
	reader := bytes.NewReader(data)
	const chunkSize = 64 * 1024 // 64 KB chunks
	var sent int64

	log.Printf("deploy-progress: %d/%d", sent, total)
	buf := make([]byte, chunkSize)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if _, writeErr := stdinPipe.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("write to remote: %w", writeErr)
			}
			sent += int64(n)
			log.Printf("deploy-progress: %d/%d", sent, total)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read binary: %w", readErr)
		}
	}

	if err := stdinPipe.Close(); err != nil {
		return fmt.Errorf("close stdin: %w", err)
	}
	if err := sess.Wait(); err != nil {
		return fmt.Errorf("upload command: %w", err)
	}
	return nil
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

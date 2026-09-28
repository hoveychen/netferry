package deploy

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// FectunUp deploys the server binary over an existing (plain TCP) SSH
// connection and runs `server --fectun-up`, which makes sure the host's
// fectun daemon listens on UDP port and returns the pre-shared key it uses.
// rateMbps caps the daemon's send rate toward clients (0 = fectun default)
// and rateMinMbps is its congestion-control floor (0 = fixed rate);
// restart replaces an already-running daemon.
func FectunUp(client *ssh.Client, version string, port int, rateMbps, rateMinMbps float64, restart bool) (string, error) {
	remotePath, err := EnsureServer(client, version)
	if err != nil {
		return "", err
	}
	cmd := shellQuote(remotePath) + " --fectun-up --fectun-port " + strconv.Itoa(port) +
		" --fectun-rate " + strconv.FormatFloat(rateMbps, 'g', -1, 64) +
		" --fectun-rate-min " + strconv.FormatFloat(rateMinMbps, 'g', -1, 64)
	if restart {
		cmd += " --fectun-restart"
	}
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var stderr strings.Builder
	sess.Stderr = &stderr
	out, err := sess.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("fectun-up: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseFectunKey(string(out))
}

func parseFectunKey(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if k, ok := strings.CutPrefix(strings.TrimSpace(line), "fectun-key "); ok && k != "" {
			return k, nil
		}
	}
	return "", fmt.Errorf("fectun-up: no key in output %q", strings.TrimSpace(out))
}

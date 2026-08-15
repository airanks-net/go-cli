package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const updateCheckInterval = 24 * time.Hour
const updateFetchTimeout = 2500 * time.Millisecond
const updateInstallTimeout = 60 * time.Second
const upgradeFallback = "run: brew upgrade air  (or) go install github.com/airanks-net/go-cli@latest"

func updateCheckPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "air", "update-check.json")
}

type updateStamp struct {
	CheckedAt int64  `json:"checkedAt"`
	Latest    string `json:"latest"`
}

func readUpdateStamp() updateStamp {
	b, err := os.ReadFile(updateCheckPath())
	if err != nil {
		return updateStamp{}
	}
	var s updateStamp
	_ = json.Unmarshal(b, &s)
	return s
}

func writeUpdateStamp(s updateStamp) {
	dir := filepath.Dir(updateCheckPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return // cache is best-effort — a read-only home just means we re-check next run
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(updateCheckPath(), b, 0o600)
}

// semverGt reports whether dotted numeric version a is strictly newer than b.
func semverGt(a, b string) bool {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var na, nb int
		if i < len(pa) {
			na, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			nb, _ = strconv.Atoi(pb[i])
		}
		if na != nb {
			return na > nb
		}
	}
	return false
}

func latestGitHubRelease() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/airanks-net/go-cli/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", res.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	return strings.TrimPrefix(body.TagName, "v"), nil
}

// autoUpgrade: once/day, interactive-only check for a newer air release, with a best-effort
// self-upgrade via `go install`. Fully non-fatal — any network/parse/spawn failure is
// swallowed so a lookup is never blocked or broken by the update path. Opt out with
// AIR_NO_UPDATE. Always returns so the caller's real work proceeds regardless of outcome.
func autoUpgrade(interactive bool) {
	if os.Getenv("AIR_NO_UPDATE") != "" || !interactive {
		return
	}
	stamp := readUpdateStamp()
	if time.Since(time.UnixMilli(stamp.CheckedAt)) < updateCheckInterval {
		return
	}
	latest, err := latestGitHubRelease()
	if err != nil {
		latest = ""
	}
	writeUpdateStamp(updateStamp{CheckedAt: time.Now().UnixMilli(), Latest: latest})
	if latest == "" || !semverGt(latest, cliVersion) {
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", cyan(FA["hourglass"]), dim(fmt.Sprintf("updating air %s → %s…", cliVersion, latest)))
	ctx, cancel := context.WithTimeout(context.Background(), updateInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "install", "github.com/airanks-net/go-cli@latest")
	if cmd.Run() != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", yellow(FA["warn"]), dim(upgradeFallback))
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", green(FA["check"]), dim(fmt.Sprintf("updated to %s — re-run to use it", latest)))
}

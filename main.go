// Command air is the Go client for airanks.net's public AIR API: it looks up any
// domain's AIR score (AI Rank) straight from the terminal, or searches domains,
// brands, and phrases. Same public API the airanks toolbar browser extension uses,
// and the same shared login as every other AIR client (node, rust, PHP).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// wizardShouldRun gates the first-run setup wizard: only when there's no saved credential
// yet AND the session is unambiguously interactive. jsonOut covers --json; stderrTTY is
// isTTYErr, which already folds in --txt/TERM=dumb/CI=true/NO_COLOR — so any of those, or a
// piped stdin/stderr, skip the wizard and every existing anonymous flow behaves exactly as
// before.
func wizardShouldRun(hasAuth, jsonOut, stdinTTY, stderrTTY bool) bool {
	return !hasAuth && !jsonOut && stdinTTY && stderrTTY
}

func main() {
	args := os.Args[1:]
	var cmd string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			cmd = a
			break
		}
	}
	switch cmd {
	case "login":
		cmdLogin()
		return
	case "logout":
		cmdLogout()
		return
	case "whoami":
		cmdWhoami()
		return
	}

	jsonOut := argsHave(args, "--json")
	forceDomain := argsHave(args, "-d") || argsHave(args, "--domain")
	forceSearch := argsHave(args, "-k") || argsHave(args, "--keyword") || argsHave(args, "-p") || argsHave(args, "--phrase")
	help := argsHave(args, "--help") || argsHave(args, "-h")

	var words []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	input := strings.Join(words, " ")

	if input == "" || help {
		fmt.Println(helpText())
		if help {
			os.Exit(0)
		}
		os.Exit(1)
	}

	stdinTTY := isTerminal(os.Stdin)
	hasAuth := os.Getenv("AIR_API_KEY") != "" || readAuth() != nil
	if wizardShouldRun(hasAuth, jsonOut, stdinTTY, isTTYErr) {
		runSetupWizard()
	}
	autoUpgrade(stdinTTY && isTTYErr && !jsonOut)

	// A multi-word query ("best crm software") searches on the whole phrase —
	// normalizeHostname naturally rejects it as a domain (spaces don't parse as a URL
	// host), so a single-token domain still works unchanged.
	var host string
	if !forceSearch {
		host = normalizeHostname(input)
	}
	isSearch := forceSearch || (!forceDomain && host == "")

	if isSearch {
		result, err := fetchSearch(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s search failed: %s\n", red(FA["timesCircle"]), err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(result.Data)
		} else {
			var sr SearchResult
			_ = json.Unmarshal(result.Data, &sr)
			fmt.Println(renderSearch(input, sr))
		}
		return
	}

	if host == "" {
		fmt.Fprintf(os.Stderr, "%s %q is not a supported hostname\n", red(FA["timesCircle"]), input)
		os.Exit(1)
	}

	result, pending, err := lookup(host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s lookup failed: %s\n", red(FA["timesCircle"]), err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(result.Data)
	} else if pending {
		var pd struct {
			Hostname string `json:"hostname"`
		}
		_ = json.Unmarshal(result.Data, &pd)
		if pd.Hostname == "" {
			pd.Hostname = host
		}
		fmt.Println(renderPending(pd.Hostname))
	} else {
		var d Domain
		_ = json.Unmarshal(result.Data, &d)
		fmt.Println(render(d, result.DatasetVersion))
	}
	if pending {
		os.Exit(2)
	}
}

func printJSON(raw json.RawMessage) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		fmt.Println(string(raw))
		return
	}
	fmt.Println(buf.String())
}

func helpText() string {
	return fmt.Sprintf(`%s air %s [%s] [%s] [%s|%s]
       air %s    log in via API key or browser
       air %s   remove the local saved token
       air %s   show who you're logged in as

%s`,
		bold("usage:"), cyan("<domain|keyword>"), cyan("--json"), cyan("--txt"), cyan("-d"), cyan("-k"),
		bold("login"), bold("logout"), bold("whoami"),
		dim(`Looks up a domain's AIR score — cached results return instantly, a
never-seen domain is gathered on the spot (polls until ready, ~1-3 min).
An argument that isn't a domain searches domains/brands/phrases instead.
Force either with -d/--domain or -k/--keyword (alias -p/--phrase).

  --json   machine-readable output
  --txt    plain text — no color, no icons (same as TERM=dumb, CI=true)

Logging in lifts the anonymous rate limit; AIR_API_KEY env overrides any
saved login. Exit 2: domain is still being gathered, re-run in a bit.
Exit 1: hard failure, including a rate-limit that outlasted the deadline.`))
}

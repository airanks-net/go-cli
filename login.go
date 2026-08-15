package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func loginWithKey(key string) {
	if key == "" {
		fmt.Fprintf(os.Stderr, "%s no key entered\n", red(FA["times"]))
		os.Exit(1)
	}
	res, err := apiFetch(http.MethodGet, userURL, nil, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", red(FA["times"]), err)
		os.Exit(1)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		fmt.Fprintf(os.Stderr, "%s that key was rejected (401) — check it and try again\n", red(FA["times"]))
		os.Exit(1)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "%s %s\n", red(FA["times"]), apiErrorMessage(res))
		os.Exit(1)
	}
	body, _ := io.ReadAll(res.Body)
	user := extractUser(body)
	host, _ := url.Parse(userURL)
	if err := saveAuth(AuthFile{
		Token:     key,
		Host:      host.Host,
		User:      user,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not save login: %s\n", red(FA["times"]), err)
		os.Exit(1)
	}
	fmt.Printf("%s logged in as %s\n", green(FA["check"]), bold(displayName(user)))
}

func openBrowser(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	_ = cmd.Start() // URL is already printed — nothing else to do if this fails.
}

func loginWithBrowser() {
	verifier := generateVerifier()
	challenge := pkceChallenge(verifier)
	res, err := apiFetch(http.MethodPost, apiBase+"/cli/auth", map[string]string{"code_challenge": challenge}, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s could not start browser login: %s\n", red(FA["times"]), err)
		os.Exit(1)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "%s could not start browser login (%d)\n", red(FA["times"]), res.StatusCode)
		os.Exit(1)
	}
	var start struct {
		Data struct {
			Nonce            string `json:"nonce"`
			BrowserURL       string `json:"browser_url"`
			ConfirmationCode string `json:"confirmation_code"`
			Interval         int    `json:"interval"`
			ExpiresIn        int    `json:"expires_in"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&start); err != nil {
		fmt.Fprintf(os.Stderr, "%s could not start browser login: %s\n", red(FA["times"]), err)
		os.Exit(1)
	}
	d := start.Data

	fmt.Printf("\n  Enter this code at %s: %s\n\n", d.BrowserURL, bold(d.ConfirmationCode))

	headless := runtime.GOOS == "linux" && os.Getenv("DISPLAY") == ""
	remote := os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
	if headless || remote {
		fmt.Println("Open this URL on any device to continue.")
	} else if sameOrigin(d.BrowserURL, apiBase) {
		openBrowser(d.BrowserURL)
	} else {
		fmt.Fprintf(os.Stderr, "%s refusing to auto-open a cross-origin URL — open it yourself if you trust it.\n", yellow(FA["warn"]))
	}

	deadline := time.Now().Add(time.Duration(d.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(d.Interval) * time.Second)
		pollRes, err := apiFetch(http.MethodPost, apiBase+"/cli/auth/poll", map[string]string{"nonce": d.Nonce, "code_verifier": verifier}, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s poll failed: %s\n", red(FA["times"]), err)
			os.Exit(1)
		}
		if pollRes.StatusCode == http.StatusTooManyRequests {
			ra, convErr := strconv.Atoi(pollRes.Header.Get("Retry-After"))
			if convErr != nil || ra < 1 {
				ra = d.Interval
			}
			remaining := time.Until(deadline)
			wait := time.Duration(ra) * time.Second
			if wait > remaining {
				wait = remaining
			}
			if wait < 0 {
				wait = 0
			}
			fmt.Fprintf(os.Stderr, "%s rate limited — retrying in %ds\n", yellow(FA["warn"]), int(math.Ceil(wait.Seconds())))
			pollRes.Body.Close()
			time.Sleep(wait)
			continue
		}
		body, _ := io.ReadAll(pollRes.Body)
		pollRes.Body.Close()
		// not_found rides a 404 with a normal envelope, so read the body before deciding
		// this was a transport failure.
		var poll struct {
			Data struct {
				Status    string    `json:"status"`
				Token     string    `json:"token"`
				User      *AuthUser `json:"user"`
				ExpiresAt *string   `json:"expires_at"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &poll); err != nil || poll.Data.Status == "" {
			fmt.Fprintf(os.Stderr, "%s poll failed (%d)\n", red(FA["times"]), pollRes.StatusCode)
			os.Exit(1)
		}
		switch poll.Data.Status {
		case "pending":
			msg := fmt.Sprintf("%s waiting for confirmation in the browser...", FA["hourglass"])
			if isTTYErr {
				fmt.Fprintf(os.Stderr, "%s\r", cyan(msg))
			} else {
				fmt.Fprintf(os.Stderr, "%s\n", msg)
			}
			continue
		case "ok":
			host, _ := url.Parse(apiBase)
			if err := saveAuth(AuthFile{
				Token:     poll.Data.Token,
				Host:      host.Host,
				User:      poll.Data.User,
				ExpiresAt: poll.Data.ExpiresAt,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}); err != nil {
				fmt.Fprintf(os.Stderr, "%s could not save login: %s\n", red(FA["times"]), err)
				os.Exit(1)
			}
			if isTTYErr {
				fmt.Fprintln(os.Stderr)
			}
			fmt.Printf("%s logged in as %s\n", green(FA["check"]), bold(displayName(poll.Data.User)))
			return
		case "forbidden":
			fmt.Fprintf(os.Stderr, "%s verifier mismatch — restart login\n", red(FA["times"]))
			os.Exit(1)
		default:
			messages := map[string]string{"denied": "login denied", "expired": "login expired", "not_found": "login request not found"}
			msg, ok := messages[poll.Data.Status]
			if !ok {
				msg = poll.Data.Status
			}
			fmt.Fprintf(os.Stderr, "%s %s\n", red(FA["times"]), msg)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "%s login timed out — run \"air login\" again\n", red(FA["times"]))
	os.Exit(1)
}

// readMasked mutes terminal echo for one line via `stty -echo`/`stty echo` — the standard
// zero-dep masking trick. Reuses the caller's *bufio.Reader (a second one on the same
// stdin would drop already-buffered input, e.g. piped answers).
func readMasked(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	hadTTY := off.Run() == nil
	if hadTTY {
		defer func() {
			on := exec.Command("stty", "echo")
			on.Stdin = os.Stdin
			_ = on.Run()
		}()
	}
	line, _ := reader.ReadString('\n')
	fmt.Println()
	return strings.TrimSpace(line)
}

// authChoicePrompt is the one place the "how would you like to authenticate" text lives —
// both `air login` and the first-run wizard read through it. anon adds the wizard-only
// "continue without signing in" escape hatch so the wizard is never a hard gate.
func authChoicePrompt(reader *bufio.Reader, anon bool) string {
	prompt := "How would you like to authenticate?\n  1) Paste an API key\n  2) Log in with your browser"
	if anon {
		prompt += "\n  3) Continue without signing in"
	}
	fmt.Println(prompt)
	fmt.Print("> ")
	choice, _ := reader.ReadString('\n')
	return strings.TrimSpace(choice)
}

func cmdLogin() {
	reader := bufio.NewReader(os.Stdin)
	switch authChoicePrompt(reader, false) {
	case "1":
		key := readMasked(reader, "API key: ")
		loginWithKey(key)
	case "2":
		loginWithBrowser()
	default:
		fmt.Fprintf(os.Stderr, "%s invalid choice\n", red(FA["times"]))
		os.Exit(1)
	}
}

// runSetupWizard fires once, on the first interactive invocation with no saved credential
// (see wizardShouldRun in main.go). Picking 1/2 runs the exact same login as `air login`;
// picking 3 (or anything else) writes an anonymous marker so later runs never ask again.
func runSetupWizard() {
	reader := bufio.NewReader(os.Stdin)
	switch authChoicePrompt(reader, true) {
	case "1":
		key := readMasked(reader, "API key: ")
		loginWithKey(key)
	case "2":
		loginWithBrowser()
	default:
		if err := saveAuth(AuthFile{Anonymous: true, CreatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
			fmt.Fprintf(os.Stderr, "%s could not save setup choice: %s\n", red(FA["times"]), err)
			return
		}
		fmt.Fprintf(os.Stderr, "%s continuing without signing in — run \"air login\" anytime\n", dim(FA["hourglass"]))
	}
}

func cmdLogout() {
	if deleteAuth() {
		fmt.Printf("%s logged out (local only — the token stays valid server-side until it expires)\n", green(FA["check"]))
	} else {
		fmt.Println("not logged in")
	}
}

func cmdWhoami() {
	resolved := resolveToken()
	if resolved.Token == "" {
		fmt.Println("not logged in (anonymous tier)")
		return
	}
	token := ""
	if shouldAttachToken(userURL, resolved) {
		token = resolved.Token
	}
	res, err := apiFetch(http.MethodGet, userURL, nil, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s\n", red(FA["times"]), err)
		os.Exit(1)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		fmt.Fprintf(os.Stderr, "%s token invalid or revoked — run \"air login\"\n", red(FA["times"]))
		os.Exit(1)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "%s %s\n", red(FA["times"]), apiErrorMessage(res))
		os.Exit(1)
	}
	body, _ := io.ReadAll(res.Body)
	user := extractUser(body)
	tierNote := "token tier"
	if resolved.Source == "env" {
		tierNote = "token tier — via AIR_API_KEY"
	}
	expiryNote := ""
	if resolved.ExpiresAt != nil && *resolved.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, *resolved.ExpiresAt); err == nil {
			days := int(math.Ceil(time.Until(t).Hours() / 24))
			if days < 0 {
				days = 0
			}
			expiryNote = dim(fmt.Sprintf("  · expires in %dd", days))
		}
	}
	fmt.Printf("%s %s <%s>  %s%s\n", green(FA["check"]), bold(displayName(user)), user.Email, dim("("+tierNote+")"), expiryNote)
}

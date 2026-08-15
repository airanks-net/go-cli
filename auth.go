package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// Auth file shape is IDENTICAL across every AIR client — node, rust, go, and the
// composer package all read/write ~/.config/air/auth.json so `air login` in any one
// of them authenticates all the others. Do not change field names or the path.

type AuthUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type AuthFile struct {
	Token     string    `json:"token"`
	Host      string    `json:"host"`
	User      *AuthUser `json:"user,omitempty"`
	ExpiresAt *string   `json:"expires_at"`
	CreatedAt string    `json:"created_at"`
	// Anonymous marks a first-run wizard "continue without signing in" choice — the file
	// exists (so the wizard never asks again) but carries no Token, so resolveToken below
	// already falls through to the anonymous tier on its own.
	Anonymous bool `json:"anonymous,omitempty"`
}

func authPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "air", "auth.json")
}

func readAuth() *AuthFile {
	b, err := os.ReadFile(authPath())
	if err != nil {
		return nil
	}
	var a AuthFile
	if err := json.Unmarshal(b, &a); err != nil {
		return nil
	}
	return &a
}

// saveAuth writes atomically (tmp file + rename) at 0600, dir at 0700 — same as node-cli.
func saveAuth(a AuthFile) error {
	dir := filepath.Dir(authPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", authPath(), os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, authPath())
}

func deleteAuth() bool {
	return os.Remove(authPath()) == nil
}

type ResolvedToken struct {
	Token     string
	Source    string // "env" | "file" | "anonymous"
	Host      string
	User      *AuthUser
	ExpiresAt *string
}

// resolveToken: AIR_API_KEY env > saved auth file > anonymous. Identical order to every
// other AIR client.
func resolveToken() ResolvedToken {
	if v := os.Getenv("AIR_API_KEY"); v != "" {
		return ResolvedToken{Token: v, Source: "env"}
	}
	if a := readAuth(); a != nil && a.Token != "" {
		return ResolvedToken{Token: a.Token, Source: "file", Host: a.Host, User: a.User, ExpiresAt: a.ExpiresAt}
	}
	return ResolvedToken{Source: "anonymous"}
}

// shouldAttachToken: a file-sourced token only rides to requests whose URL host equals
// the saved host; an env token attaches unconditionally. Stops a saved token leaking if
// AIR_API_BASE ever points somewhere else than where the user logged in.
func shouldAttachToken(rawURL string, r ResolvedToken) bool {
	if r.Token == "" {
		return false
	}
	if r.Source == "env" {
		return true
	}
	if r.Host == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

func sameOrigin(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

func displayName(u *AuthUser) string {
	if u == nil {
		return "you"
	}
	if u.Name != "" {
		return u.Name
	}
	if u.Email != "" {
		return u.Email
	}
	return "you"
}

// extractUser handles both {data:{...}} and a bare {...} user body.
func extractUser(body []byte) *AuthUser {
	var wrap struct {
		Data *AuthUser `json:"data"`
	}
	_ = json.Unmarshal(body, &wrap)
	if wrap.Data != nil {
		return wrap.Data
	}
	var u AuthUser
	_ = json.Unmarshal(body, &u)
	return &u
}

// ---- PKCE (RFC 7636 S256) ----

func base64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func generateVerifier() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64url(b)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64url(sum[:])
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const cliVersion = "1.0.0"

// RFC 9110 product-token + contact-URL convention — a UA-less request 403s server-side.
const userAgent = "air-go/" + cliVersion + " (+https://airanks.net)"
const fetchTimeout = 10 * time.Second

var apiBase = envOr("AIR_API_BASE", "https://airanks.net/api/v1")

// Contract: GET {base without trailing /v1}/user, e.g. https://airanks.net/api/user.
var userURL = strings.TrimSuffix(apiBase, "/v1") + "/user"

var httpClient = &http.Client{Timeout: fetchTimeout}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

type apiEnvelope struct {
	Data json.RawMessage `json:"data"`
	Meta struct {
		DatasetVersion *int `json:"dataset_version"`
	} `json:"meta"`
}

type fetchResult struct {
	Data               json.RawMessage
	DatasetVersion     *int
	RateLimitedSeconds int
}

func apiFetch(method, urlStr string, body any, token string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, urlStr, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return httpClient.Do(req)
}

// Server error envelope is {"error":{"code":...,"message":...}}; Laravel validation
// failures (422) ship {message, errors} instead — fall back to the top-level message
// before a generic status line. Consumes res.Body.
func apiErrorMessage(res *http.Response) string {
	var body struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	b, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(b, &body)
	if body.Error != nil && body.Error.Message != "" {
		return body.Error.Message
	}
	if body.Message != "" {
		return body.Message
	}
	return fmt.Sprintf("API returned %d", res.StatusCode)
}

func retryAfterSeconds(res *http.Response) int {
	n, err := strconv.Atoi(res.Header.Get("Retry-After"))
	if err != nil || n < 1 {
		return 30
	}
	return n
}

func fetchDomain(host string) (*fetchResult, error) {
	resolved := resolveToken()
	u := apiBase + "/domains/" + host
	token := ""
	if shouldAttachToken(u, resolved) {
		token = resolved.Token
	}
	res, err := apiFetch(http.MethodGet, u, nil, token)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return &fetchResult{RateLimitedSeconds: retryAfterSeconds(res)}, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", apiErrorMessage(res))
	}
	var env apiEnvelope
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return nil, err
	}
	return &fetchResult{Data: env.Data, DatasetVersion: env.Meta.DatasetVersion}, nil
}

func fetchSearch(q string) (*fetchResult, error) {
	resolved := resolveToken()
	u := apiBase + "/search?q=" + url.QueryEscape(q)
	token := ""
	if shouldAttachToken(u, resolved) {
		token = resolved.Token
	}
	res, err := apiFetch(http.MethodGet, u, nil, token)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", apiErrorMessage(res))
	}
	var env apiEnvelope
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return nil, err
	}
	return &fetchResult{Data: env.Data, DatasetVersion: env.Meta.DatasetVersion}, nil
}

func isPending(data json.RawMessage) bool {
	var p struct {
		AIFiles struct {
			Status string `json:"status"`
		} `json:"ai_files"`
	}
	_ = json.Unmarshal(data, &p)
	return p.AIFiles.Status == "pending"
}

// lookup polls a single wall-clock deadline for the whole call — pending polls AND 429
// sleeps both count against it, and Retry-After is clamped to whatever budget is left.
// Returns (result, pendingAtCap, err): pendingAtCap true means the deadline hit while
// the domain was still gathering (caller exits 2); err set means a hard failure,
// including a rate-limit that outlived the deadline (caller exits 1).
func lookup(host string) (*fetchResult, bool, error) {
	pollMs := envIntOr("AIR_POLL_MS", 20_000)
	pollMaxMs := envIntOr("AIR_POLL_MAX_MS", 180_000)
	deadline := time.Now().Add(time.Duration(pollMaxMs) * time.Millisecond)
	for {
		result, err := fetchDomain(host)
		if err != nil {
			return nil, false, err
		}
		if result.RateLimitedSeconds > 0 {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil, false, fmt.Errorf("rate limited — hit the %ds lookup deadline while still throttled; try again in a bit", pollMaxMs/1000)
			}
			wait := time.Duration(result.RateLimitedSeconds) * time.Second
			if wait > remaining {
				wait = remaining
			}
			fmt.Fprintf(os.Stderr, "%s rate limited — retrying in %ds\n", rgbColor(amber, FA["warn"], ""), int(math.Ceil(wait.Seconds())))
			time.Sleep(wait)
			continue
		}
		pending := isPending(result.Data)
		remaining := time.Until(deadline)
		if !pending || remaining <= 0 {
			return result, pending, nil
		}
		wait := time.Duration(pollMs) * time.Millisecond
		if wait > remaining {
			wait = remaining
		}
		pollWait(host, wait)
	}
}

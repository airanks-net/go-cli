package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --txt (also TERM=dumb / CI=true) is the full plain path: no ANSI, no icons.
// NO_COLOR is narrower — color only, icons stay.
var textMode = argsHave(os.Args[1:], "--txt") || os.Getenv("TERM") == "dumb" || os.Getenv("CI") == "true"
var plainEnv = os.Getenv("NO_COLOR") != "" || textMode
var isTTYOut = isTerminal(os.Stdout) && !plainEnv
var isTTYErr = isTerminal(os.Stderr) && !plainEnv

func argsHave(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// ponytail: os.ModeCharDevice is the standard zero-dep isatty heuristic (no ioctl syscalls).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// FA glyphs are Font Awesome codepoints (render with any Nerd Font / FA-patched terminal
// font) — same table as node-cli. Blanked out entirely in text mode.
var FA = map[string]string{
	"globe": "", "trophy": "", "chart": "", "check": "",
	"times": "", "file": "", "hourglass": "", "hourglassHalf": "",
	"shield": "", "warn": "", "checkCircle": "", "timesCircle": "",
	"database": "", "link": "", "eyeSlash": "", "tag": "", "quote": "",
}

func init() {
	if textMode {
		for k := range FA {
			FA[k] = ""
		}
	}
}

func ansi(code, s string) string {
	if isTTYOut {
		return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, s)
	}
	return s
}
func bold(s string) string   { return ansi("1", s) }
func dim(s string) string    { return ansi("2", s) }
func italic(s string) string { return ansi("3", s) }
func cyan(s string) string   { return ansi("36", s) }
func green(s string) string  { return ansi("32", s) }
func yellow(s string) string { return ansi("33", s) }
func red(s string) string    { return ansi("31", s) }

// Truecolor score ramp: 0 red -> 5 amber -> 10 green.
var scoreStops = [3][3]int{{255, 95, 95}, {255, 209, 102}, {90, 247, 142}}
var amber = [3]int{255, 209, 102}

func scoreRGB(score float64) [3]int {
	t := math.Min(math.Max(score/10, 0), 1)
	var a, b [3]int
	var k float64
	if t < 0.5 {
		a, b, k = scoreStops[0], scoreStops[1], t*2
	} else {
		a, b, k = scoreStops[1], scoreStops[2], t*2-1
	}
	var out [3]int
	for i := range out {
		out[i] = int(math.Round(float64(a[i]) + float64(b[i]-a[i])*k))
	}
	return out
}

func rgbColor(rgb [3]int, s, extra string) string {
	if isTTYOut {
		return fmt.Sprintf("\x1b[%s38;2;%d;%d;%dm%s\x1b[0m", extra, rgb[0], rgb[1], rgb[2], s)
	}
	return s
}

// 10-cell gauge, each filled cell its own gradient step; TTY-only.
func gauge(score float64) string {
	if !isTTYOut {
		return ""
	}
	var sb strings.Builder
	filled := int(math.Round(score))
	for i := 0; i < 10; i++ {
		if i < filled {
			sb.WriteString(rgbColor(scoreRGB(float64(i)+0.5), "█", ""))
		} else {
			sb.WriteString("\x1b[38;5;238m░\x1b[0m")
		}
	}
	return "  " + sb.String()
}

// OSC 8 hyperlink — clickable in iTerm2/kitty/Ghostty/WezTerm; plain when piped.
func hyperlink(url, label string) string {
	if isTTYOut {
		return fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", url, label)
	}
	return label
}

// ponytail: no ioctl TIOCGWINSZ (would need a dep or per-OS syscalls) — COLUMNS env or
// an 80-col guess. Add real width detection if narrow-terminal wrapping complaints show up.
func contentWidth() int {
	w := 80
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			w = n
		}
	}
	cw := w - 4
	if cw > 74 {
		cw = 74
	}
	return cw
}

func rule() string {
	if !isTTYOut {
		return ""
	}
	return "  \x1b[38;5;240m" + strings.Repeat("─", contentWidth()) + "\x1b[0m"
}

func pctPaint(p float64, label string) string {
	if !isTTYOut {
		return "  ·  " + label
	}
	top := (1 - p) * 100
	var painted string
	switch {
	case top <= 10:
		painted = rgbColor([3]int{168, 85, 247}, label, "")
	case top <= 25:
		painted = rgbColor([3]int{34, 211, 238}, label, "")
	default:
		painted = dim(label)
	}
	return dim("  ·  ") + painted
}

var verdictRGB = map[string][3]int{
	"allowed": {90, 247, 142}, "allow": {90, 247, 142},
	"partial": {250, 173, 60},
	"blocked": {255, 95, 95}, "block": {255, 95, 95}, "disallowed": {255, 95, 95},
}

func percentileLabel(p *float64) string {
	if p == nil {
		return ""
	}
	v := int(math.Max(1, math.Round((1-*p)*100)))
	return fmt.Sprintf("top %d%%", v)
}

func wrapText(text string, width int, indent string) string {
	words := strings.Fields(text)
	var lines []string
	line := ""
	for _, w := range words {
		if line != "" && len(line)+len(w)+1 > width {
			lines = append(lines, line)
			line = w
		} else if line == "" {
			line = w
		} else {
			line = line + " " + w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

var newlinesRe = regexp.MustCompile(`\n+`)
var sentenceEndRe = regexp.MustCompile(`[.!?](\s|$)`)

// Live "spinner" simplified to a single status line + sleep (no ANSI redraw loop) — the
// information is identical, just no per-frame animation.
func pollWait(host string, wait time.Duration) {
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", cyan(FA["hourglass"]), dim(fmt.Sprintf("gathering %s — next check in %ds", host, secs)))
	time.Sleep(wait)
}

type AIFiles struct {
	Status         string            `json:"status"`
	LlmsTxt        bool              `json:"llms_txt"`
	LlmsFullTxt    bool              `json:"llms_full_txt"`
	AiTxt          bool              `json:"ai_txt"`
	RobotsTxt      bool              `json:"robots_txt"`
	JSONLD         bool              `json:"json_ld"`
	JSONLDTypes    []string          `json:"json_ld_types"`
	RobotsAIAgents map[string]string `json:"robots_ai_agents"`
}

type Domain struct {
	Hostname     string   `json:"hostname"`
	AirScore     float64  `json:"air_score"`
	Percentile   *float64 `json:"percentile"`
	Tracked      bool     `json:"tracked"`
	Occurrences  int      `json:"occurrences"`
	PhrasesCount int      `json:"phrases_count"`
	BrandsCount  int      `json:"brands_count"`
	Summary      *string  `json:"summary"`
	AIFiles      AIFiles  `json:"ai_files"`
}

func render(d Domain, datasetVersion *int) string {
	var out []string
	col := scoreRGB(d.AirScore)
	pct := percentileLabel(d.Percentile)
	pctPart := ""
	if pct != "" {
		pctPart = pctPaint(*d.Percentile, pct)
	}
	out = append(out, "")
	out = append(out, fmt.Sprintf("  %s %s   %s%s%s",
		cyan(FA["globe"]), bold(d.Hostname),
		rgbColor(col, fmt.Sprintf("%s AIR %v/10", FA["trophy"], d.AirScore), "1;"),
		gauge(d.AirScore), pctPart))
	out = append(out, "")

	trackedIcon := FA["eyeSlash"]
	if d.Tracked {
		trackedIcon = FA["chart"]
	}
	trackedText := dim("not tracked yet — no AI answer has cited this domain")
	if d.Tracked {
		trackedText = fmt.Sprintf("tracked — %s occurrences %s %s phrases %s %s brands",
			bold(strconv.Itoa(d.Occurrences)), dim("·"), bold(strconv.Itoa(d.PhrasesCount)), dim("·"), bold(strconv.Itoa(d.BrandsCount)))
	}
	out = append(out, fmt.Sprintf("  %s %s", cyan(trackedIcon), trackedText))

	f := d.AIFiles
	if f.Status == "pending" {
		out = append(out, fmt.Sprintf("  %s %s — re-run in a minute", rgbColor(amber, FA["hourglassHalf"], ""), rgbColor(amber, "still gathering", "")))
	} else if f.Status != "" {
		mark := func(v bool) string {
			if textMode {
				if v {
					return "yes"
				}
				return "no"
			}
			if v {
				return rgbColor([3]int{90, 247, 142}, FA["checkCircle"], "")
			}
			return dim(FA["timesCircle"])
		}
		typesNote := ""
		if len(f.JSONLDTypes) > 0 {
			typesNote = dim(" (" + strings.Join(f.JSONLDTypes, ", ") + ")")
		}
		out = append(out, fmt.Sprintf("  %s llms.txt %s  llms-full %s  ai.txt %s  robots.txt %s  json-ld %s%s",
			cyan(FA["file"]), mark(f.LlmsTxt), mark(f.LlmsFullTxt), mark(f.AiTxt), mark(f.RobotsTxt), mark(f.JSONLD), typesNote))

		if len(f.RobotsAIAgents) > 0 {
			counts := map[string]int{}
			for _, v := range f.RobotsAIAgents {
				counts[v]++
			}
			keys := make([]string, 0, len(counts))
			for k := range counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, v := range keys {
				label := fmt.Sprintf("%d %s", counts[v], v)
				if rgb, ok := verdictRGB[v]; ok {
					parts = append(parts, rgbColor(rgb, label, ""))
				} else {
					parts = append(parts, label)
				}
			}
			out = append(out, fmt.Sprintf("  %s ai crawlers: %s %s", cyan(FA["shield"]), strings.Join(parts, dim(" · ")), dim(fmt.Sprintf("of %d", len(f.RobotsAIAgents)))))
		}
	}

	if d.Summary != nil && *d.Summary != "" {
		out = append(out, "")
		cleaned := newlinesRe.ReplaceAllString(*d.Summary, " ")
		wrapped := wrapText(cleaned, contentWidth(), "  ")
		cut := len(wrapped)
		if loc := sentenceEndRe.FindStringIndex(wrapped); loc != nil {
			cut = loc[0] + 1
		}
		out = append(out, italic(wrapped[:cut])+wrapped[cut:])
	}

	out = append(out, "")
	if sep := rule(); sep != "" {
		out = append(out, sep)
	}
	pageURL := fmt.Sprintf("https://airanks.net/page?d=%s", d.Hostname)
	dsNote := ""
	if datasetVersion != nil {
		dsNote = dim(fmt.Sprintf("  ·  %s dataset %d", FA["database"], *datasetVersion))
	}
	out = append(out, fmt.Sprintf("  %s %s%s", dim(FA["link"]), dim(hyperlink(pageURL, fmt.Sprintf("airanks.net/page?d=%s", d.Hostname))), dsNote))
	out = append(out, "")
	return strings.Join(out, "\n")
}

// Pending-at-cap honesty: the server casts a not-yet-hydrated air_score to 0 (int), so
// printing the normal score headline here would read as a measured zero, not "we don't
// know yet". Render the gathering state instead; main() exits 2.
func renderPending(hostname string) string {
	var out []string
	out = append(out, "")
	out = append(out, fmt.Sprintf("  %s %s", cyan(FA["globe"]), bold(hostname)))
	out = append(out, "")
	out = append(out, fmt.Sprintf("  %s %s %s", rgbColor(amber, FA["hourglassHalf"], ""), rgbColor(amber, "still gathering", ""), dim("— first look can take under a minute or a few hours")))
	out = append(out, fmt.Sprintf("  %s", dim("re-run in a minute or two, or check back later")))
	out = append(out, "")
	return strings.Join(out, "\n")
}

type SearchHit struct {
	Hostname string  `json:"hostname,omitempty"`
	Name     string  `json:"name,omitempty"`
	Text     string  `json:"text,omitempty"`
	AirScore float64 `json:"air_score"`
}

type SearchResult struct {
	Domains []SearchHit `json:"domains"`
	Brands  []SearchHit `json:"brands"`
	Phrases []SearchHit `json:"phrases"`
}

func renderSearch(q string, data SearchResult) string {
	type bucket struct {
		label string
		icon  string
		hits  []SearchHit
		name  func(SearchHit) string
	}
	buckets := []bucket{
		{"domains", FA["globe"], data.Domains, func(h SearchHit) string { return h.Hostname }},
		{"brands", FA["tag"], data.Brands, func(h SearchHit) string { return h.Name }},
		{"phrases", FA["quote"], data.Phrases, func(h SearchHit) string { return h.Text }},
	}

	out := []string{"", fmt.Sprintf("  %s", dim(fmt.Sprintf("search %q", q)))}

	total := len(data.Domains) + len(data.Brands) + len(data.Phrases)
	if total == 0 {
		out = append(out, "", fmt.Sprintf("  %s air -d <domain>", dim("no matches — try a broader term, or run")), "")
		return strings.Join(out, "\n")
	}

	anyCapped := false
	for _, b := range buckets {
		if len(b.hits) == 0 {
			continue
		}
		if len(b.hits) >= 5 {
			anyCapped = true
		}
		out = append(out, "", fmt.Sprintf("  %s %s", cyan(b.icon), bold(b.label)))
		for _, h := range b.hits {
			out = append(out, fmt.Sprintf("    %s  %s", b.name(h), rgbColor(scoreRGB(h.AirScore), fmt.Sprintf("AIR %v/10", h.AirScore), "")))
		}
	}
	out = append(out, "")
	if anyCapped {
		out = append(out, fmt.Sprintf("  %s air -d <domain>\n", dim("refine your search or run")))
	}
	return strings.Join(out, "\n")
}

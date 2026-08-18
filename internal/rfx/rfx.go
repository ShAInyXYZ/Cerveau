// Package rfx implements the RFX reflex manifest (docs-private/RFX.md, spec v1):
// parsing, validation, and discovery of ~/.crv/rfx/*.rfx.yaml files.
//
// A reflex is a declarative capability — composed from tools the harness
// already guards. No prose from a reflex ever enters the context window;
// the model sees a name, a description, and a schema.
package rfx

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const SpecVersion = 1

// MaxPanelBytes caps a pack's ui/panel.html. The file is served whole into
// the sandboxed panel iframe; a cap keeps a broken pack from shipping a
// gigabyte of "UI".
const MaxPanelBytes = 512 * 1024

const (
	KindPipeline = "pipeline"
	KindExec     = "exec"
)

const (
	RiskSafe      = "safe"
	RiskSensitive = "sensitive"
	RiskDangerous = "dangerous"
)

var Modes = []string{"discussion", "brainstorming", "autopilot"}

// stringList accepts either a scalar or a sequence in YAML
// (network: none  ≡  network: [none]).
type stringList []string

func (s *stringList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*s = []string{value.Value}
		return nil
	}
	var out []string
	if err := value.Decode(&out); err != nil {
		return err
	}
	*s = out
	return nil
}

// Card is the capability card (spec §5): declared permissions, enforced in
// Go at install and dispatch. Metadata, never prompt text.
type Card struct {
	FS         stringList `yaml:"fs"`         // "workspace" | "none" | extra absolute read-only paths
	Network    stringList `yaml:"network"`    // "none" | "any" | host allowlist
	Env        []string   `yaml:"env"`        // env var names visible to exec steps
	Subprocess bool       `yaml:"subprocess"` // required by kind: exec
}

// DefaultCard is the most restrictive card (spec §2): what a reflex gets
// when it declares none.
func DefaultCard() Card {
	return Card{FS: []string{"workspace"}, Network: []string{"none"}, Env: []string{}, Subprocess: false}
}

// Contract is the fuzz contract (spec §6): what "passing crv rfx test" means.
type Contract struct {
	MaxMs          int      `yaml:"max_ms"`
	OutputRegex    string   `yaml:"output_regex"`
	MustNotContain []string `yaml:"must_not_contain"`
}

// Step is one pipeline step (spec §3): a single-key map of tool name → args,
// plus optional id / when / optional meta keys.
type Step struct {
	ID       string
	When     string
	Optional bool
	Tool     string
	Args     any // string for bash; map[string]any otherwise
}

var stepMetaKeys = map[string]bool{"id": true, "when": true, "optional": true}

func (s *Step) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("step must be a map")
	}
	var raw map[string]yaml.Node
	if err := value.Decode(&raw); err != nil {
		return err
	}
	tool := ""
	for k := range raw {
		if stepMetaKeys[k] {
			continue
		}
		if tool != "" {
			return fmt.Errorf("step has two tool keys (%q and %q) — one tool per step", tool, k)
		}
		tool = k
	}
	if tool == "" {
		return fmt.Errorf("step has no tool key")
	}
	s.Tool = tool
	argsNode := raw[tool]
	var args any
	if err := argsNode.Decode(&args); err != nil {
		return fmt.Errorf("step %q args: %w", tool, err)
	}
	s.Args = args
	if n, ok := raw["id"]; ok {
		if err := n.Decode(&s.ID); err != nil {
			return fmt.Errorf("step id: %w", err)
		}
	}
	if n, ok := raw["when"]; ok {
		if err := n.Decode(&s.When); err != nil {
			return fmt.Errorf("step when: %w", err)
		}
	}
	if n, ok := raw["optional"]; ok {
		if err := n.Decode(&s.Optional); err != nil {
			return fmt.Errorf("step optional: %w", err)
		}
	}
	return nil
}

// Reflex is one parsed .rfx.yaml manifest.
type Reflex struct {
	RFX         int            `yaml:"rfx"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Kind        string         `yaml:"kind"`
	Risk        string         `yaml:"risk"`
	Modes       []string       `yaml:"modes"` // nil = all modes (registry semantics)
	IngressCap  *int           `yaml:"ingress_cap"`
	Params      map[string]any `yaml:"params"`
	Card        Card           `yaml:"card"`
	Contract    Contract       `yaml:"contract"`
	Steps       []Step         `yaml:"steps"`
	Argv        []string       `yaml:"argv"`
	Timeout     string         `yaml:"timeout"`

	Path string `yaml:"-"`
	Pack string `yaml:"-"` // pack name when loaded from a pack folder, "" = standalone
}

// Cap returns the effective ingress cap (spec §2): unset = 4000, 0 = uncapped.
func (r *Reflex) Cap() int {
	if r.IngressCap == nil {
		return 4000
	}
	return *r.IngressCap
}

var nameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// placeholder: {{ params.NAME }} or {{ steps.ID.output }}
var placeholderRe = regexp.MustCompile(`\{\{\s*(params\.[A-Za-z0-9_-]+|steps\.[a-z0-9-]+\.output)\s*\}\}`)

// anyBrace catches malformed placeholders like {{ paramx.foo }} or {{ }}.
var anyBraceRe = regexp.MustCompile(`\{\{[^}]*\}\}`)

var whenRe = regexp.MustCompile(`^!?steps\.([a-z0-9-]+)\.(ok|failed)$`)

// EvalWhen evaluates the entire v1 conditional language (spec §3.3) against
// step statuses. statusFor returns (ok, defined) for a step id. Load-time
// validation has already rejected bad expressions; an error here means a
// runtime inconsistency and is surfaced loudly.
func EvalWhen(expr string, statusFor func(id string) (ok, defined bool)) (bool, error) {
	m := whenRe.FindStringSubmatch(expr)
	if m == nil {
		return false, fmt.Errorf("when %q: not in the conditional language ([\"!\"]steps.ID.(ok|failed))", expr)
	}
	negated := strings.HasPrefix(expr, "!")
	id := m[1]
	wantOK := m[2] == "ok"
	ok, defined := statusFor(id)
	if !defined {
		return false, fmt.Errorf("when %q: step %q has no recorded status (skipped?)", expr, id)
	}
	result := ok == wantOK
	if negated {
		result = !result
	}
	return result, nil
}

// Parse decodes one manifest file. Validation lives in Validate; Parse only
// guarantees YAML structure decodes.
func Parse(data []byte, path string) (*Reflex, error) {
	var r Reflex
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // typos in field names are load-time errors, not silent drops
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r.Path = path
	// Spec §2 defaults: an undeclared card is the most restrictive one.
	if r.Card.FS == nil {
		r.Card.FS = []string{"workspace"}
	}
	if r.Card.Network == nil {
		r.Card.Network = []string{"none"}
	}
	return &r, nil
}

// KnownTool reports whether a step tool name exists in the registry. Injected
// by the caller so rfx stays free of a tools dependency (reflextool imports
// rfx, not the other way around).
type KnownTool func(name string) bool

// Validate enforces spec v1. Every rule here is a load-time rejection —
// nothing in this list may fail mid-run.
func Validate(r *Reflex, known KnownTool) error {
	if r.RFX != SpecVersion {
		return fmt.Errorf("rfx: must be %d (got %d) — unknown versions are rejected, never guessed", SpecVersion, r.RFX)
	}
	if r.Name == "" {
		return fmt.Errorf("name: required")
	}
	if len(r.Name) > 40 || !nameRe.MatchString(r.Name) {
		return fmt.Errorf("name %q: must match [a-z0-9-]+ and be ≤ 40 chars", r.Name)
	}
	if stem := fileStem(r.Path); stem != "" && stem != r.Name {
		return fmt.Errorf("name %q must equal filename stem %q (spec §1)", r.Name, stem)
	}
	if r.Description == "" {
		return fmt.Errorf("description: required — it is what the model sees to choose this tool")
	}
	if len(r.Description) > 200 {
		return fmt.Errorf("description: %d chars, max 200 — write it for a small-model reader", len(r.Description))
	}
	if r.Kind != KindPipeline && r.Kind != KindExec {
		return fmt.Errorf("kind %q: must be %q or %q", r.Kind, KindPipeline, KindExec)
	}
	switch r.Risk {
	case RiskSafe, RiskSensitive, RiskDangerous:
	case "":
		return fmt.Errorf("risk: required — no default, authors must decide")
	default:
		return fmt.Errorf("risk %q: must be safe, sensitive, or dangerous", r.Risk)
	}
	for _, m := range r.Modes {
		if !validMode(m) {
			return fmt.Errorf("modes: %q is not one of %s", m, strings.Join(Modes, ", "))
		}
	}
	if r.IngressCap != nil && *r.IngressCap < 0 {
		return fmt.Errorf("ingress_cap: must be ≥ 0")
	}
	if err := validateParams(r.Params); err != nil {
		return err
	}
	if err := validateCard(r); err != nil {
		return err
	}
	if r.Timeout != "" {
		d, err := time.ParseDuration(r.Timeout)
		if err != nil || d <= 0 {
			return fmt.Errorf("timeout %q: use a positive duration like 90s or 5m", r.Timeout)
		}
	}
	if r.Contract.MaxMs < 0 {
		return fmt.Errorf("contract.max_ms: must be ≥ 0")
	}

	switch r.Kind {
	case KindPipeline:
		if len(r.Steps) == 0 {
			return fmt.Errorf("kind pipeline: steps required (an alias is a one-step pipeline)")
		}
		if len(r.Argv) > 0 {
			return fmt.Errorf("kind pipeline: argv is only valid for kind exec")
		}
		return validateSteps(r, known)
	case KindExec:
		if len(r.Steps) > 0 {
			return fmt.Errorf("kind exec: steps are only valid for kind pipeline")
		}
		if len(r.Argv) == 0 {
			return fmt.Errorf("kind exec: argv required")
		}
		if !strings.HasPrefix(r.Argv[0], "/") {
			return fmt.Errorf("kind exec: argv[0] must be an absolute path (got %q)", r.Argv[0])
		}
		// The absolute-path guarantee is only real if it survives substitution:
		// "/usr/bin/{{ params.x }}" would let a param walk the path anywhere.
		if anyBraceRe.MatchString(r.Argv[0]) {
			return fmt.Errorf("kind exec: argv[0] may not contain placeholders — the binary path must be literal")
		}
		if !r.Card.Subprocess {
			return fmt.Errorf("kind exec requires card.subprocess: true (spec §4)")
		}
		return validatePlaceholders(r, strings.Join(r.Argv, " "))
	}
	return nil
}

func validateSteps(r *Reflex, known KnownTool) error {
	seenIDs := map[string]bool{}
	hasBash := false
	var refs []string
	for i, s := range r.Steps {
		where := fmt.Sprintf("steps[%d] (%s)", i, s.Tool)
		if known != nil && !known(s.Tool) {
			return fmt.Errorf("%s: unknown tool %q — step tools must exist in the registry", where, s.Tool)
		}
		if s.Tool == "bash" {
			hasBash = true
			if _, ok := s.Args.(string); !ok {
				return fmt.Errorf("%s: bash args must be a command string", where)
			}
		}
		if s.ID != "" {
			if !nameRe.MatchString(s.ID) {
				return fmt.Errorf("%s: id %q must match [a-z0-9-]+", where, s.ID)
			}
			if seenIDs[s.ID] {
				return fmt.Errorf("%s: duplicate step id %q", where, s.ID)
			}
		}
		if s.When != "" {
			m := whenRe.FindStringSubmatch(s.When)
			if m == nil {
				return fmt.Errorf("%s: when %q — the entire conditional language is [\"!\"]steps.ID.(ok|failed)", where, s.When)
			}
			if !seenIDs[m[1]] {
				return fmt.Errorf("%s: when references %q, which is not a previously defined step id", where, m[1])
			}
		}
		if str, ok := s.Args.(string); ok {
			refs = append(refs, str)
		} else {
			collectStrings(s.Args, &refs)
		}
		if s.ID != "" {
			seenIDs[s.ID] = true
		}
	}
	// Risk plausibility (spec §7): a pipeline with a bash step may not
	// declare safe. Stricter than computed is fine, laxer is not.
	if hasBash && r.Risk == RiskSafe {
		return fmt.Errorf("risk: pipeline contains a bash step — may not declare safe (spec §7)")
	}
	return validatePlaceholders(r, refs...)
}

// validatePlaceholders checks every {{ ... }} in step/argv strings: known
// param names, previously defined step ids, and no malformed braces.
func validatePlaceholders(r *Reflex, strs ...string) error {
	paramNames := map[string]bool{}
	if props, ok := r.Params["properties"].(map[string]any); ok {
		for k := range props {
			paramNames[k] = true
		}
	}
	stepIDs := map[string]bool{}
	for _, s := range r.Steps {
		if s.ID != "" {
			stepIDs[s.ID] = true
		}
	}
	for _, str := range strs {
		for _, bad := range anyBraceRe.FindAllString(str, -1) {
			if !placeholderRe.MatchString(bad) {
				return fmt.Errorf("malformed placeholder %s in %q — only {{ params.NAME }} and {{ steps.ID.output }} exist", bad, trunc(str, 60))
			}
		}
		for _, ph := range placeholderRe.FindAllStringSubmatch(str, -1) {
			ref := strings.Join(strings.Fields(ph[1]), "")
			if strings.HasPrefix(ref, "params.") {
				if !paramNames[strings.TrimPrefix(ref, "params.")] {
					return fmt.Errorf("unknown placeholder {{ %s }} — not declared in params.properties", ref)
				}
			} else {
				id := strings.TrimSuffix(strings.TrimPrefix(ref, "steps."), ".output")
				if !stepIDs[id] {
					return fmt.Errorf("unknown placeholder {{ %s }} — no step with id %q", ref, id)
				}
			}
		}
	}
	return nil
}

func collectStrings(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case map[string]any:
		for _, vv := range t {
			collectStrings(vv, out)
		}
	case []any:
		for _, vv := range t {
			collectStrings(vv, out)
		}
	}
}

// validateParams enforces the GBNF-compatible schema subset: the same types
// tools.SchemaToGBNF can compile, checked here so rfx doesn't import tools.
func validateParams(p map[string]any) error {
	if p == nil {
		return nil
	}
	if typ, _ := p["type"].(string); typ != "" && typ != "object" {
		return fmt.Errorf("params: top-level type must be object")
	}
	props, ok := p["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return fmt.Errorf("params: properties must be a non-empty map")
	}
	var walk func(name string, s map[string]any) error
	walk = func(name string, s map[string]any) error {
		if enums, ok := s["enum"].([]any); ok {
			for _, e := range enums {
				if _, isStr := e.(string); !isStr {
					return fmt.Errorf("params.%s: enum values must be strings (GBNF)", name)
				}
			}
			return nil
		}
		switch typ, _ := s["type"].(string); typ {
		case "string", "integer", "number", "boolean":
		case "array":
			sub, _ := s["items"].(map[string]any)
			if sub != nil {
				return walk(name+"[]", sub)
			}
		case "object", "":
			sub, _ := s["properties"].(map[string]any)
			for k, v := range sub {
				vm, _ := v.(map[string]any)
				if err := walk(name+"."+k, vm); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("params.%s: type %q is not GBNF-compilable (string/integer/number/boolean/array/object)", name, typ)
		}
		return nil
	}
	for k, v := range props {
		vm, _ := v.(map[string]any)
		if err := walk(k, vm); err != nil {
			return err
		}
	}
	return nil
}

func validateCard(r *Reflex) error {
	for _, f := range r.Card.FS {
		if f == "workspace" || f == "none" {
			continue
		}
		if !strings.HasPrefix(f, "/") {
			return fmt.Errorf("card.fs: %q must be \"workspace\", \"none\", or an absolute path", f)
		}
	}
	for _, n := range r.Card.Network {
		if n == "none" || n == "any" {
			continue
		}
		// hosts only: allow host or host:port, no schemes or paths
		if strings.Contains(n, "://") || strings.ContainsAny(n, "/ ") {
			return fmt.Errorf("card.network: %q must be a host (optionally host:port), \"none\", or \"any\"", n)
		}
	}
	for _, e := range r.Card.Env {
		if strings.ContainsAny(e, "= \t") {
			return fmt.Errorf("card.env: %q must be a variable NAME, not a value", e)
		}
	}
	return nil
}

func validMode(m string) bool {
	for _, v := range Modes {
		if m == v {
			return true
		}
	}
	return false
}

func fileStem(path string) string {
	base := path
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".rfx.yaml")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── Packs (spec v1.1) ───────────────────────────────────────────────────

// Pack is a pack.yaml manifest: a folder of related reflexes — a "talent"
// (github/, blender/, homelab/). One folder level max; names stay global.
// Widget is one RFX_UI content declaration (docs-private/RFX-UI.md): declarative
// only — no code, no HTML. type: button | field | status | log | toggle |
// progress. A field named after a param IS the binding for sibling buttons.
// Row is one status metric: a regex (capture group = value, else match
// count) plus an optional author-declared tone. Tone is semantic — ok / err /
// warn / accent — the renderer maps it to theme colors; authors never pick
// raw colors (declarative, theme-owned pixels).
type Row struct {
	Re   string `yaml:"re"   json:"re"`
	Tone string `yaml:"tone" json:"tone,omitempty"`
}

// UnmarshalYAML accepts both forms: `label: regex` and `label: {re, tone}`.
func (r *Row) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		r.Re = value.Value
		return nil
	}
	var m struct {
		Re   string `yaml:"re"`
		Tone string `yaml:"tone"`
	}
	if err := value.Decode(&m); err != nil {
		return err
	}
	r.Re, r.Tone = m.Re, m.Tone
	return nil
}

// Icons is the closed icon vocabulary (docs-private/RFX-UI.md). Authors pick a NAME;
// the renderer owns the glyphs. Unknown names are load-time rejections, so a
// card never renders a mystery hole.
var Icons = []string{
	"play", "zap", "plus", "check", "x", "upload", "download", "history",
	"file-diff", "git-branch", "git-commit", "folder-git", "terminal",
	"database", "globe", "wrench", "package", "bug", "shield", "sparkles",
	"rocket", "refresh", "search", "trash", "eye", "flame",
}

func validIcon(name string) bool {
	for _, i := range Icons {
		if i == name {
			return true
		}
	}
	return false
}

// Action is a small declarative remedy: shown by a status widget when its
// run fails (on_fail) — "this is broken, here is the button that fixes it".
type Action struct {
	Label string `yaml:"label" json:"label"`
	Run   string `yaml:"run"   json:"run"`
	Icon  string `yaml:"icon"  json:"icon,omitempty"`
}

type Widget struct {
	Type   string         `yaml:"type"    json:"type"`
	Label  string         `yaml:"label"   json:"label,omitempty"`
	Icon   string         `yaml:"icon"    json:"icon,omitempty"` // from Icons; buttons default to play
	Run    string         `yaml:"run"     json:"run,omitempty"`
	Args   map[string]any `yaml:"args"    json:"args,omitempty"`
	Every  string         `yaml:"every"   json:"every,omitempty"` // status refresh interval, e.g. 30s
	Rows   map[string]Row `yaml:"rows"    json:"rows,omitempty"`  // status metrics
	OnFail *Action        `yaml:"on_fail" json:"on_fail,omitempty"`
	Lines  int            `yaml:"lines"   json:"lines,omitempty"` // log tail length
	Name   string         `yaml:"name"    json:"name,omitempty"`  // field param name / toggle target
	Match  string         `yaml:"match"   json:"match,omitempty"` // list: multiline regex over the status run's output
	Limit  int            `yaml:"limit"   json:"limit,omitempty"` // list: max lines shown
}

// PackUI is the ui: block of a pack (docs-private/RFX-UI.md §2).
//
// session/turn are PANEL CAPABILITIES, opt-in per pack: session lets the
// panel read the current session's plan + checkpoints; turn lets it post a
// turn into that session (what the user could type). Both are mediated by
// the host — declaring them only makes the doors visible.
type PackUI struct {
	Session bool     `yaml:"session" json:"session,omitempty"`
	Turn    bool     `yaml:"turn"    json:"turn,omitempty"`
	Widgets []Widget `yaml:"widgets" json:"widgets"`
}

type Pack struct {
	RFX         int      `yaml:"rfx"`
	Pack        string   `yaml:"pack"`
	Version     string   `yaml:"version"`
	Author      string   `yaml:"author"`
	Description string   `yaml:"description"`
	Icon        string   `yaml:"icon" json:"icon,omitempty"` // from Icons; the pack's tab + card glyph
	Panel       string   `yaml:"-"    json:"-"` // discovered ui/panel.html — full custom UI (RFX-UI tier 2)
	UI          PackUI   `yaml:"ui"   json:"ui,omitempty"`
	Docs        []string `yaml:"-"    json:"docs,omitempty"` // discovered docs/*.md
	Path        string   `yaml:"-"    json:"-"`
}

func ParsePack(data []byte, path string) (*Pack, error) {
	var p Pack
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p.Path = path
	return &p, nil
}

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func ValidatePack(p *Pack) error {
	if p.RFX != SpecVersion {
		return fmt.Errorf("rfx: must be %d (got %d)", SpecVersion, p.RFX)
	}
	if !nameRe.MatchString(p.Pack) || len(p.Pack) > 40 {
		return fmt.Errorf("pack %q: must match [a-z0-9-]+ and be ≤ 40 chars", p.Pack)
	}
	if !semverRe.MatchString(p.Version) {
		return fmt.Errorf("version %q: must be semver (e.g. 1.0.0)", p.Version)
	}
	if p.Description == "" {
		return fmt.Errorf("description: required — one sentence on what this talent does")
	}
	if len(p.Description) > 200 {
		return fmt.Errorf("description: %d chars, max 200", len(p.Description))
	}
	if p.Icon != "" && !validIcon(p.Icon) {
		return fmt.Errorf("icon %q: not in the icon set (%s)", p.Icon, strings.Join(Icons, ", "))
	}
	for i, w := range p.UI.Widgets {
		if err := validateWidget(w); err != nil {
			return fmt.Errorf("ui.widgets[%d]: %w", i, err)
		}
	}
	return nil
}

func validateWidget(w Widget) error {
	switch w.Type {
	case "button":
		if w.Label == "" {
			return fmt.Errorf("button: label required")
		}
		// Pack-level context: there is no "card's own reflex" to default to.
		if w.Run == "" {
			return fmt.Errorf("button: run: required (a pack card has no implicit target reflex)")
		}
	case "field":
		if w.Name == "" {
			return fmt.Errorf("field: name required (it is the param binding)")
		}
	case "status":
		if w.Run == "" || len(w.Rows) == 0 {
			return fmt.Errorf("status: run + rows required")
		}
	case "log":
		if w.Lines < 0 {
			return fmt.Errorf("log: lines must be ≥ 0")
		}
	case "list":
		if w.Match == "" {
			return fmt.Errorf("list: match: required (a multiline regex over the status run's output)")
		}
		if _, err := regexp.Compile(w.Match); err != nil {
			return fmt.Errorf("list: match %q does not compile: %v", w.Match, err)
		}
		if w.Limit < 0 {
			return fmt.Errorf("list: limit must be ≥ 0")
		}
	case "toggle", "progress":
	default:
		return fmt.Errorf("unknown widget type %q (button|field|status|log|toggle|progress|list)", w.Type)
	}
	// Semantic checks — load-time rejection, never a runtime surprise in the
	// panel (the renderer would otherwise throw on a bad regex mid-render).
	if w.Icon != "" && !validIcon(w.Icon) {
		return fmt.Errorf("icon %q: not in the icon set (%s)", w.Icon, strings.Join(Icons, ", "))
	}
	if w.OnFail != nil {
		if w.Type != "status" {
			return fmt.Errorf("on_fail: only valid on a status widget")
		}
		if w.OnFail.Label == "" || w.OnFail.Run == "" {
			return fmt.Errorf("on_fail: label and run are both required")
		}
		if w.OnFail.Icon != "" && !validIcon(w.OnFail.Icon) {
			return fmt.Errorf("on_fail.icon %q: not in the icon set", w.OnFail.Icon)
		}
	}
	for label, row := range w.Rows {
		if _, err := regexp.Compile(row.Re); err != nil {
			return fmt.Errorf("rows.%s: regex %q does not compile: %v", label, row.Re, err)
		}
		switch row.Tone {
		case "", "ok", "err", "warn", "accent":
		default:
			return fmt.Errorf("rows.%s: tone %q — must be ok, err, warn, or accent", label, row.Tone)
		}
	}
	if w.Every != "" {
		d, err := time.ParseDuration(w.Every)
		if err != nil || d <= 0 {
			return fmt.Errorf("every %q: use a positive duration like 30s or 5m", w.Every)
		}
	}
	return nil
}

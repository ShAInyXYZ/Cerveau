package tools

import (
	"fmt"
	"sort"
	"strings"
)

type gbnfBuilder struct {
	defs  map[string]string
	order []string
	n     int
}

func (b *gbnfBuilder) fresh(base string) string {
	b.n++
	return fmt.Sprintf("%s-%d", sanitizeRuleName(base), b.n)
}

// GBNF rule names may only contain [a-zA-Z0-9-]. Property keys like "open_loops"
// or "promotion_candidates" carry underscores, which llama.cpp's grammar parser
// rejects ("failed to parse grammar"). Map every illegal char to '-'.
func sanitizeRuleName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			out = append(out, r)
		} else {
			out = append(out, '-')
		}
	}
	return string(out)
}

func (b *gbnfBuilder) add(name, def string) {
	if _, ok := b.defs[name]; !ok {
		b.order = append(b.order, name)
	}
	b.defs[name] = def
}

const gbnfQuote = `"\""`

func gbnfLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return gbnfQuote + ` "` + s + `" ` + gbnfQuote
}

func (b *gbnfBuilder) build(name string, schema map[string]any) (string, error) {
	typ, _ := schema["type"].(string)
	if enums, ok := schema["enum"].([]any); ok && len(enums) > 0 {
		parts := []string{}
		for _, e := range enums {
			s, ok := e.(string)
			if !ok {
				return "", fmt.Errorf("enum values must be strings")
			}
			parts = append(parts, gbnfLiteral(s))
		}
		b.add(name, strings.Join(parts, " | "))
		return name, nil
	}
	switch typ {
	case "string":
		b.add(name, `"\"" ([^"\\] | "\\" .)* "\""`)
	case "integer":
		b.add(name, `"-"? [0-9]+`)
	case "number":
		b.add(name, `"-"? ([0-9] | [1-9] [0-9]*) ("." [0-9]+)? ([eE] [-+]? [0-9]+)?`)
	case "boolean":
		b.add(name, `"true" | "false"`)
	case "array":
		itemSchema, _ := schema["items"].(map[string]any)
		itemName := b.fresh(name + "-item")
		if itemSchema == nil {
			itemSchema = map[string]any{}
		}
		if _, err := b.build(itemName, itemSchema); err != nil {
			return "", err
		}
		b.add(name, `"[" ws ( `+itemName+` ("," ws `+itemName+`)* )? "]" ws`)
	case "object", "":
		props, _ := schema["properties"].(map[string]any)
		required := map[string]bool{}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					required[s] = true
				}
			}
		}
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		type member struct {
			lit  string
			rule string
			req  bool
		}
		members := []member{}
		for _, k := range keys {
			sub, _ := props[k].(map[string]any)
			rule := b.fresh(name + "-" + k)
			if _, err := b.build(rule, sub); err != nil {
				return "", err
			}
			members = append(members, member{lit: gbnfLiteral(k), rule: rule, req: required[k]})
		}
		pair := func(m member) string {
			return m.lit + ` ws ":" ws ` + m.rule
		}
		reqs := []member{}
		opts := []member{}
		for _, m := range members {
			if m.req {
				reqs = append(reqs, m)
			} else {
				opts = append(opts, m)
			}
		}
		var sb strings.Builder
		sb.WriteString(`"{" ws `)
		if len(reqs) > 0 {
			sb.WriteString(pair(reqs[0]))
			for _, m := range reqs[1:] {
				sb.WriteString(` "," ws ` + pair(m))
			}
			for _, m := range opts {
				sb.WriteString(` ( "," ws ` + pair(m) + ` )?`)
			}
		} else if len(opts) > 0 {
			sb.WriteString(` ( ` + pair(opts[0]))
			for _, m := range opts[1:] {
				sb.WriteString(` ( "," ws ` + pair(m) + ` )?`)
			}
			sb.WriteString(` )?`)
		}
		sb.WriteString(` "}" ws`)
		b.add(name, sb.String())
	default:
		return "", fmt.Errorf("unsupported schema type %q", typ)
	}
	return name, nil
}

const gbnfPrelude = `ws ::= [ \t\n]*
`

func SchemaToGBNF(schema map[string]any) (string, error) {
	b := &gbnfBuilder{defs: map[string]string{}}
	if _, err := b.build("root", schema); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(gbnfPrelude)
	for _, name := range b.order {
		sb.WriteString(name + " ::= " + b.defs[name] + "\n")
	}
	return sb.String(), nil
}

func UnionToolCallGrammar(entries []Entry, mode string) (string, error) {
	b := &gbnfBuilder{defs: map[string]string{}}
	alternatives := []string{}
	for _, e := range entries {
		name := e.Tool.Name()
		callRule := b.fresh("call-" + name)
		argsRule := b.fresh("args-" + name)
		if _, err := b.build(argsRule, e.Tool.Schema()); err != nil {
			return "", err
		}
		b.add(callRule, `"{" ws `+gbnfLiteral("tool")+` ws ":" ws `+gbnfLiteral(name)+` ws "," ws `+gbnfLiteral("args")+` ws ":" ws `+argsRule+` "}" ws`)
		alternatives = append(alternatives, callRule)
	}
	if len(alternatives) == 0 {
		return "", fmt.Errorf("no tools for mode %q", mode)
	}
	b.add("root", strings.Join(alternatives, " | "))
	var sb strings.Builder
	sb.WriteString(gbnfPrelude)
	for _, name := range b.order {
		sb.WriteString(name + " ::= " + b.defs[name] + "\n")
	}
	return sb.String(), nil
}

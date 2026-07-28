package codeintel

import (
	"regexp"
	"strings"
)

type regexRule struct {
	lang       string
	defs       []*regexp.Regexp
	kind       string
	callRe     *regexp.Regexp
	callFilter map[string]bool
}

var pyRule = &regexRule{
	lang: "python",
	defs: []*regexp.Regexp{
		regexp.MustCompile(`^\s*def\s+([A-Za-z_]\w*)\s*\(([^)]*)\)`),
		regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`),
	},
	callRe: regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`),
	callFilter: pyBuiltins,
}

var jsRule = &regexRule{
	lang: "javascript",
	defs: []*regexp.Regexp{
		regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$]\w*)\s*\(([^)]*)\)`),
		regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$]\w*)\s*=\s*(?:async\s*)?\(([^)]*)\)\s*=>`),
		regexp.MustCompile(`^\s*(?:export\s+)?class\s+([A-Za-z_$]\w*)`),
	},
	callRe: regexp.MustCompile(`\b([A-Za-z_$]\w*)\s*\(`),
	callFilter: jsBuiltins,
}

var tsRule = &regexRule{
	lang: "typescript",
	defs: append(append([]*regexp.Regexp{}, jsRule.defs...),
		regexp.MustCompile(`^\s*(?:export\s+)?(?:interface|type|enum)\s+([A-Za-z_$]\w*)`)),
	callRe:     jsRule.callRe,
	callFilter: jsBuiltins,
}

var rustRule = &regexRule{
	lang: "rust",
	defs: []*regexp.Regexp{
		regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)\s*(?:<[^>]*>)?\(([^)]*)\)`),
		regexp.MustCompile(`^\s*(?:pub\s+)?(?:struct|enum|trait|impl)\s+([A-Za-z_]\w*)`),
	},
	callRe:     regexp.MustCompile(`\b([A-Za-z_]\w*)\s*(?:\(|!)`),
	callFilter: rustBuiltins,
}

var clikeRule = &regexRule{
	lang: "clike",
	defs: []*regexp.Regexp{
		regexp.MustCompile(`^\s*(?:class|struct)\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^\s*(?:public|private|protected|static|final|virtual|inline|\s)*[A-Za-z_][\w:<>,\[\]*&\s]*\s+([A-Za-z_]\w*)\s*\(([^)]*)\)\s*(?:\{|$)`),
	},
	callRe:     regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`),
	callFilter: cBuiltins,
}

func ruleFor(lang string) *regexRule {
	switch lang {
	case "python":
		return pyRule
	case "javascript":
		return jsRule
	case "typescript":
		return tsRule
	case "rust":
		return rustRule
	case "c", "cpp", "java", "csharp":
		return clikeRule
	}
	return nil
}

func extractRegex(path, lang string, src []byte) ([]Symbol, []Call, error) {
	rule := ruleFor(lang)
	if rule == nil {
		return nil, nil, nil
	}
	var symbols []Symbol
	var calls []Call
	lines := strings.Split(string(src), "\n")
	var lastDef *Symbol
	for i, line := range lines {
		for _, re := range rule.defs {
			if m := re.FindStringSubmatch(line); m != nil {
				name := m[1]
				sig := strings.TrimSpace(line)
				if len(sig) > 120 {
					sig = sig[:120]
				}
				sym := Symbol{File: path, Name: name, Kind: kindOfDef(sig), Signature: sig, Line: i + 1}
				symbols = append(symbols, sym)
				lastDef = &symbols[len(symbols)-1]
				break
			}
		}
		for _, m := range rule.callRe.FindAllStringSubmatch(line, -1) {
			name := m[1]
			if rule.callFilter[name] {
				continue
			}
			caller := ""
			if lastDef != nil {
				caller = lastDef.Name
			}
			calls = append(calls, Call{CallerFile: path, CallerSymbol: caller, CalleeName: name, Line: i + 1})
		}
	}
	return symbols, calls, nil
}

func kindOfDef(sig string) string {
	switch {
	case strings.HasPrefix(sig, "class") || strings.Contains(sig, " class "):
		return "class"
	case strings.HasPrefix(sig, "interface") || strings.Contains(sig, "interface ") || strings.HasPrefix(sig, "type") || strings.HasPrefix(sig, "struct") || strings.Contains(sig, "struct ") || strings.HasPrefix(sig, "enum") || strings.Contains(sig, "enum ") || strings.HasPrefix(sig, "trait") || strings.Contains(sig, "trait "):
		return "type"
	default:
		return "func"
	}
}

var pyBuiltins = map[string]bool{
	"if": true, "for": true, "while": true, "return": true, "print": true, "len": true,
	"range": true, "str": true, "int": true, "float": true, "list": true, "dict": true,
	"set": true, "tuple": true, "type": true, "isinstance": true, "super": true,
	"self": true, "cls": true, "import": true, "from": true, "with": true, "as": true,
	"def": true, "class": true, "except": true, "raise": true, "assert": true, "lambda": true,
	"map": true, "filter": true, "zip": true, "enumerate": true, "sorted": true, "open": true,
	"getattr": true, "setattr": true, "hasattr": true, "any": true, "all": true, "sum": true,
	"min": true, "max": true, "abs": true, "repr": true, "format": true, "join": true,
}

var jsBuiltins = map[string]bool{
	"if": true, "for": true, "while": true, "return": true, "function": true, "const": true,
	"let": true, "var": true, "new": true, "class": true, "typeof": true, "instanceof": true,
	"console": true, "log": true, "require": true, "import": true, "export": true,
	"async": true, "await": true, "switch": true, "case": true, "catch": true, "throw": true,
	"fetch": true, "then": true, "map": true, "filter": true, "reduce": true,
	"forEach": true, "push": true, "slice": true, "split": true, "join": true, "stringify": true,
	"parse": true, "setTimeout": true, "setInterval": true, "promise": true, "resolve": true,
	"reject": true, "Object": true, "Array": true, "String": true, "Number": true, "JSON": true,
}

var rustBuiltins = map[string]bool{
	"if": true, "for": true, "while": true, "loop": true, "match": true, "return": true,
	"let": true, "fn": true, "pub": true, "use": true, "mod": true, "impl": true,
	"struct": true, "enum": true, "trait": true, "println": true, "print": true, "format": true,
	"vec": true, "assert": true, "panic": true, "Some": true, "None": true, "Ok": true, "Err": true,
	"self": true, "Self": true, "mut": true, "ref": true, "Box": true, "Rc": true, "Arc": true,
	"String": true, "Vec": true, "Option": true, "Result": true, "unwrap": true, "expect": true,
}

var cBuiltins = map[string]bool{
	"if": true, "for": true, "while": true, "return": true, "switch": true, "case": true,
	"sizeof": true, "typedef": true, "struct": true, "class": true, "public": true,
	"private": true, "protected": true, "static": true, "void": true, "int": true,
	"char": true, "float": true, "double": true, "long": true, "unsigned": true,
	"const": true, "new": true, "delete": true, "this": true, "template": true,
	"printf": true, "malloc": true, "free": true, "memcpy": true, "strlen": true,
}

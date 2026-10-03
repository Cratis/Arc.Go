// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

type directives struct {
	kind, name, path, model, http string
	namespace                     string
	hasNamespace, hasPath         bool
	ignore, exclude               bool
	auth                          *metadata.Authorization
	severity                      *validation.Severity
	pos                           token.Pos
}

func parseDirectives(group *ast.CommentGroup) (directives, error) {
	var d directives
	seen := map[string]bool{}
	if group == nil {
		return d, nil
	}
	for _, comment := range group.List {
		text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
		if !strings.HasPrefix(text, "arc:") {
			if strings.Contains(comment.Text, "arc:") && strings.HasPrefix(comment.Text, "/*") {
				return d, fmt.Errorf("arc directives require line comments")
			}
			continue
		}
		d.pos = comment.Pos()
		words := strings.Fields(strings.TrimPrefix(text, "arc:"))
		if len(words) == 0 {
			return d, fmt.Errorf("empty arc directive")
		}
		name := words[0]
		if seen[name] && name != "authorize" {
			return d, fmt.Errorf("duplicate arc:%s", name)
		}
		seen[name] = true
		var allowed string
		switch name {
		case "command":
			allowed = " name path block-on "
		case "readmodel":
			allowed = " name path "
		case "query":
			allowed = " model name path http "
		case "authorize":
			allowed = " roles policy "
		case "namespace":
			if len(words) != 2 {
				return d, fmt.Errorf("arc:namespace requires one logical namespace")
			}
			d.namespace, d.hasNamespace = words[1], true
			continue
		case "allow-anonymous", "exclude-from-discovery", "ignore":
		default:
			return d, fmt.Errorf("unknown arc:%s", name)
		}
		opts := map[string]string{}
		for _, word := range words[1:] {
			key, value, ok := strings.Cut(word, "=")
			if !ok || !strings.Contains(allowed, " "+key+" ") {
				return d, fmt.Errorf("unsupported option %q on arc:%s", word, name)
			}
			if _, exists := opts[key]; exists {
				return d, fmt.Errorf("duplicate option %q", key)
			}
			if value == "" && key != "path" {
				return d, fmt.Errorf("empty option %q", key)
			}
			opts[key] = value
		}
		switch name {
		case "command", "readmodel", "query":
			if d.kind != "" {
				return d, fmt.Errorf("conflicting artifact directives")
			}
			d.kind = name
			d.name, d.model, d.http = opts["name"], opts["model"], opts["http"]
			d.path, d.hasPath = opts["path"]
			if d.http != "" && d.http != "GET" && d.http != "QUERY" {
				return d, fmt.Errorf("http must be GET or QUERY")
			}
			if value, ok := opts["block-on"]; ok {
				severity, valid := map[string]validation.Severity{"unknown": validation.Unknown, "information": validation.Information, "warning": validation.Warning, "error": validation.Error}[value]
				if !valid {
					return d, fmt.Errorf("invalid block-on severity %q", value)
				}
				d.severity = &severity
			}
		case "authorize":
			if d.auth != nil && d.auth.AllowAnonymous {
				return d, fmt.Errorf("anonymous and authorization declarations conflict")
			}
			if d.auth == nil {
				d.auth = &metadata.Authorization{}
			}
			r := metadata.AuthorizationRequirement{Policy: opts["policy"]}
			if roles, ok := opts["roles"]; ok {
				r.Roles = strings.Split(roles, ",")
			}
			d.auth.Requirements = append(d.auth.Requirements, r)
		case "allow-anonymous":
			if d.auth != nil {
				return d, fmt.Errorf("anonymous and authorization declarations conflict")
			}
			d.auth = &metadata.Authorization{AllowAnonymous: true}
		case "exclude-from-discovery":
			d.exclude = true
		case "ignore":
			d.ignore = true
		}
	}
	if d.ignore && (d.kind != "" || d.auth != nil || d.exclude || d.hasNamespace) {
		return d, fmt.Errorf("arc:ignore cannot be combined with other directives")
	}
	return d, nil
}

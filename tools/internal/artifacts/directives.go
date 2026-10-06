// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"unicode"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

type directives struct {
	kind, name, path, model, http           string
	namespace                               string
	hasNamespace, hasPath                   bool
	ignore, exclude                         bool
	auth                                    *metadata.Authorization
	severity                                *validation.Severity
	pos                                     token.Pos
	flags                                   bool
	parse                                   string
	members                                 map[string]string
	response                                string
	derivedID, derivedBase, targetInterface string
	// concept selects concept-leaf validator registration on arc:validator.
	concept bool
	// evaluatesAnonymous opts an arc:policy into guest evaluation.
	evaluatesAnonymous bool
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
			allowed = " name path block-on response "
		case "readmodel":
			allowed = " name path "
		case "model":
			allowed = " name "
		case "enum":
			allowed = " name flags members parse "
		case "codec":
			return d, fmt.Errorf("arc:codec is not implemented: complex-key dictionaries, geospatial, Type/Uri, enumerable-model-to-concept")
		case "derived":
			allowed = " id base interface "
		case "query":
			allowed = " model name path http "
		case "authorize":
			allowed = " roles policy "
		case "validator":
			allowed = " concept "
		case "policy":
			allowed = " name evaluates-anonymous "
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
		case "validator", "policy":
			if d.kind != "" {
				return d, fmt.Errorf("conflicting artifact directives")
			}
			d.kind = name
			for key, target := range map[string]*bool{"concept": &d.concept, "evaluates-anonymous": &d.evaluatesAnonymous} {
				if value, supplied := opts[key]; supplied {
					if value != "true" && value != "false" {
						return d, fmt.Errorf("%s must be true or false", key)
					}
					*target = value == "true"
				}
			}
			if name == "policy" {
				d.name = opts["name"]
				if d.name == "" {
					return d, fmt.Errorf("arc:policy requires name=<policy name>")
				}
				if strings.IndexFunc(d.name, unicode.IsControl) >= 0 {
					return d, fmt.Errorf("policy name must not contain control characters")
				}
			}
		case "command", "readmodel", "query", "enum", "model":
			if d.kind != "" {
				return d, fmt.Errorf("conflicting artifact directives")
			}
			d.kind = name
			d.name, d.model, d.http = opts["name"], opts["model"], opts["http"]
			d.path, d.hasPath = opts["path"]
			d.response = opts["response"]
			d.parse = opts["parse"]
			if d.parse != "" && d.parse != "int32" {
				return d, fmt.Errorf("enum parse must be int32")
			}
			if value, supplied := opts["flags"]; supplied {
				if value != "true" && value != "false" {
					return d, fmt.Errorf("flags must be true or false")
				}
				d.flags = value == "true"
			}
			if value := opts["members"]; value != "" {
				d.members = map[string]string{}
				for _, item := range strings.Split(value, ",") {
					key, member, ok := strings.Cut(item, ":")
					if !ok || key == "" || member == "" || d.members[key] != "" {
						return d, fmt.Errorf("invalid enum member mapping")
					}
					d.members[key] = member
				}
			}
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
		case "derived":
			d.derivedID, d.derivedBase, d.targetInterface = opts["id"], opts["base"], opts["interface"]
			if d.targetInterface == "" {
				return d, fmt.Errorf("derived declaration requires an interface")
			}
			if d.derivedID != "" && d.derivedBase == "" {
				return d, fmt.Errorf("derived declaration requires an explicit base model")
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
	if d.targetInterface != "" && d.kind == "" {
		d.kind = "model"
	}
	if d.targetInterface != "" && d.kind != "model" && d.kind != "readmodel" {
		return d, fmt.Errorf("derived declarations require a wire model")
	}
	if (d.kind == "validator" || d.kind == "policy") && (d.auth != nil || d.exclude || d.hasNamespace || d.targetInterface != "") {
		return d, fmt.Errorf("arc:%s cannot be combined with other directives", d.kind)
	}
	if d.ignore && (d.kind != "" || d.auth != nil || d.exclude || d.hasNamespace || d.targetInterface != "") {
		return d, fmt.Errorf("arc:ignore cannot be combined with other directives")
	}
	return d, nil
}

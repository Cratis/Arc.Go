// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package consumer

import (
	"context"
	"strings"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/validation"
)

//arc:command name=Rename
//arc:authorize policy=Editors
type rename struct {
	Name  string `json:"name"`
	Code  code   `json:"code"`
	Owner owner  `json:"owner"`
}

func (rename) Handle() error { return nil }

//arc:command name=Audit
//arc:authorize policy=Auditors
type audit struct {
	Note string `json:"note"`
}

func (audit) Handle() error { return nil }

type code string

type owner struct {
	ID string `json:"id"`
}

// Shared validator: the zero value is registered once with validation.Register.
//
//arc:validator
type renameValidator struct{}

func (renameValidator) Validate(_ context.Context, command rename) ([]validation.Result, error) {
	if command.Name == "" {
		return []validation.Result{{Severity: validation.Error, Message: "Name required", Members: []string{"name"}}}, nil
	}
	return nil, nil
}

// Concept validator with a pointer receiver: registered as &codeValidator{}.
//
//arc:validator concept=true
type codeValidator struct{}

func (*codeValidator) Validate(_ context.Context, value code) ([]validation.Result, error) {
	if strings.ToUpper(string(value)) != string(value) {
		return []validation.Result{{Severity: validation.Error, Message: "Code must be upper case"}}, nil
	}
	return nil, nil
}

type ownerRules interface {
	Allowed(string) bool
}

type ownerValidator struct{ rules ownerRules }

func (v *ownerValidator) Validate(_ context.Context, value owner) ([]validation.Result, error) {
	if !v.rules.Allowed(value.ID) {
		return []validation.Result{{Severity: validation.Error, Message: "Owner not allowed", Members: []string{"id"}}}, nil
	}
	return nil, nil
}

// Scoped validator: the constructor runs per validation through RegisterScoped.
//
//arc:validator
func newOwnerValidator(_ context.Context, rules ownerRules) (*ownerValidator, error) {
	return &ownerValidator{rules: rules}, nil
}

// Shared policy registered with Policies().Register.
//
//arc:policy name=Editors
type editors struct{}

func (editors) Authorize(_ context.Context, value authorization.Context) (authorization.Decision, error) {
	if value.Principal.HasRole("Editor") {
		return authorization.Allow(), nil
	}
	return authorization.Deny("editors only"), nil
}

type auditLog interface {
	Evaluated()
}

type auditors struct{ log auditLog }

func (a auditors) Authorize(_ context.Context, value authorization.Context) (authorization.Decision, error) {
	a.log.Evaluated()
	if value.Principal.IsAuthenticated() {
		return authorization.Deny("auditors accept guests only"), nil
	}
	return authorization.Allow(), nil
}

// Scoped policy registered with authorization.RegisterPolicy; guests are evaluated.
//
//arc:policy name=Auditors evaluates-anonymous=true
func newAuditors(log auditLog) auditors { return auditors{log: log} }

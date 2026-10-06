package validation_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cratis/arc.go/validation"
)

func TestSeverityMatrix(t *testing.T) {
	for allowed := -1; allowed <= 3; allowed++ {
		for floor := -1; floor <= 3; floor++ {
			t.Run(fmt.Sprintf("allowed_%d_floor_%d", allowed, floor), func(t *testing.T) {
				options := validation.SeverityOptions{}
				a, f := validation.Severity(allowed), validation.Severity(floor)
				if allowed >= 0 {
					options.Allowed = &a
				}
				if floor >= 0 {
					options.BlockOn = &f
				}
				policy, err := validation.NewPolicy(options)
				if err != nil {
					t.Fatal(err)
				}
				for severity := validation.Unknown; severity <= validation.Error; severity++ {
					threshold := 2
					if allowed >= 0 {
						threshold = allowed
					}
					if floor >= 0 && (allowed < 0 || floor-1 < threshold) {
						threshold = floor - 1
					}
					want := int(severity) > threshold || (floor >= 0 && severity == validation.Unknown)
					if policy.Blocks(severity) != want {
						t.Fatalf("severity %d blocks=%v want=%v", severity, policy.Blocks(severity), want)
					}
				}
				a, f = 3, 3
				if !policy.Blocks(99) || !policy.Blocks(-1) {
					t.Fatal("invalid finding allowed")
				}
			})
		}
	}
	bad := validation.Severity(4)
	for _, options := range []validation.SeverityOptions{{Allowed: &bad}, {BlockOn: &bad}} {
		if _, err := validation.NewPolicy(options); !errors.Is(err, validation.ErrInvalidSeverity) {
			t.Fatal(err)
		}
	}
	var policy validation.Policy
	source := []validation.Result{{Severity: validation.Error, Members: []string{"name"}}, {Severity: validation.Warning}}
	got := policy.Filter(source)
	got[0].Members[0] = "changed"
	if len(got) != 1 || source[0].Members[0] != "name" {
		t.Fatal("filter semantics")
	}
}

func TestAllowedHeader(t *testing.T) {
	for _, tc := range []struct {
		values []string
		want   validation.Severity
		ok     bool
	}{
		{[]string{"0"}, 0, true}, {[]string{" +1 "}, 1, true}, {[]string{"2"}, 2, true}, {[]string{"3"}, 2, true},
		{nil, 0, false}, {[]string{"1", "2"}, 0, false}, {[]string{"1,2"}, 0, false}, {[]string{"Error"}, 0, false},
		{[]string{"-1"}, 0, false}, {[]string{"4"}, 0, false}, {[]string{"2147483648"}, 0, false}, {[]string{"0x2"}, 0, false}, {[]string{""}, 0, false},
	} {
		got, ok := validation.AllowedFromHeader(tc.values)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%v = %d,%v", tc.values, got, ok)
		}
	}
}

func FuzzAllowedFromHeader(f *testing.F) {
	for _, seed := range []string{"3", "-1", "+2", "1,2", "2147483648"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		severity, ok := validation.AllowedFromHeader([]string{text})
		if ok && (severity < 0 || severity > validation.Warning) {
			t.Fatalf("unsafe allowance %d", severity)
		}
	})
}

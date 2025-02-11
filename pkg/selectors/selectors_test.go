package selectors_test

import (
	"testing"

	"github.com/bwagner5/vm/pkg/selectors"
)

func TestParseSelectors(t *testing.T) {
	type testCases struct {
		selectorStr string
		expected    []selectors.GenericSelector
		expectedErr bool
	}

	for _, tc := range []testCases{
		{
			selectorStr: "tag:Name=foo,tag:Owner=bar",
			expected: []selectors.GenericSelector{
				{
					Tags: map[string]string{
						"Name":  "foo",
						"Owner": "bar",
					},
				},
			},
		},
		{
			selectorStr: "tag:Name=foo,tag:Owner=bar,Name:baz,ID:r-123",
			expected: []selectors.GenericSelector{
				{
					Tags: map[string]string{
						"Name":  "foo",
						"Owner": "bar",
					},
					KeyVals: map[string]string{
						"name": "baz",
						"id":   "r-123",
					},
				},
			},
		},
		{
			selectorStr: "tag:Name=foo,tag:Owner=bar;Name:baz,ID:r-123",
			expected: []selectors.GenericSelector{
				{
					Tags: map[string]string{
						"Name":  "foo",
						"Owner": "bar",
					},
				},
				{
					KeyVals: map[string]string{
						"name": "baz",
						"id":   "r-123",
					},
				},
			},
		},
		{
			selectorStr: "tag:Name=foo,tag:Owner=bar;",
			expected: []selectors.GenericSelector{
				{
					Tags: map[string]string{
						"Name":  "foo",
						"Owner": "bar",
					},
				},
			},
		},
		{
			selectorStr: "tag:Name,tag:Owner=bar",
			expected: []selectors.GenericSelector{
				{
					Tags: map[string]string{
						"Name":  "",
						"Owner": "bar",
					},
				},
			},
		},
	} {
		t.Run(tc.selectorStr, func(t *testing.T) {
			parsedSelectors, err := selectors.ParseSelectorsTokens(tc.selectorStr)
			if tc.expectedErr && err != nil {
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(parsedSelectors) != len(tc.expected) {
				t.Fatalf("expected %d selectors, got %d", len(tc.expected), len(parsedSelectors))
			}

			for i, expected := range tc.expected {

				if len(parsedSelectors[i].KeyVals) != len(expected.KeyVals) {
					t.Fatalf("expected %d key/vals, got %d", len(expected.KeyVals), len(parsedSelectors[i].KeyVals))
				}

				for k, v := range expected.KeyVals {
					if parsedSelectors[i].KeyVals[k] != v {
						t.Errorf("expected key/vals %q=%q, got %q=%q", k, v, k, parsedSelectors[i].KeyVals[k])
					}
				}

				if len(parsedSelectors[i].Tags) != len(expected.Tags) {
					t.Fatalf("expected %d tags, got %d", len(expected.Tags), len(parsedSelectors[i].Tags))
				}

				for k, v := range expected.Tags {
					if parsedSelectors[i].Tags[k] != v {
						t.Errorf("expected tag %q=%q, got %q=%q", k, v, k, parsedSelectors[i].Tags[k])
					}
				}
			}
		})
	}
}

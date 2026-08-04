package config

// mergeonly_test.go — CRAWLER_MERGE_ONLY parsing.
//
// This setting used to be `getenv("CRAWLER_MERGE_ONLY") != "false"`, so only the
// exact lowercase string "false" turned it off. "0", "no", "off" and "False"
// all silently meant TRUE, while every other boolean in this package went
// through strconv.ParseBool. An operator who typed CRAWLER_MERGE_ONLY=0 got the
// opposite of what they asked for and no warning. These cases pin the fix: the
// accepted forms are exactly ParseBool's, and anything else is a startup error.

import (
	"strings"
	"testing"
)

func TestLoadSettings_MergeOnly(t *testing.T) {
	base := map[string]string{
		"CRAWLER_DB_DSN":        "postgres://u:p@h/db",
		"CRAWLER_PUSH_ENDPOINT": "https://d/push",
		"CRAWLER_INDEX_URLS":    "https://a/i",
	}

	tests := []struct {
		name    string
		val     string
		want    bool
		wantErr bool
	}{
		{name: "unset defaults to merge-only", val: "", want: true},
		{name: "whitespace defaults to merge-only", val: "   ", want: true},
		{name: "true", val: "true", want: true},
		{name: "false", val: "false", want: false},
		// The regression the old parser had: every one of these used to mean TRUE.
		{name: "zero turns it off", val: "0", want: false},
		{name: "one turns it on", val: "1", want: true},
		{name: "f turns it off", val: "f", want: false},
		{name: "t turns it on", val: "t", want: true},
		{name: "FALSE turns it off", val: "FALSE", want: false},
		{name: "False turns it off", val: "False", want: false},
		{name: "TRUE turns it on", val: "TRUE", want: true},
		{name: "surrounding space is tolerated", val: "  false  ", want: false},
		// Garbage is rejected rather than silently read as true.
		{name: "no is rejected", val: "no", wantErr: true},
		{name: "yes is rejected", val: "yes", wantErr: true},
		{name: "off is rejected", val: "off", wantErr: true},
		{name: "on is rejected", val: "on", wantErr: true},
		{name: "typo is rejected", val: "flase", wantErr: true},
		{name: "empty-ish garbage is rejected", val: "-", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"CRAWLER_MERGE_ONLY": tt.val}
			for k, v := range base {
				env[k] = v
			}
			s, err := LoadSettings(func(k string) string { return env[k] })
			if tt.wantErr {
				if err == nil {
					t.Fatalf("LoadSettings(CRAWLER_MERGE_ONLY=%q) = no error, want a rejection", tt.val)
				}
				if !strings.Contains(err.Error(), "CRAWLER_MERGE_ONLY") {
					t.Errorf("error %q does not name the offending variable", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadSettings(CRAWLER_MERGE_ONLY=%q): %v", tt.val, err)
			}
			if s.MergeOnly != tt.want {
				t.Fatalf("MergeOnly = %v, want %v", s.MergeOnly, tt.want)
			}
		})
	}
}

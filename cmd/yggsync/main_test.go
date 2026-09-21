package main

import (
	"errors"
	"testing"
)

func TestNotifyWorthy(t *testing.T) {
	lockErr := errors.New("another yggsync run is already active")
	cases := []struct {
		name   string
		failed map[string]error
		want   bool
	}{
		{"no failures", nil, false},
		{"lock bounce only", map[string]error{"_lock": lockErr}, false},
		{"real job failure", map[string]error{"dcim": errors.New("boom")}, true},
		{"lock plus real", map[string]error{"_lock": lockErr, "dcim": errors.New("boom")}, true},
	}
	for _, tc := range cases {
		if got := notifyWorthy(tc.failed); got != tc.want {
			t.Errorf("%s: notifyWorthy=%v, want %v", tc.name, got, tc.want)
		}
	}
}

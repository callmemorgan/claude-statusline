package main

import "testing"

func TestConfigureArgs(t *testing.T) {
	cases := []struct {
		args   []string
		canvas bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"--drawer"}, true},
		{[]string{"-d"}, true},
		{[]string{"--foo", "--drawer"}, true},
		{[]string{"--drawer-extra"}, false},
		{[]string{"--help"}, false},
	}
	for _, tc := range cases {
		got := configureArgs(tc.args)
		if got != tc.canvas {
			t.Errorf("configureArgs(%v) = %v, want %v", tc.args, got, tc.canvas)
		}
	}
}

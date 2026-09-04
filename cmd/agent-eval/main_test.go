package main

import "testing"

func TestAgentEvalDefaultsToCurrentAgentRelease(t *testing.T) {
	if defaultAgentRelease != "nano.default@25" {
		t.Fatalf("default Agent release=%q", defaultAgentRelease)
	}
}

package runtime

import "testing"

func TestRuntimeServiceLabelMatchesLaunchAgentLabel(t *testing.T) {
	if RuntimeServiceLabel != "dev.agx.runtime" {
		t.Fatalf("RuntimeServiceLabel = %q, want dev.agx.runtime", RuntimeServiceLabel)
	}
	if LaunchAgentLabel != RuntimeServiceLabel {
		t.Fatalf("LaunchAgentLabel = %q, want %q", LaunchAgentLabel, RuntimeServiceLabel)
	}
}

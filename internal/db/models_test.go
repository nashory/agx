package db

import "testing"

func TestParseTaskStatus(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    TaskStatus
		wantErr bool
	}{
		{name: "active with case and spaces", input: " Active ", want: StatusActive},
		{name: "waiting", input: "waiting", want: StatusWaiting},
		{name: "complete", input: "complete", want: StatusComplete},
		{name: "offline", input: "offline", want: StatusOffline},
		{name: "invalid", input: "blocked", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTaskStatus(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseTaskStatus(%q) succeeded, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTaskStatus(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseTaskStatus(%q) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

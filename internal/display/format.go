package display

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func ShortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func Age(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func Truncate(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	if width <= 3 {
		return truncateDisplay(s, width)
	}
	return truncateDisplay(s, width-3) + "..."
}

func truncateDisplay(s string, width int) string {
	var b strings.Builder
	used := 0
	for _, r := range s {
		part := string(r)
		w := lipgloss.Width(part)
		if used+w > width {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String()
}

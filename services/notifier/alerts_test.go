package notifier

import (
	"strings"
	"testing"
)

// TestFormatAlertFDThresholdExceeded guards delivery of the fd-exhaustion alert.
// formatAlert returns ("", "") for event types it does not handle, and
// handleEvent drops those silently. If the EventFDThresholdExceeded case is
// missing, the alert is published but never reaches any notifier.
func TestFormatAlertFDThresholdExceeded(t *testing.T) {
	h := NewAlertHandler(AlertConfig{})
	ev := NewEvent(EventFDThresholdExceeded, SeverityCritical, "test").
		WithData("open_fds", 52429).
		WithData("limit_fds", uint64(65536)).
		WithData("details", map[string]interface{}{"percent": 80, "socket_states": map[string]int{"CLOSE_WAIT": 40000}})

	subject, message := h.formatAlert(ev)

	if subject == "" {
		t.Fatal("EventFDThresholdExceeded produced empty subject: it would be dropped and never delivered")
	}
	if !strings.Contains(message, "52429") || !strings.Contains(message, "65536") {
		t.Errorf("alert message missing fd counts, got: %q", message)
	}
}

package notifications

import "testing"

func TestMacOSAlwaysHasANotifier(t *testing.T) {
	if !Supported() {
		t.Fatal("Supported() is false on macOS")
	}
	want := "Notification Center accepted Mate's test notification. If no banner appeared, check System Settings > Notifications > Mate and Focus."
	if got := AcceptedNote(); got != want {
		t.Fatalf("AcceptedNote() = %q, want %q", got, want)
	}
}

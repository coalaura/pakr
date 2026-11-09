package fl2test

import "testing"

func TestQueuedTimeout(t *testing.T) {
	message := queuedTimeout()
	if message != "" {
		t.Fatal(message)
	}
}

func TestQueuedFinalization(t *testing.T) {
	message := queuedFinalization()
	if message != "" {
		t.Fatal(message)
	}
}

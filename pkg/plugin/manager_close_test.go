package plugin

import (
	"errors"
	"testing"
)

// The closers the Manager registers panic when a plugin's Close returns an
// error (asserted by the Test*CloserPanicsOnError tests). At shutdown that
// must not stop the remaining plugins from being closed: a publisher whose
// broker connection is already gone must not prevent the cache client, the
// telemetry exporter or the others from being closed and flushed.
func TestManagerCloseAll_RunsEveryCloserEvenIfOnePanics(t *testing.T) {
	var ran []string
	m := &Manager{closers: []func(){
		func() { ran = append(ran, "first") },
		func() { panic(errors.New("amqp: connection already closed")) },
		func() { ran = append(ran, "third") },
	}}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("closeAll let a closer's panic escape: %v", r)
			}
		}()
		m.closeAll()
	}()

	if len(ran) != 2 || ran[0] != "first" || ran[1] != "third" {
		t.Errorf("closers run = %v, want [first third]", ran)
	}
}

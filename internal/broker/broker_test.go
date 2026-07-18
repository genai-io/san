package broker

import "testing"

func TestSendRoutesToRecipientOnly(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	var toA, toB int
	Register("a", func(Message) bool { toA++; return true })
	Register("b", func(Message) bool { toB++; return true })

	if !Send(Message{To: "a", Content: "hi"}) {
		t.Fatal("send to a registered address should report delivered")
	}
	if toA != 1 || toB != 0 {
		t.Fatalf("only the addressed recipient should receive: a=%d b=%d", toA, toB)
	}
}

func TestSendToUnregisteredAddressIsDropped(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	if Send(Message{To: "ghost", Content: "into the void"}) {
		t.Fatal("send to an unregistered address should report not delivered")
	}
}

func TestUnregisterStopsDelivery(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	var got int
	Register("a", func(Message) bool { got++; return true })
	Send(Message{To: "a"})
	Unregister("a")
	if Send(Message{To: "a"}) {
		t.Fatal("an unregistered address should not receive")
	}
	if got != 1 {
		t.Fatalf("delivery count = %d, want 1", got)
	}
}

func TestUnregisterIsIdempotent(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	Unregister("never-registered") // must not panic
}

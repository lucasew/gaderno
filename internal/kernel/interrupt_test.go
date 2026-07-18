package kernel

import (
	"context"
	"testing"
)

func TestInterruptNoConnection(t *testing.T) {
	m := &Manager{}
	if err := m.Interrupt(context.Background()); err == nil {
		t.Fatal("expected error when Conn is nil")
	}
}

func TestInterruptRespectsCanceledContext(t *testing.T) {
	m := &Manager{Conn: &Conn{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Interrupt(ctx); err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestInterruptRequestHeader(t *testing.T) {
	// Protocol: interrupt_request is a control-channel message with empty content.
	h := NewHeader("sess", "interrupt_request")
	if h.MsgType != "interrupt_request" {
		t.Fatalf("msg_type=%q", h.MsgType)
	}
	if h.Session != "sess" || h.MsgID == "" {
		t.Fatalf("header incomplete: %+v", h)
	}
}

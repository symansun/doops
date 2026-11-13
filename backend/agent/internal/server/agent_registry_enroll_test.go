package server

import (
	"errors"
	"testing"
)

func TestAgentRegistryRefusesUnenrolledSessionAfterEnrollmentConfirmed(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	registry, err := NewAgentRegistry(store)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	// An open (unenrolled) session registers while no enrollment is confirmed.
	open := newGatewayAgentSession("dev", "local", "", "198.51.100.1:1", nil, nil)
	if err := registry.Register(open); err != nil {
		t.Fatalf("open registration before enrollment: %v", err)
	}
	created, err := store.CreateAgentEnrollment("dev", "local")
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	// Issuing without confirming keeps the target open.
	if err := registry.Register(newGatewayAgentSession("dev", "local", "", "198.51.100.1:2", nil, nil)); err != nil {
		t.Fatalf("unconfirmed enrollment must keep the target open: %v", err)
	}

	enrolled := newGatewayAgentSession("dev", "local", created.ID, "198.51.100.2:1", nil, nil)
	if err := store.ConfirmAgentEnrollment(created.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := registry.Register(enrolled); err != nil {
		t.Fatalf("enrolled registration: %v", err)
	}

	// A session that passed the handler check before confirmation must not
	// replace the enrolled one.
	late := newGatewayAgentSession("dev", "local", "", "203.0.113.5:1", nil, nil)
	if err := registry.Register(late); !errors.Is(err, ErrAgentEnrollmentRequired) {
		t.Fatalf("late unenrolled registration error = %v", err)
	}
	if current := registry.Get("dev", "local"); current != enrolled {
		t.Fatal("unenrolled session replaced the enrolled agent")
	}

	// Other targets are unaffected.
	if err := registry.Register(newGatewayAgentSession("dev", "other", "", "198.51.100.3:1", nil, nil)); err != nil {
		t.Fatalf("other target: %v", err)
	}

	// Revoking the enrollment is the documented recovery path back to open
	// registration.
	if err := store.RevokeAgentEnrollment(created.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := registry.Register(newGatewayAgentSession("dev", "local", "", "198.51.100.1:3", nil, nil)); err != nil {
		t.Fatalf("registration after revoke: %v", err)
	}
}

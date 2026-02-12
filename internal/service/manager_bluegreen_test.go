package service

import (
	"testing"

	"github.com/buildvigil/agent/internal/container"
)

func TestSelectBlueGreenTargetPort(t *testing.T) {
	pair := container.PortPair{BluePort: 3002, GreenPort: 3003}

	t.Run("active blue deploys to green", func(t *testing.T) {
		target, err := selectBlueGreenTargetPort(3002, pair)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target != 3003 {
			t.Fatalf("expected target port 3003, got %d", target)
		}
	})

	t.Run("active green deploys to blue", func(t *testing.T) {
		target, err := selectBlueGreenTargetPort(3003, pair)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target != 3002 {
			t.Fatalf("expected target port 3002, got %d", target)
		}
	})

	t.Run("zero active port defaults to green", func(t *testing.T) {
		target, err := selectBlueGreenTargetPort(0, pair)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target != 3003 {
			t.Fatalf("expected target port 3003, got %d", target)
		}
	})

	t.Run("active port outside pair returns error", func(t *testing.T) {
		_, err := selectBlueGreenTargetPort(3999, pair)
		if err == nil {
			t.Fatal("expected error for mismatched active port")
		}
	})

	t.Run("invalid pair returns error", func(t *testing.T) {
		_, err := selectBlueGreenTargetPort(3002, container.PortPair{})
		if err == nil {
			t.Fatal("expected error for invalid pair")
		}
	})
}

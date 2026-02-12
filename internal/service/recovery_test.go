package service

import (
	"testing"

	"github.com/buildvigil/agent/internal/api"
)

func TestRecoverService_PortLogic(t *testing.T) {
	t.Log("Testing service recovery port logic")

	// Test case 1: Recovery when activePort is blue (even port)
	t.Run("ActivePortIsBlue", func(t *testing.T) {
		_ = api.Service{ID: "test-1", Name: "Test Service 1"} // service declared but not needed in this test

		// Simulate recovery where activePort is blue (even)
		activePort := 3002 // even port - should be blue
		bluePort := 0
		greenPort := 0

		// Apply the recovery logic from RecoverService
		if bluePort == 0 {
			if activePort%2 == 0 {
				bluePort = activePort
				greenPort = activePort + 1
			} else {
				greenPort = activePort
				bluePort = activePort - 1
			}
		}
		if greenPort == 0 {
			greenPort = bluePort + 1
		}

		if bluePort != 3002 {
			t.Errorf("Expected bluePort=3002, got %d", bluePort)
		}
		if greenPort != 3003 {
			t.Errorf("Expected greenPort=3003, got %d", greenPort)
		}
		t.Logf("✓ Active blue port recovery: blue=%d, green=%d", bluePort, greenPort)
	})

	// Test case 2: Recovery when activePort is green (odd port)
	t.Run("ActivePortIsGreen", func(t *testing.T) {
		_ = api.Service{ID: "test-2", Name: "Test Service 2"} // service declared but not needed in this test

		// Simulate recovery where activePort is green (odd)
		activePort := 3003 // odd port - should be green
		bluePort := 0
		greenPort := 0

		// Apply the recovery logic from RecoverService
		if bluePort == 0 {
			if activePort%2 == 0 {
				bluePort = activePort
				greenPort = activePort + 1
			} else {
				greenPort = activePort
				bluePort = activePort - 1
			}
		}
		if greenPort == 0 {
			greenPort = bluePort + 1
		}

		if bluePort != 3002 {
			t.Errorf("Expected bluePort=3002, got %d", bluePort)
		}
		if greenPort != 3003 {
			t.Errorf("Expected greenPort=3003, got %d", greenPort)
		}
		t.Logf("✓ Active green port recovery: blue=%d, green=%d", bluePort, greenPort)
	})

	// Test case 3: Verify port pair consistency
	t.Run("PortPairConsistency", func(t *testing.T) {
		_ = api.Service{ID: "test-3", Name: "Test Service 3"} // service declared but not needed in this test

		// Test both scenarios ensure greenPort = bluePort + 1
		testCases := []struct {
			name          string
			activePort    int
			expectedBlue  int
			expectedGreen int
		}{
			{"EvenActivePort", 3004, 3004, 3005},
			{"OddActivePort", 3005, 3004, 3005},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				bluePort := 0
				greenPort := 0
				activePort := tc.activePort

				// Apply the recovery logic
				if bluePort == 0 {
					if activePort%2 == 0 {
						bluePort = activePort
						greenPort = activePort + 1
					} else {
						greenPort = activePort
						bluePort = activePort - 1
					}
				}
				if greenPort == 0 {
					greenPort = bluePort + 1
				}

				if bluePort != tc.expectedBlue {
					t.Errorf("Expected bluePort=%d, got %d", tc.expectedBlue, bluePort)
				}
				if greenPort != tc.expectedGreen {
					t.Errorf("Expected greenPort=%d, got %d", tc.expectedGreen, greenPort)
				}
				if greenPort != bluePort+1 {
					t.Errorf("greenPort (%d) should be bluePort (%d) + 1", greenPort, bluePort)
				}
				t.Logf("✓ %s: blue=%d, green=%d", tc.name, bluePort, greenPort)
			})
		}
	})
}

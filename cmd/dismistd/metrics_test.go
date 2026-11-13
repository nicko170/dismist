package main

import "testing"

func TestMetricsListenerAcceptsOnlyNumericLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "192.0.2.5:0", ":0", "localhost:0", "[::]:0"} {
		listener, err := metricsListener(address)
		if err == nil {
			listener.Close()
			t.Errorf("accepted %q", address)
		}
	}
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		listener, err := metricsListener(address)
		if err != nil {
			t.Errorf("rejected %q: %v", address, err)
			continue
		}
		listener.Close()
	}
}

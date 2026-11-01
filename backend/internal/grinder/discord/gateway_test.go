package discord

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gorilla/websocket"
)

func TestIsAuthFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"bad token, wrapped like run does", fmt.Errorf("read: %w", &websocket.CloseError{Code: 4004, Text: "Authentication failed."}), true},
		{"other close code", fmt.Errorf("read: %w", &websocket.CloseError{Code: 4000, Text: "Unknown error"}), false},
		{"abnormal closure", fmt.Errorf("read: %w", &websocket.CloseError{Code: websocket.CloseAbnormalClosure}), false},
		{"not a close error", errors.New("server requested reconnect"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAuthFailure(c.err); got != c.want {
				t.Fatalf("isAuthFailure(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

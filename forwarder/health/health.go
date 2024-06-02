package health

import (
	"context"
	"errors"
	"log/slog"
	"net"
)

type Status int

const (
	INIT Status = iota
	SUCCESS
	FAILED
)

type HealthChecker interface {
	Check(ctx context.Context) (stat Status, changed bool, err error)
}

type TCP struct {
	Addr string

	status Status
	d      net.Dialer
	logger slog.Logger
}

func (h *TCP) Check(ctx context.Context) (stat Status, changed bool, err error) {
	stat = SUCCESS
	changed = true
	// Attempt a dial
	conn, err := h.d.DialContext(ctx, "tcp", h.Addr)
	if err != nil {
		stat = FAILED
	} else {
		defer conn.Close()
	}
	// Don't return error due to timeout since it is expected
	if errors.Is(err, context.Canceled) {
		err = nil
	}

	// Check if changed
	if h.status == stat {
		changed = false
	}
	// Store new result
	h.status = stat

	return
}

package health

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"
	"golang.org/x/net/nettest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func runTestListener(t testing.TB, ctx context.Context) string {
	l, err := nettest.NewLocalListener("tcp")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	return l.Addr().String()
}

func TestRunHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	addr := runTestListener(t, ctx)
	tcp := &TCP{
		Addr:   addr,
		status: 0,
		d:      net.Dialer{},
		logger: slog.Logger{},
	}
	stat, changed, err := tcp.Check(ctx)
	assert.Equal(t, stat, SUCCESS)
	assert.True(t, changed)
	assert.Nil(t, err)
}

func TestRunUnealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tcp := &TCP{
		Addr:   "",
		status: 0,
		d:      net.Dialer{},
		logger: slog.Logger{},
	}
	stat, changed, err := tcp.Check(ctx)
	assert.Equal(t, FAILED, stat)
	assert.True(t, changed)
	assert.NotNil(t, err)
}

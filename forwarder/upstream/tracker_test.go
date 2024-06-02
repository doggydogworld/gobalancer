package upstream

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type fakeErr struct {
	error
}

var key = struct{}{}

func fakeLongRunner(t *testing.T, ctx context.Context, expect error) {
	<-ctx.Done()
	assert.ErrorIs(t, context.Cause(ctx), expect)
}

func assertWGFinishes(t *testing.T, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		done <- struct{}{}
	}()
	select {
	case <-time.After(time.Second):
		t.Errorf("waitgroup waited too long which means a context wasn't cancelled correctly")
	case <-done:
	}
}

// Making sure that if the backend context is cancelled through
// removal of a backend that the context should be cancelled
func TestBackendContextCancelPropagates(t *testing.T) {
	// This ctx is to ensure that all resources are cleaned up
	// good for checking for goroutine leaks
	track := NewTracker(context.Background(), "test")
	defer track.Cancel(ErrBackendRemoved)
	track.TrackBackend("127.0.0.1:8000")

	wg := &sync.WaitGroup{}
	for range 10 {
		// Contexts created from NextWithContext should be cancelled when backend is removed
		_, ctx, _, _ := track.NextWithContext(context.Background())
		wg.Add(1)
		go func() {
			fakeLongRunner(t, ctx, ErrBackendUnhealthy)
			wg.Done()
		}()
	}

	// Untracking a backend will cancel the backends context and if they are derived
	// correctly it should cancel all of the goroutines above
	track.UntrackBackend("127.0.0.1:8000", ErrBackendUnhealthy)
	assertWGFinishes(t, wg)
}

// Just ensuring that if the request scoped context is cancelled
// if will cancel as normal
func TestOuterContextCancelPropagates(t *testing.T) {
	track := NewTracker(context.Background(), "test")
	defer track.Cancel(ErrBackendRemoved)
	track.TrackBackend("127.0.0.1:8000")

	wg := &sync.WaitGroup{}
	parentContext, parentContextCancel := context.WithCancelCause(context.Background())
	for range 10 {
		_, inCtx, _, _ := track.NextWithContext(context.WithValue(parentContext, key, "key"))
		wg.Add(1)
		go func() {
			fakeLongRunner(t, inCtx, fakeErr{})
			wg.Done()
		}()
	}
	assert.Eventually(t, func() bool { return track.BackendActiveConns("127.0.0.1:8000") == 10 }, time.Second, time.Millisecond)

	// Should cancel all contexts and "free" resources. This should reflect connections completing and the associated
	// context being cancelled to free any resources associated with it which includes tracking it.
	parentContextCancel(fakeErr{})
	assertWGFinishes(t, wg)

	// Cancelled contexts should no longer be tracked and since all have been cancelled ensure that 0 active connections exist
	assert.Eventuallyf(
		t,
		func() bool { return track.BackendActiveConns("127.0.0.1:8000") == 0 },
		time.Second,
		10*time.Millisecond,
		"expected 0 got %d", track.BackendActiveConns("127.0.0.1:8000"),
	)
}

func assertExpectedLengths(t *Tracker, addrs []string, expectedLength []int) bool {
	for i, addr := range addrs {
		numConns := t.BackendActiveConns(addr)
		if numConns != expectedLength[i] {
			fmt.Printf("backend at index %d expected length %d but got %d\n", i, expectedLength[i], numConns)
			return false
		}
	}
	return true
}

func (t *Tracker) addCtxDirectly(ctx context.Context, addr string) (context.Context, context.CancelFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.healthyBackends[addr][ctx] = struct{}{}
	return t.trackCtx(ctx, ctx, addr)
}

// Testing least connections pick
// This test will cover this scenario
//
// There are 3 listeners setup: l1, l2, l3
//
//   - l1 will have 5 active connections
//   - l2 will have 3 active connections
//   - l3 will have 0 active connections
//
// # There will be 10 total incoming connections
//
//   - 3 connections will be received (3 total) : [5, 3, 3]
//   - 4 connections will be released from l1 : [1, 3, 3]
//   - 2 connections will be received (5 total) : [3, 3, 3]
//   - 2 connections will be released from l2 : [3, 1, 3]
//   - 2 connections will be received (7 total) : [3, 3, 3]
//   - 3 connections will be received (10 total) : [4, 4, 4]
func TestLeastConnections(t *testing.T) {

	l1 := "127.0.0.1:8000"
	l2 := "127.0.0.1:8001"
	l3 := "127.0.0.1:8002"
	listeners := []string{l1, l2, l3}

	track := NewTracker(context.Background(), "test")
	defer track.Cancel(ErrBackendRemoved)

	parentReqContext, parentReqCancel := context.WithCancel(context.Background())

	// There are 3 listeners setup: l1, l2, l3
	//
	//   - l1 will have 5 active connections
	//   - l2 will have 3 active connections
	//   - l3 will have 0 active connections
	//
	track.TrackBackend(l1)
	track.TrackBackend(l2)
	track.TrackBackend(l3)

	l1Cancels := []context.CancelFunc{}
	l2Cancels := []context.CancelFunc{}
	for range 5 {
		_, newCtxCancel := track.addCtxDirectly(context.WithValue(parentReqContext, key, nil), l1)
		l1Cancels = append(l1Cancels, newCtxCancel)
	}
	for range 3 {
		_, newCtxCancel := track.addCtxDirectly(context.WithValue(parentReqContext, key, nil), l2)
		l2Cancels = append(l2Cancels, newCtxCancel)
	}
	// - 3 connections will be received (3 total) : [5, 3, 3]
	for range 3 {
		newCtx := context.WithValue(parentReqContext, key, nil)
		track.NextWithContext(newCtx)
	}
	assert.True(t, assertExpectedLengths(track, listeners, []int{5, 3, 3}))
	// - 4 connections will be released from l1 : [1, 3, 3]
	for i := range 4 {
		l1Cancels[i]()
	}
	// Unfortunately due to the nature of how scheduling works and that cleanup happens in a goroutine after cancel we don't
	// currently have a mechanism for waitin for cancels but we should expect it to happen quickly
	// We could implement a channel in trackCtx to specifically signal when they're happening but might be too annoying
	assert.Eventually(t, func() bool { return assertExpectedLengths(track, listeners, []int{1, 3, 3}) }, time.Second, time.Millisecond)
	// - 2 connections will be received (5 total) : [3, 3, 3]
	for range 2 {
		newCtx := context.WithValue(parentReqContext, key, nil)
		track.NextWithContext(newCtx)
	}
	assert.True(t, assertExpectedLengths(track, listeners, []int{3, 3, 3}))
	// - 2 connections will be released from l2 : [3, 1, 3]
	for i := range 2 {
		l2Cancels[i]()
	}
	assert.Eventually(t, func() bool { return assertExpectedLengths(track, listeners, []int{3, 1, 3}) }, time.Second, time.Millisecond)
	// - 2 connections will be received (7 total) : [3, 3, 3]
	for range 2 {
		newCtx := context.WithValue(parentReqContext, key, nil)
		track.NextWithContext(newCtx)
	}
	assert.True(t, assertExpectedLengths(track, listeners, []int{3, 3, 3}))
	// - 3 connections will be received (10 total) : [4, 4, 4]
	for range 3 {
		newCtx := context.WithValue(parentReqContext, key, nil)
		track.NextWithContext(newCtx)
	}
	assert.True(t, assertExpectedLengths(track, listeners, []int{4, 4, 4}))

	// Cleanup and ensure that there are no more active conns
	parentReqCancel()
	assert.Eventually(t, func() bool { return assertExpectedLengths(track, listeners, []int{0, 0, 0}) }, time.Second, time.Millisecond)
}

package msm8916

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// fakeMMCLI answers the read-only query cascade of queryModemStatus with one
// registered modem and a ready SIM, mirroring the fixtures in observer_test.go.
func fakeMMCLI(args []string) (string, error) {
	switch {
	case reflect.DeepEqual(args, []string{"-K", "-L"}):
		return "modem-list.length : 1\nmodem-list.value[1] : /org/freedesktop/ModemManager1/Modem/1\n", nil
	case reflect.DeepEqual(args, []string{"-K", "-m", "/org/freedesktop/ModemManager1/Modem/1"}):
		return "modem.generic.state : registered\nmodem.generic.sim : /org/freedesktop/ModemManager1/SIM/1\nmodem.generic.access-technologies.value[1] : lte\nmodem.generic.signal-quality.value : 81\nmodem.3gpp.registration-state : home\n", nil
	case reflect.DeepEqual(args, []string{"-K", "-m", "/org/freedesktop/ModemManager1/Modem/1", "--signal-get"}):
		return "modem.signal.lte.rsrp : -95\n", nil
	case reflect.DeepEqual(args, []string{"-K", "-i", "/org/freedesktop/ModemManager1/SIM/1"}):
		return "sim.properties.iccid : 8900000000000000000\nsim.properties.imsi : 001010000000001\n", nil
	default:
		return "", errors.New("unexpected command")
	}
}

func TestStatusQueriesAreCachedWithinTTL(t *testing.T) {
	var calls int
	backend := New(Options{ReadOnly: true, Run: func(_ context.Context, args ...string) (string, error) {
		calls++
		return fakeMMCLI(args)
	}})
	first, err := backend.GetStatus(context.Background())
	if err != nil || first.State != proto.ModemReady {
		t.Fatalf("first status = %#v, %v", first, err)
	}
	second, err := backend.GetStatus(context.Background())
	if err != nil || second.State != proto.ModemReady {
		t.Fatalf("second status = %#v, %v", second, err)
	}
	if calls != 4 {
		t.Fatalf("expected one query cascade of 4 subprocess calls, got %d", calls)
	}
	if !first.ObservedAt.Equal(second.ObservedAt) {
		t.Fatalf("second status was recomputed: %s vs %s", first.ObservedAt, second.ObservedAt)
	}
}

func TestStatusCacheExpiresAfterTTL(t *testing.T) {
	current := time.Now()
	var calls int
	backend := New(Options{ReadOnly: true, Now: func() time.Time { return current }, Run: func(_ context.Context, args ...string) (string, error) {
		calls++
		return fakeMMCLI(args)
	}})
	if _, err := backend.GetStatus(context.Background()); err != nil {
		t.Fatalf("first status: %v", err)
	}
	current = current.Add(statusTTL + time.Second)
	if _, err := backend.GetStatus(context.Background()); err != nil {
		t.Fatalf("second status: %v", err)
	}
	if calls != 8 {
		t.Fatalf("expected the expired cache to trigger a second cascade, got %d subprocess calls", calls)
	}
}

func TestConcurrentStatusCallersCoalesceIntoOneCascade(t *testing.T) {
	var calls atomic.Int32
	backend := New(Options{ReadOnly: true, Run: func(_ context.Context, args ...string) (string, error) {
		calls.Add(1)
		time.Sleep(25 * time.Millisecond)
		return fakeMMCLI(args)
	}})
	var group sync.WaitGroup
	for index := 0; index < 4; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			status, err := backend.GetStatus(context.Background())
			if err != nil || status.State != proto.ModemReady {
				t.Errorf("status = %#v, %v", status, err)
			}
		}()
	}
	group.Wait()
	if got := calls.Load(); got != 4 {
		t.Fatalf("expected 4 concurrent callers to share one cascade (4 subprocess calls), got %d", got)
	}
}

func TestOfflineStatusIsAlsoCached(t *testing.T) {
	var calls int
	backend := New(Options{ReadOnly: true, Run: func(context.Context, ...string) (string, error) {
		calls++
		return "", errors.New("ModemManager is not active")
	}})
	first, err := backend.GetStatus(context.Background())
	if err != nil || first.State != proto.ModemOffline {
		t.Fatalf("first status = %#v, %v", first, err)
	}
	second, err := backend.GetStatus(context.Background())
	if err != nil || second.State != proto.ModemOffline {
		t.Fatalf("second status = %#v, %v", second, err)
	}
	if calls != 1 {
		t.Fatalf("an inactive ModemManager must not be probed on every request, subprocess calls = %d", calls)
	}
}

func TestStatusAccessorsShareOneCascade(t *testing.T) {
	var calls int
	backend := New(Options{ReadOnly: true, Run: func(_ context.Context, args ...string) (string, error) {
		calls++
		return fakeMMCLI(args)
	}})
	status, _ := backend.GetStatus(context.Background())
	sim, _ := backend.GetSIMInfo(context.Background())
	signal, _ := backend.GetSignal(context.Background())
	if status.SIM.ICCID == "" || sim.ICCID == "" || signal.RSRP == nil {
		t.Fatalf("accessors did not observe the modem: %#v %#v %#v", status.SIM, sim, signal)
	}
	if calls != 4 {
		t.Fatalf("expected GetStatus/GetSIMInfo/GetSignal to share one cached cascade, got %d subprocess calls", calls)
	}
}

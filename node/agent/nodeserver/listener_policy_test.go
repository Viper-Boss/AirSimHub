package nodeserver

import (
	"context"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/nassimhub-node/agent/modembackend/mock"
	"github.com/human-agent65535/nassimhub-node/agent/netbackend"
	netmock "github.com/human-agent65535/nassimhub-node/agent/netbackend/mock"
)

// The policy is the whole reason a KCP connection cannot be handed a weaker
// request budget than a TCP one, so the contract is pinned here: a timeout
// somebody deletes or weakens fails this test instead of failing on a device.
//
// Offline: no network is needed beyond loopback listeners with ephemeral ports.
func TestNodeListenerPolicyBindsEveryRequestAndNeverEndsACall(t *testing.T) {
	policy := nodeListenerPolicy()
	if policy.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout must be positive, got %v", policy.ReadHeaderTimeout)
	}
	// The OTA artifact upload (PUT /v1/ota/artifact) is a resumable body of up
	// to proto.OTAMaxArtifactBytes; below a quarter of an hour the deadline
	// starts cutting honest pushes on a weak link, which the protocol survives
	// by resuming but should not have to.
	if policy.ReadTimeout < 15*time.Minute {
		t.Fatalf("ReadTimeout must leave room for a slow OTA upload, got %v", policy.ReadTimeout)
	}
	if policy.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout must be positive, got %v", policy.IdleTimeout)
	}
	if policy.MaxHeaderBytes <= 0 || policy.MaxHeaderBytes > 16<<10 {
		t.Fatalf("MaxHeaderBytes must be set and no larger than the console's, got %d", policy.MaxHeaderBytes)
	}
	// WriteTimeout is deliberately absent: GET /v1/calls/{id}/media streams
	// call audio for the length of the call, and any finite write deadline
	// would hang up every call at the same moment regardless of the
	// conversation. This clause fails if somebody adds one without reading
	// this.
	if _, ok := reflect.TypeOf(policy).FieldByName("WriteTimeout"); ok {
		t.Fatal("listenerPolicy must not gain a WriteTimeout; call audio has no finite write deadline")
	}
}

// testNode assembles the smallest Node that can Start: plain HTTP on loopback
// ephemeral ports, mock modem and Wi-Fi backends, throw-away state.
func testNode(t *testing.T, options Options) *Server {
	t.Helper()
	options.StateDir = t.TempDir()
	if len(options.Listeners) == 0 {
		options.Listeners = []string{"127.0.0.1:0"}
	}
	if options.Modem == nil {
		options.Modem = mock.New(mock.Options{})
	}
	if options.Network == nil && options.NetworkFactory == nil {
		options.NetworkFactory = func(string) netbackend.Backend {
			return netmock.New(netmock.Options{})
		}
	}
	node, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

// Addresses reads what Start wrote and what Stop cleared, from goroutines the
// server did not wait for, so the paths the srvMu lock protects are exercised
// end to end: a reader must see every listener or none, never a slice that is
// being appended to.
func TestAddressesTrackListenersAcrossStartAndStop(t *testing.T) {
	node := testNode(t, Options{Listeners: []string{"127.0.0.1:0", "127.0.0.1:0"}})
	if got := node.Addresses(); len(got) != 0 {
		t.Fatalf("before Start the node listens nowhere, got %v", got)
	}
	if err := node.Start(); err != nil {
		t.Fatal(err)
	}
	addresses := node.Addresses()
	if len(addresses) != 2 {
		t.Fatalf("two configured listeners must report two addresses, got %v", addresses)
	}
	for _, address := range addresses {
		_, portText, err := net.SplitHostPort(address)
		if err != nil || portText == "0" {
			t.Fatalf("an ephemeral listener must resolve to a real port, got %q (err %v)", address, err)
		}
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := node.Addresses(); len(got) != 0 {
		t.Fatalf("after Stop the node listens nowhere, got %v", got)
	}
	// Stop is idempotent: a second call must neither panic nor block.
	if err := node.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A reader of Addresses while Start is still opening listeners, and while Stop
// is shutting them down, must not race the lifecycle. Without srvMu this test
// ranges over a slice another goroutine appends to or clears.
func TestAddressesSurviveConcurrentLifecycle(t *testing.T) {
	node := testNode(t, Options{Listeners: []string{"127.0.0.1:0", "127.0.0.1:0"}})
	if err := node.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	var readers sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-finished:
					return
				default:
					_ = node.Addresses()
					_ = node.AdminAddress()
				}
			}
		}()
	}
	// Stop runs while the readers are still looping, which is the overlap the
	// lock exists for.
	if err := node.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(finished)
	readers.Wait()
}

// The console's reachability must be reported from the bound listener: a
// configuration that asked for an ephemeral port would otherwise tell the NAS
// "port 0" for as long as the agent runs.
func TestAdminPortIsTheBoundPortWhenConfiguredEphemeral(t *testing.T) {
	node := testNode(t, Options{
		EnableTLS: true, EnableLocalAdmin: true, AdminListen: "127.0.0.1:0",
	})
	if port := node.adminPort(); port != 0 {
		t.Fatalf("before Start only the configured ephemeral port exists, got %d", port)
	}
	if err := node.Start(); err != nil {
		t.Fatal(err)
	}
	if port := node.adminPort(); port <= 0 {
		t.Fatalf("after Start the bound port must be reported, got %d", port)
	}
	if node.AdminAddress() == "" {
		t.Fatal("AdminAddress must report the bound address")
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if node.AdminAddress() != "" {
		t.Fatalf("after Stop the console is not listening, got %q", node.AdminAddress())
	}
}

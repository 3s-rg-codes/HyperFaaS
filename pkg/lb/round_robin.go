// HyperFaaS round-robin load balancer implementation
// Based on : https://github.com/grpc/grpc-go/blob/master/examples/features/customloadbalancer/client/customroundrobin/customroundrobin.go
// TODO: add our custom logic to pick the best node.
// orca out-of-band metrics may be a way to do this. https://github.com/grpc/grpc-go/blob/master/examples/features/orca/client/main.go
package lb

import (
	"encoding/json"
	"sync/atomic"

	_ "google.golang.org/grpc" // to register pick_first
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/balancer/pickfirst/pickfirstleaf"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/serviceconfig"
)

func init() {
	balancer.Register(hyperFaaSRoundRobinBuilder{})
}

const hyperFaaSRRName = "hyperfaas_round_robin"

type hyperFaaSRRConfig struct {
	serviceconfig.LoadBalancingConfig `json:"-"`
}

type hyperFaaSRoundRobinBuilder struct{}

func (hyperFaaSRoundRobinBuilder) Name() string {
	return hyperFaaSRRName
}

func (hyperFaaSRoundRobinBuilder) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	return &hyperFaaSRRConfig{}, nil
}

func (hyperFaaSRoundRobinBuilder) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	hrr := &hyperFaaSRoundRobin{
		ClientConn: cc,
		bOpts:      bOpts,
	}
	hrr.Balancer = endpointsharding.NewBalancer(hrr, bOpts, balancer.Get(pickfirstleaf.Name).Build, endpointsharding.Options{})
	return hrr
}

type hyperFaaSRoundRobin struct {
	// All state and operations on this balancer are either initialized at build
	// time and read only after, or are only accessed as part of its
	// balancer.Balancer API (UpdateState from children only comes in from
	// balancer.Balancer calls as well, and children are called one at a time),
	// in which calls are guaranteed to come synchronously. Thus, no extra
	// synchronization is required in this balancer.
	balancer.Balancer
	balancer.ClientConn
	bOpts balancer.BuildOptions

	cfg atomic.Pointer[hyperFaaSRRConfig]
}

func (hrr *hyperFaaSRoundRobin) UpdateClientConnState(state balancer.ClientConnState) error {
	hrrCfg, ok := state.BalancerConfig.(*hyperFaaSRRConfig)
	if !ok {
		return balancer.ErrBadResolverState
	}
	hrr.cfg.Store(hrrCfg)
	// A call to UpdateClientConnState should always produce a new Picker.  That
	// is guaranteed to happen since the aggregator will always call
	// UpdateChildState in its UpdateClientConnState.
	return hrr.Balancer.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: state.ResolverState,
	})
}

func (hrr *hyperFaaSRoundRobin) UpdateState(state balancer.State) {
	if state.ConnectivityState == connectivity.Ready {
		childStates := endpointsharding.ChildStatesFromPicker(state.Picker)
		var readyPickers []balancer.Picker
		for _, childState := range childStates {
			if childState.State.ConnectivityState == connectivity.Ready {
				readyPickers = append(readyPickers, childState.State.Picker)
			}
		}
		// If we have ready children, use round robin picker
		if len(readyPickers) > 0 {
			picker := &hyperFaaSRRPicker{
				pickers: readyPickers,
				next:    0,
			}
			hrr.ClientConn.UpdateState(balancer.State{
				ConnectivityState: connectivity.Ready,
				Picker:            picker,
			})
			return
		}
	}
	// Delegate to default behavior/picker from below.
	hrr.ClientConn.UpdateState(state)
}

type hyperFaaSRRPicker struct {
	pickers []balancer.Picker
	next    uint32
}

func (p *hyperFaaSRRPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	if len(p.pickers) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	// Simple round-robin: increment counter and modulo by number of available pickers
	next := atomic.AddUint32(&p.next, 1)
	pickerIndex := (next - 1) % uint32(len(p.pickers))
	childPicker := p.pickers[pickerIndex]

	return childPicker.Pick(info)
}

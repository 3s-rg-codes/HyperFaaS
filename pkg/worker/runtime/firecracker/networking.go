package firecracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	netnsDir                    = "/var/run/netns"
	tapDeviceName               = "fc-tap"
	nsVethName                  = "fc-ns"
	rootVethLimit               = 15
	portProxyBackendDialTimeout = 5 * time.Second
	// defaultRefillInterval paces background refill builds so request-driven
	// network builds can take the build mutex between them.
	defaultRefillInterval = 50 * time.Millisecond
)

type networkConfig struct {
	Name        string
	Path        string
	TapName     string
	GuestIP     net.IP
	GatewayIP   net.IP
	GuestMAC    string
	HostVeth    string
	HostIP      net.IP
	NamespaceIP net.IP
	ExposedIP   net.IP
	pooled      bool
}

type networkManager struct {
	mu      sync.Mutex
	buildMu sync.Mutex

	internal networkAllocator
	exposed  networkAllocator

	logger *slog.Logger

	usePool        bool
	poolSize       int
	pool           chan *networkConfig
	poolIDCounter  uint64
	replenishMu    sync.Mutex
	refillOnBorrow bool
	refillInterval time.Duration
	statsInterval  time.Duration
	refillActive   atomic.Bool
	inUse          atomic.Int64
	borrowedTotal  atomic.Uint64
	onFlyTotal     atomic.Uint64
	releasedTotal  atomic.Uint64
	rebuiltTotal   atomic.Uint64
	buildTotal     atomic.Uint64
	buildFailed    atomic.Uint64
	buildTimeNS    atomic.Int64
	buildWaitNS    atomic.Int64

	workDir   string
	guestIP   net.IP
	gatewayIP net.IP
	guestMAC  string
}

type networkAllocator struct {
	network *net.IPNet
	next    uint32
}

func newNetworkManager(internalCIDR, exposedCIDR string, usePool bool, poolSize int, workDir string, guestIP, gatewayIP net.IP, guestMAC string, logger *slog.Logger) (*networkManager, error) {
	internal, err := newNetworkAllocator(internalCIDR, 1)
	if err != nil {
		return nil, err
	}
	exposed, err := newNetworkAllocator(exposedCIDR, 10)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	m := &networkManager{
		internal:  internal,
		exposed:   exposed,
		logger:    logger,
		usePool:   usePool,
		poolSize:  poolSize,
		workDir:   workDir,
		guestIP:   guestIP,
		gatewayIP: gatewayIP,
		guestMAC:  guestMAC,
		// Refill is enabled on borrow by default; the old policy only refilled
		// after a release or an empty-pool build. Knobs allow A/B tests on one binary.
		refillOnBorrow: true,
		refillInterval: defaultRefillInterval,
	}
	if raw := os.Getenv("HYPERFAAS_FC_REFILL_ON_BORROW"); raw != "" {
		onBorrow, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			logger.Warn("invalid firecracker refill-on-borrow flag", "value", raw)
		} else {
			m.refillOnBorrow = onBorrow
		}
	}
	if raw := os.Getenv("HYPERFAAS_FC_REFILL_INTERVAL"); raw != "" {
		interval, parseErr := time.ParseDuration(raw)
		if parseErr != nil || interval < 0 {
			logger.Warn("invalid firecracker refill interval", "value", raw)
		} else {
			m.refillInterval = interval
		}
	}
	if usePool && poolSize > 0 {
		m.pool = make(chan *networkConfig, poolSize)
		logger.Info("firecracker network pool refill policy",
			"pool_size", poolSize,
			"refill_on_borrow", m.refillOnBorrow,
			"refill_interval", m.refillInterval,
			"refill_threshold", m.refillThreshold(),
		)
	}
	if raw := os.Getenv("HYPERFAAS_FC_POOL_STATS_INTERVAL"); raw != "" {
		interval, parseErr := time.ParseDuration(raw)
		if parseErr != nil || interval <= 0 {
			logger.Warn("invalid firecracker pool stats interval", "value", raw)
		} else {
			m.statsInterval = interval
		}
	}
	return m, nil
}

func (m *networkManager) startStatsLoop() {
	if m.pool == nil || m.statsInterval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(m.statsInterval)
		defer ticker.Stop()
		for range ticker.C {
			m.logger.Info("firecracker network pool stats",
				"unix_nano", time.Now().UnixNano(),
				"available", len(m.pool),
				"capacity", m.poolSize,
				"in_use", m.inUse.Load(),
				"borrowed_total", m.borrowedTotal.Load(),
				"on_fly_total", m.onFlyTotal.Load(),
				"released_total", m.releasedTotal.Load(),
				"rebuilt_total", m.rebuiltTotal.Load(),
				"build_total", m.buildTotal.Load(),
				"build_failed", m.buildFailed.Load(),
				"build_time_ms", float64(m.buildTimeNS.Load())/1e6,
				"build_wait_ms", float64(m.buildWaitNS.Load())/1e6,
				"refill_active", m.refillActive.Load(),
			)
		}
	}()
}

func newNetworkAllocator(cidr string, offset uint32) (networkAllocator, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return networkAllocator{}, err
	}
	if ipnet.IP.To4() == nil {
		return networkAllocator{}, fmt.Errorf("only IPv4 networks are supported: %s", cidr)
	}
	return networkAllocator{network: ipnet, next: offset}, nil
}

func (a *networkAllocator) alloc() net.IP {
	base := a.network.IP.To4()
	value := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	value += a.next
	a.next++
	return net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

func (m *networkManager) populatePool() {
	m.logger.Info("pre-populating firecracker network pool", "size", m.poolSize)
	for i := 0; i < m.poolSize; i++ {
		cfg, err := m.buildNetwork(poolIDBase+uint64(i), "prepopulate")
		if err != nil {
			m.logger.Error("failed to pre-populate firecracker network", "index", i, "error", err)
			continue
		}
		cfg.pooled = true
		m.pool <- cfg
	}
	atomic.StoreUint64(&m.poolIDCounter, uint64(m.poolSize-1))
	m.logger.Info("firecracker network pool pre-population completed")
}

func (m *networkManager) acquire(instanceID uint64) (*networkConfig, error) {
	if !m.usePool || m.pool == nil {
		cfg, err := m.buildNetwork(instanceID, "direct")
		if err != nil {
			return nil, err
		}
		m.inUse.Add(1)
		return cfg, nil
	}
	select {
	case cfg := <-m.pool:
		if err := validateNetworkConfig(cfg); err != nil {
			m.logger.Warn("pooled firecracker network invalid, rebuilding", "ns", cfg.Name, "error", err)
			_ = m.destroyNetwork(cfg)
			rebuilt, buildErr := m.buildNetwork(poolIDFromConfig(cfg), "repair")
			if buildErr != nil {
				return nil, buildErr
			}
			rebuilt.pooled = true
			cfg = rebuilt
		}
		m.logger.Debug("borrowed firecracker network from pool", "ns", cfg.Name)
		m.borrowedTotal.Add(1)
		m.inUse.Add(1)
		if m.refillOnBorrow {
			m.maybeTriggerReplenish()
		}
		return cfg, nil
	default:
		m.logger.Info("firecracker network pool empty, creating network on the fly",
			"pool_available", len(m.pool),
			"pool_size", m.poolSize,
		)
		cfg, err := m.buildNetwork(instanceID, "on_fly")
		if err != nil {
			return nil, err
		}
		cfg.pooled = true
		m.onFlyTotal.Add(1)
		m.inUse.Add(1)
		m.maybeTriggerReplenish()
		return cfg, nil
	}
}

func (m *networkManager) release(cfg *networkConfig) error {
	if cfg == nil {
		return nil
	}
	m.inUse.Add(-1)
	if err := validateNetworkConfig(cfg); err != nil {
		m.logger.Warn("firecracker network missing on release, dropping", "ns", cfg.Name, "error", err)
		return m.destroyNetwork(cfg)
	}
	if cfg.pooled && m.usePool && m.pool != nil {
		select {
		case m.pool <- cfg:
			m.releasedTotal.Add(1)
			m.logger.Debug("returned firecracker network to pool", "ns", cfg.Name)
			m.triggerReplenish()
			return nil
		default:
			return m.destroyNetwork(cfg)
		}
	}
	return m.destroyNetwork(cfg)
}

// discard destroys a network instead of returning it to the pool. It is used
// when the sandbox process may not have exited, because a TAP device is held
// until its firecracker process exits: pooling such a network would make the
// next restore fail with "Open tap device failed: Resource busy".
func (m *networkManager) discard(cfg *networkConfig) error {
	if cfg == nil {
		return nil
	}
	m.inUse.Add(-1)
	return m.destroyNetwork(cfg)
}

func (m *networkManager) triggerReplenish() {
	if m.pool == nil {
		return
	}
	go m.replenishPool()
}

// refillThreshold is the idle network count below which the pool is considered
// low. The previous policy used the same fraction but only checked it on release
// or after an empty-pool build.
func (m *networkManager) refillThreshold() int {
	threshold := m.poolSize / 4
	if threshold < 1 {
		threshold = 1
	}
	return threshold
}

// maybeTriggerReplenish starts a background refill only when idle occupancy is
// low. Borrowing a network no longer leaves the pool drained until the next
// release or empty-pool build.
func (m *networkManager) maybeTriggerReplenish() {
	if m.pool == nil {
		return
	}
	if len(m.pool) >= m.refillThreshold() {
		return
	}
	m.triggerReplenish()
}

func (m *networkManager) replenishPool() {
	if m.pool == nil {
		return
	}
	if !m.replenishMu.TryLock() {
		return
	}
	defer m.replenishMu.Unlock()
	m.refillActive.Store(true)
	defer m.refillActive.Store(false)

	if len(m.pool) >= m.refillThreshold() {
		return
	}

	m.logger.Info("firecracker network pool refill started",
		"available", len(m.pool),
		"capacity", m.poolSize,
		"in_use", m.inUse.Load(),
		"interval", m.refillInterval,
	)
	built := 0
	for len(m.pool) < m.poolSize {
		if built > 0 && m.refillInterval > 0 {
			// Pace refill builds so request-driven and direct builds can take
			// the build mutex between them and guests keep CPU.
			time.Sleep(m.refillInterval)
		}
		id := atomic.AddUint64(&m.poolIDCounter, 1)
		cfg, err := m.buildNetwork(poolIDBase+id, "refill")
		if err != nil {
			m.logger.Warn("failed to replenish firecracker network pool", "error", err)
			return
		}
		cfg.pooled = true
		select {
		case m.pool <- cfg:
			m.rebuiltTotal.Add(1)
			built++
			m.logger.Debug("replenished firecracker network pool", "ns", cfg.Name)
		default:
			_ = m.destroyNetwork(cfg)
			return
		}
	}
	m.logger.Info("firecracker network pool refill completed",
		"available", len(m.pool),
		"built", built,
	)
}

func (m *networkManager) buildNetwork(instanceID uint64, source string) (result *networkConfig, resultErr error) {
	// Dirigent holds its pool lock through network creation. Serialize all network
	// construction here so an empty pool cannot fan out concurrent root-netns
	// veth/route mutations.
	started := time.Now()
	m.buildMu.Lock()
	waited := time.Since(started)
	selfCPUStart, childCPUStart := readCPUSeconds()
	defer func() {
		elapsed := time.Since(started)
		m.buildTotal.Add(1)
		m.buildTimeNS.Add(elapsed.Nanoseconds())
		m.buildWaitNS.Add(waited.Nanoseconds())
		if resultErr != nil {
			m.buildFailed.Add(1)
		}
		if m.statsInterval > 0 {
			selfCPUEnd, childCPUEnd := readCPUSeconds()
			m.logger.Info("firecracker network build",
				"source", source,
				"id", instanceID,
				"elapsed_ms", float64(elapsed.Nanoseconds())/1e6,
				"wait_ms", float64(waited.Nanoseconds())/1e6,
				"self_cpu_ms", (selfCPUEnd-selfCPUStart)*1000,
				"child_cpu_ms", (childCPUEnd-childCPUStart)*1000,
				"error", resultErr,
			)
		}
	}()
	defer m.buildMu.Unlock()

	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()

	m.mu.Lock()
	m.internal.next = ((m.internal.next+2)/4)*4 + 1
	hostIP := m.internal.alloc()
	nsIP := m.internal.alloc()
	exposedIP := m.exposed.alloc()
	m.mu.Unlock()

	name := fmt.Sprintf("hf-fc-%d", instanceID)
	if len(name) > rootVethLimit {
		name = fmt.Sprintf("hffc%d", instanceID%1_000_000_000)
	}
	hostVeth := name
	if len(hostVeth) > rootVethLimit {
		hostVeth = hostVeth[:rootVethLimit]
	}
	cfg := &networkConfig{
		Name:        fmt.Sprintf("hyperfaas-fc-%d", instanceID),
		Path:        filepath.Join(netnsDir, fmt.Sprintf("hyperfaas-fc-%d", instanceID)),
		TapName:     tapDeviceName,
		GuestIP:     m.guestIP,
		GatewayIP:   m.gatewayIP,
		GuestMAC:    m.guestMAC,
		HostVeth:    hostVeth,
		HostIP:      hostIP,
		NamespaceIP: nsIP,
		ExposedIP:   exposedIP,
	}

	if err := os.MkdirAll(m.workDir, 0o755); err != nil {
		return nil, fmt.Errorf("create network work dir: %w", err)
	}
	if err := os.MkdirAll(netnsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create netns dir: %w", err)
	}
	_ = netlink.LinkDel(&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: cfg.HostVeth}})
	_ = os.Remove(cfg.Path)

	origNS, err := netns.Get()
	if err != nil {
		return nil, fmt.Errorf("get current netns: %w", err)
	}
	defer origNS.Close()

	newNS, err := netns.NewNamed(cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("create netns %s: %w", cfg.Name, err)
	}
	defer newNS.Close()

	if err := netns.Set(origNS); err != nil {
		return nil, fmt.Errorf("restore root netns after create: %w", err)
	}

	peerName := fmt.Sprintf("ns-%d", instanceID)
	if len(peerName) > rootVethLimit {
		peerName = fmt.Sprintf("ns%d", instanceID%1_000_000)
	}

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: cfg.HostVeth, MTU: 1500},
		PeerName:  peerName,
	}
	if err := netlink.LinkAdd(veth); err != nil {
		_ = m.destroyNetwork(cfg)
		return nil, fmt.Errorf("create veth pair: %w", err)
	}
	peer, err := netlink.LinkByName(peerName)
	if err != nil {
		_ = m.destroyNetwork(cfg)
		return nil, fmt.Errorf("lookup veth peer: %w", err)
	}
	if err := netlink.LinkSetNsFd(peer, int(newNS)); err != nil {
		_ = m.destroyNetwork(cfg)
		return nil, fmt.Errorf("move veth peer to netns: %w", err)
	}
	hostLink, err := netlink.LinkByName(cfg.HostVeth)
	if err != nil {
		_ = m.destroyNetwork(cfg)
		return nil, fmt.Errorf("lookup host veth: %w", err)
	}
	if err := netlink.AddrAdd(hostLink, &netlink.Addr{IPNet: &net.IPNet{IP: cfg.HostIP, Mask: net.CIDRMask(30, 32)}}); err != nil {
		_ = m.destroyNetwork(cfg)
		return nil, fmt.Errorf("assign host veth IP: %w", err)
	}
	if err := netlink.LinkSetUp(hostLink); err != nil {
		_ = m.destroyNetwork(cfg)
		return nil, fmt.Errorf("bring host veth up: %w", err)
	}
	if err := netlink.RouteAdd(&netlink.Route{Dst: &net.IPNet{IP: cfg.ExposedIP, Mask: net.CIDRMask(32, 32)}, Gw: cfg.NamespaceIP, LinkIndex: hostLink.Attrs().Index}); err != nil && !errors.Is(err, os.ErrExist) {
		_ = m.destroyNetwork(cfg)
		return nil, fmt.Errorf("add exposed IP route: %w", err)
	}

	if err := withNetNS(newNS, func() error {
		lo, err := netlink.LinkByName("lo")
		if err == nil {
			_ = netlink.LinkSetUp(lo)
		}

		tapAttrs := netlink.NewLinkAttrs()
		tapAttrs.Name = cfg.TapName
		tapAttrs.MTU = 1500
		tap := &netlink.Tuntap{LinkAttrs: tapAttrs, Mode: netlink.TUNTAP_MODE_TAP}
		if err := netlink.LinkAdd(tap); err != nil {
			return fmt.Errorf("create tap: %w", err)
		}
		tapLink, err := netlink.LinkByName(cfg.TapName)
		if err != nil {
			return fmt.Errorf("lookup tap: %w", err)
		}
		if err := netlink.AddrAdd(tapLink, &netlink.Addr{IPNet: &net.IPNet{IP: cfg.GatewayIP, Mask: net.CIDRMask(30, 32)}}); err != nil {
			return fmt.Errorf("assign tap IP: %w", err)
		}
		if err := netlink.LinkSetUp(tapLink); err != nil {
			return fmt.Errorf("bring tap up: %w", err)
		}

		nsLink, err := netlink.LinkByName(peerName)
		if err != nil {
			return fmt.Errorf("lookup namespace veth by unique name: %w", err)
		}
		if err := netlink.LinkSetName(nsLink, nsVethName); err != nil {
			return fmt.Errorf("rename namespace veth to standard name: %w", err)
		}
		if err := netlink.AddrAdd(nsLink, &netlink.Addr{IPNet: &net.IPNet{IP: cfg.NamespaceIP, Mask: net.CIDRMask(30, 32)}}); err != nil {
			return fmt.Errorf("assign namespace veth IP: %w", err)
		}
		if err := netlink.LinkSetUp(nsLink); err != nil {
			return fmt.Errorf("bring namespace veth up: %w", err)
		}
		if err := netlink.RouteAdd(&netlink.Route{Gw: cfg.HostIP, LinkIndex: nsLink.Attrs().Index}); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("add namespace default route: %w", err)
		}
		if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644); err != nil {
			return fmt.Errorf("enable namespace forwarding: %w", err)
		}

		ipt, err := iptables.New()
		if err != nil {
			return fmt.Errorf("open iptables in netns: %w", err)
		}
		if err := ipt.AppendUnique("nat", "POSTROUTING", "-s", cfg.GuestIP.String(), "-o", nsVethName, "-j", "SNAT", "--to-source", cfg.ExposedIP.String()); err != nil {
			return fmt.Errorf("add namespace SNAT rule: %w", err)
		}
		if err := ipt.AppendUnique("nat", "PREROUTING", "-i", nsVethName, "-d", cfg.ExposedIP.String(), "-j", "DNAT", "--to-destination", cfg.GuestIP.String()); err != nil {
			return fmt.Errorf("add namespace DNAT rule: %w", err)
		}
		return nil
	}); err != nil {
		_ = m.destroyNetwork(cfg)
		return nil, err
	}

	return cfg, nil
}

func validateNetworkConfig(cfg *networkConfig) error {
	if cfg == nil {
		return errors.New("network config is nil")
	}
	if cfg.Path == "" {
		return errors.New("network path is empty")
	}
	if _, err := os.Stat(cfg.Path); err != nil {
		return fmt.Errorf("netns path %s: %w", cfg.Path, err)
	}
	return nil
}

func poolIDFromConfig(cfg *networkConfig) uint64 {
	if cfg == nil || cfg.Name == "" {
		return poolIDBase
	}
	var id uint64
	if _, err := fmt.Sscanf(cfg.Name, "hyperfaas-fc-%d", &id); err != nil {
		return poolIDBase
	}
	return id
}

func (m *networkManager) destroyNetwork(cfg *networkConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.ExposedIP != nil && cfg.HostVeth != "" {
		_ = netlink.RouteDel(&netlink.Route{Dst: &net.IPNet{IP: cfg.ExposedIP, Mask: net.CIDRMask(32, 32)}})
	}
	if cfg.HostVeth != "" {
		if link, err := netlink.LinkByName(cfg.HostVeth); err == nil {
			_ = netlink.LinkDel(link)
		}
	}
	if cfg.Name != "" {
		_ = netns.DeleteNamed(cfg.Name)
	}
	return nil
}

func withNetNS(ns netns.NsHandle, fn func() error) error {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()

	orig, err := netns.Get()
	if err != nil {
		return err
	}
	defer orig.Close()
	if err := netns.Set(ns); err != nil {
		return err
	}
	defer netns.Set(orig)
	return fn()
}

type portProxy struct {
	listener net.Listener
	done     chan struct{}
}

func startPortProxy(ctx context.Context, listenIP, advertiseIP, target string, logger *slog.Logger, sandboxCIDRs ...string) (*portProxy, string, error) {
	bindIP := listenIP
	if bindIP == "" || bindIP == "0.0.0.0" || bindIP == "::" {
		bindIP = "0.0.0.0"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bindIP, "0"))
	if err != nil {
		return nil, "", fmt.Errorf("listen for firecracker port proxy: %w", err)
	}

	returnIP, err := portProxyAdvertiseIP(listenIP, advertiseIP, sandboxCIDRs...)
	if err != nil {
		_ = listener.Close()
		return nil, "", err
	}
	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	address := net.JoinHostPort(returnIP, portStr)

	proxy := &portProxy{listener: listener, done: make(chan struct{})}
	go func() {
		defer close(proxy.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go proxyPortConnection(ctx, conn, target, logger, net.DialTimeout)
		}
	}()
	return proxy, address, nil
}

func proxyPortConnection(ctx context.Context, client net.Conn, target string, logger *slog.Logger, dial func(string, string, time.Duration) (net.Conn, error)) {
	defer client.Close()
	targetConn, err := dial("tcp", target, portProxyBackendDialTimeout)
	if err != nil {
		logger.Error("firecracker port proxy backend dial failed", "backend_target", target, "error", err)
		return
	}
	defer targetConn.Close()
	errCh := make(chan error, 2)
	go copyConn(errCh, targetConn, client)
	go copyConn(errCh, client, targetConn)
	select {
	case <-errCh:
	case <-ctx.Done():
	}
}

func copyConn(errCh chan<- error, dst net.Conn, src net.Conn) {
	_, err := io.Copy(dst, src)
	if tcp, ok := dst.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	errCh <- err
}

func (p *portProxy) close() {
	if p == nil {
		return
	}
	_ = p.listener.Close()
	<-p.done
}

var routeLocalIP = func() (string, error) {
	conn, err := net.Dial("udp", "192.0.2.1:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	host, _, err := net.SplitHostPort(conn.LocalAddr().String())
	return host, err
}

func portProxyAdvertiseIP(listenIP, configuredIP string, sandboxCIDRs ...string) (string, error) {
	selected := configuredIP
	if selected == "" {
		selected = listenIP
	}
	if selected == "" || selected == "0.0.0.0" || selected == "::" {
		var err error
		selected, err = routeLocalIP()
		if err != nil {
			return "", fmt.Errorf("resolve firecracker port proxy advertise address; configure proxy_advertise_address: %w", err)
		}
	}
	ip := net.ParseIP(selected)
	if ip == nil {
		return "", fmt.Errorf("invalid firecracker port proxy advertise address %q", selected)
	}
	for _, cidr := range sandboxCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return "", fmt.Errorf("parse firecracker sandbox CIDR %q: %w", cidr, err)
		}
		if network.Contains(ip) {
			return "", fmt.Errorf("firecracker port proxy advertise address %s belongs to sandbox network %s", selected, cidr)
		}
	}
	return selected, nil
}

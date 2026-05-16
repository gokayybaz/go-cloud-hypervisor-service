package dhcp

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
)

// Lease holds the IP allocation for a VM.
type Lease struct {
	ClientMAC net.HardwareAddr
	ClientIP  net.IP
	ServerIP  net.IP
	Gateway   net.IP
	DNS       net.IP
	Mask      net.IPMask
	TTL       time.Duration
}

// Server is a per-VM DHCP server bound to a TAP interface.
type Server struct {
	iface  string
	lease  Lease
	logger logging.Logger
	cancel context.CancelFunc
	done   chan struct{}
}

// NewServer creates a DHCP server for a specific TAP interface.
func NewServer(iface string, lease Lease, logger logging.Logger) *Server {
	return &Server{
		iface:  iface,
		lease:  lease,
		logger: logger,
		done:   make(chan struct{}),
	}
}

// Start begins serving DHCP requests in a background goroutine.
func (s *Server) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	laddr := &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: 67,
	}

	srv, err := server4.NewServer(s.iface, laddr, s.handler)
	if err != nil {
		cancel()
		return fmt.Errorf("create dhcp server on %s: %w", s.iface, err)
	}

	go func() {
		defer close(s.done)
		defer cancel()
		s.logger.Info("dhcp server started", "iface", s.iface, "client_ip", s.lease.ClientIP)
		go func() {
			<-ctx.Done()
			srv.Close()
		}()
		if err := srv.Serve(); err != nil {
			s.logger.Info("dhcp server stopped", "iface", s.iface, "err", err)
		}
	}()

	return nil
}

// Stop shuts down the DHCP server.
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	<-s.done
	s.logger.Info("dhcp server stopped", "iface", s.iface)
}

func (s *Server) handler(conn net.PacketConn, peer net.Addr, m *dhcpv4.DHCPv4) {
	if m == nil {
		return
	}

	var reply *dhcpv4.DHCPv4
	var err error

	switch m.MessageType() {
	case dhcpv4.MessageTypeDiscover:
		reply, err = s.buildOffer(m)
	case dhcpv4.MessageTypeRequest:
		reply, err = s.buildAck(m)
	default:
		return
	}

	if err != nil {
		s.logger.Error("dhcp handler error", "err", err)
		return
	}

	if _, err := conn.WriteTo(reply.ToBytes(), peer); err != nil {
		s.logger.Error("dhcp write error", "err", err)
	}
}

func (s *Server) buildOffer(req *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, error) {
	return dhcpv4.NewReplyFromRequest(req,
		dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer),
		dhcpv4.WithServerIP(s.lease.ServerIP),
		dhcpv4.WithYourIP(s.lease.ClientIP),
		dhcpv4.WithOption(dhcpv4.OptSubnetMask(s.lease.Mask)),
		dhcpv4.WithOption(dhcpv4.OptRouter(s.lease.Gateway)),
		dhcpv4.WithOption(dhcpv4.OptDNS(s.lease.DNS)),
		dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(s.lease.TTL)),
	)
}

func (s *Server) buildAck(req *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, error) {
	return dhcpv4.NewReplyFromRequest(req,
		dhcpv4.WithMessageType(dhcpv4.MessageTypeAck),
		dhcpv4.WithServerIP(s.lease.ServerIP),
		dhcpv4.WithYourIP(s.lease.ClientIP),
		dhcpv4.WithOption(dhcpv4.OptSubnetMask(s.lease.Mask)),
		dhcpv4.WithOption(dhcpv4.OptRouter(s.lease.Gateway)),
		dhcpv4.WithOption(dhcpv4.OptDNS(s.lease.DNS)),
		dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(s.lease.TTL)),
	)
}

package dhcp // import "github.com/cafebazaar/blacksmith/dhcp"

import (
	"bytes"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/cafebazaar/blacksmith/datasource"
	"github.com/cafebazaar/blacksmith/logging"
	"github.com/krolaw/dhcp4"
)

const (
	minLeaseHours = 24
	maxLeaseHours = 48

	debugTag = "DHCP"
)

func randLeaseDuration() time.Duration {
	n := (minLeaseHours + rand.Intn(maxLeaseHours-minLeaseHours))
	return time.Duration(n) * time.Hour
}

type DHCPSetting struct {
	IFName     string
	ServerIP   net.IP
	RouterAddr net.IP
	SubnetMask net.IP
}

func ServeDHCP(settings *DHCPSetting, datasource datasource.DataSource) error {
	handler, err := newDHCPHandler(settings, datasource)
	if err != nil {
		logging.Debug("DHCP", "Error in connecting etcd - %s", err.Error())
		return err
	}
	logging.Log("DHCP", "Listening on %s:67 (interface: %s)",
		settings.ServerIP.String(), settings.IFName)
	if settings.IFName != "" {
		err = dhcp4.ListenAndServeIf(settings.IFName, handler)
	} else {
		err = dhcp4.ListenAndServe(handler)
	}
	if err != nil {
		logging.Debug("DHCP", "Error in server - %s", err.Error())
	}

	rand.Seed(time.Now().UTC().UnixNano())

	return err
}

// DHCP handler that passed to dhcp4 package

type DHCPHandler struct {
	settings    *DHCPSetting
	datasource  datasource.DataSource
	dhcpOptions dhcp4.Options
	bootMessage string
}

func newDHCPHandler(settings *DHCPSetting, datasource datasource.DataSource) (*DHCPHandler, error) {
	h := &DHCPHandler{
		settings:    settings,
		datasource:  datasource,
		bootMessage: fmt.Sprintf("Blacksmith (%s)", datasource.Version().Version),
	}
	return h, nil
}

func (h *DHCPHandler) fillPXE() []byte {
	// PXE vendor options
	var pxe bytes.Buffer
	var l byte
	// Discovery Control - disable broadcast and multicast boot server discovery
	pxe.Write([]byte{6, 1, 3})
	// PXE boot server
	pxe.Write([]byte{8, 7, 0x80, 0x00, 1})
	pxe.Write(h.settings.ServerIP.To4())
	// PXE boot menu - one entry, pointing to the above PXE boot server
	l = byte(3 + len(h.bootMessage))
	pxe.Write([]byte{9, l, 0x80, 0x00, 9})
	pxe.WriteString(h.bootMessage)
	// PXE menu prompt+timeout
	l = byte(1 + len(h.bootMessage))
	pxe.Write([]byte{10, l, 0x2})
	pxe.WriteString(h.bootMessage)
	// End vendor options
	pxe.WriteByte(255)
	return pxe.Bytes()
}

//
func (h *DHCPHandler) ServeDHCP(p dhcp4.Packet, msgType dhcp4.MessageType, options dhcp4.Options) (d dhcp4.Packet) {
	dns, err := h.datasource.DNSAddresses()
	if err != nil {
		logging.Log(debugTag, "Failed to read dns addresses")
		return nil
	}

	dhcpOptions := dhcp4.Options{
		dhcp4.OptionSubnetMask:       h.settings.SubnetMask.To4(),
		dhcp4.OptionDomainNameServer: dns,
	}

	if h.settings.RouterAddr != nil {
		dhcpOptions[dhcp4.OptionRouter] = h.settings.RouterAddr.To4()
	}

	macAddress := strings.Join(strings.Split(p.CHAddr().String(), ":"), "")
	switch msgType {
	case dhcp4.Discover:
		ip, err := h.datasource.Assign(p.CHAddr().String())
		if err != nil {
			logging.Debug("DHCP", "err in lease pool - %s", err.Error())
			return nil // pool is full
		}
		replyOptions := dhcpOptions.SelectOrderOrAll(options[dhcp4.OptionParameterRequestList])
		packet := dhcp4.ReplyPacket(p, dhcp4.Offer, h.settings.ServerIP, ip, randLeaseDuration(), replyOptions)
		// this is a pxe request
		guidVal, isPxe := options[97]
		if isPxe {
			logging.Log("DHCP", "dhcp discover with PXE - CHADDR %s - IP %s - our ip %s", p.CHAddr().String(), ip.String(), h.settings.ServerIP.String())
			guid := guidVal[1:]
			packet.AddOption(60, []byte("PXEClient"))
			packet.AddOption(97, guid)
			packet.AddOption(43, h.fillPXE())
		} else {
			logging.Log("DHCP", "dhcp discover - CHADDR %s - IP %s", p.CHAddr().String(), ip.String())
		}
		return packet
	case dhcp4.Request:
		if server, ok := options[dhcp4.OptionServerIdentifier]; ok && !net.IP(server).Equal(h.settings.ServerIP) {
			return nil // this message is not ours
		}
		requestedIP := net.IP(options[dhcp4.OptionRequestedIPAddress])
		if requestedIP == nil {
			requestedIP = net.IP(p.CIAddr())
		}
		if len(requestedIP) != 4 || requestedIP.Equal(net.IPv4zero) {
			logging.Debug("DHCP", "dhcp request - CHADDR %s - bad request", p.CHAddr().String())
			return nil
		}
		_, err := h.datasource.Request(p.CHAddr().String(), requestedIP)
		if err != nil {
			logging.Debug("DHCP", "dhcp request - CHADDR %s - Requested IP %s - NO MATCH", p.CHAddr().String(), requestedIP.String())

			return dhcp4.ReplyPacket(p, dhcp4.NAK, h.settings.ServerIP, nil, 0, nil)
		}

		replyOptions := dhcpOptions.SelectOrderOrAll(options[dhcp4.OptionParameterRequestList])
		packet := dhcp4.ReplyPacket(p, dhcp4.ACK, h.settings.ServerIP, requestedIP, randLeaseDuration(), replyOptions)
		// this is a pxe request
		guidVal, isPxe := options[97]
		if isPxe {
			logging.Log("DHCP", "dhcp request with PXE - CHADDR %s - Requested IP %s - our ip %s - ACCEPTED", p.CHAddr().String(), requestedIP.String(), h.settings.ServerIP.String())
			guid := guidVal[1:]
			packet.AddOption(60, []byte("PXEClient"))
			packet.AddOption(97, guid)
			packet.AddOption(43, h.fillPXE())
		} else {
			logging.Log("DHCP", "dhcp request - CHADDR %s - Requested IP %s - ACCEPTED", p.CHAddr().String(), requestedIP.String())
		}
		packet.AddOption(12, []byte("node"+macAddress+"."+h.datasource.ClusterName())) // host name option
		return packet
	case dhcp4.Release, dhcp4.Decline:

		return nil
	}
	return nil
}

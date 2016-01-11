package main // import "github.com/cafebazaar/blacksmith"

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"github.com/cafebazaar/blacksmith/cloudconfig"
	"github.com/cafebazaar/blacksmith/datasource"
	"github.com/cafebazaar/blacksmith/dhcp"
	"github.com/cafebazaar/blacksmith/logging"
	"github.com/cafebazaar/blacksmith/pxe"
	"github.com/cafebazaar/blacksmith/web"
	etcd "github.com/coreos/etcd/client"
)

//go:generate go-bindata -o pxe/pxelinux_autogen.go -prefix=pxe -pkg pxe -ignore=README.md pxe/pxelinux
//go:generate go-bindata -o web/ui_autogen.go -pkg web web/ui/...

var _ cloudconfig.DataSource = (*datasource.RuntimeConfiguration)(nil)
var _ cloudconfig.DataSource = (*datasource.Flags)(nil)

const (
	workspacePathHelp = `Path to workspace which obey following structure
		/images/{core-os-version}/coreos_production_pxe_image.cpio.gz
		/images/{core-os-version}/coreos_production_pxe.vmlinuz
		/config/cloudconfig/main.yaml
		/config/ignition/main.yaml
		/initial.yaml
`
)

var (
	debugFlag         = flag.Bool("debug", false, "Log more things that aren't directly related to booting a recognized client")
	listenIFFlag      = flag.String("if", "0.0.0.0", "Interface name for DHCP and PXE to listen on")
	workspacePathFlag = flag.String("workspace", "/workspace", workspacePathHelp)
	etcdFlag          = flag.String("etcd", "", "Etcd endpoints")
	etcdDirFlag       = flag.String("etcd-dir", "blacksmith", "The etcd directory prefix")

	leaseStartFlag  = flag.String("lease-start", "", "Begining of lease starting IP")
	leaseRangeFlag  = flag.Int("lease-range", 0, "Lease range")
	leaseSubnetFlag = flag.String("lease-subnet", "", "Subnet of specified lease")
	leaseRouterFlag = flag.String("router", "", "Default router that assigned to DHCP clients")
	leaseDNSFlag    = flag.String("dns", "", "Default DNS that assigned to DHCP clients")

	version   = "v0.2"
	commit    string
	buildTime string
)

func init() {
	// If commit, branch, or build time are not set, make that clear.
	if commit == "" {
		commit = "unknown"
	}
	if buildTime == "" {
		buildTime = "unknown"
	}
}

func interfaceIP(iface *net.Interface) (net.IP, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	fs := [](func(net.IP) bool){
		net.IP.IsGlobalUnicast,
		net.IP.IsLinkLocalUnicast,
		net.IP.IsLoopback,
	}
	for _, f := range fs {
		for _, a := range addrs {
			ipaddr, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipaddr.IP.To4()
			if ip == nil {
				continue
			}
			if f(ip) {
				return ip, nil
			}
		}
	}
	return nil, fmt.Errorf("interface %s has no usable unicast addresses", iface.Name)
}

func main() {
	var err error
	flag.Parse()
	// etcd config
	if etcdFlag == nil || etcdDirFlag == nil {
		fmt.Fprint(os.Stderr, "please specify the etcd endpoints\n")
		os.Exit(1)
	}

	// listen ip address for http, tftp
	var listenIP = net.IP{0, 0, 0, 0}
	// finding interface by interface name
	var dhcpIF *net.Interface
	if *listenIFFlag != "" {
		dhcpIF, err = net.InterfaceByName(*listenIFFlag)
	} else {
		fmt.Fprint(os.Stderr, "please specify an interface\n")
		os.Exit(1)
	}
	if err != nil {
		log.Fatalln(err)
	}

	dhcpIP, err := interfaceIP(dhcpIF)
	if err != nil {
		log.Fatalln(err)
	}

	// used for replying in dhcp and pxe
	var serverIP = net.IPv4zero
	if serverIP.Equal(net.IPv4zero) {
		serverIP = dhcpIP
	}

	var httpAddr = net.TCPAddr{IP: listenIP, Port: 70}
	var tftpAddr = net.UDPAddr{IP: listenIP, Port: 69}
	var webAddr = net.TCPAddr{IP: listenIP, Port: 8000}
	var cloudConfigHTTPAddr = net.TCPAddr{IP: listenIP, Port: 8001}
	var pxeAddr = net.UDPAddr{IP: dhcpIP, Port: 4011}

	// dhcp setting
	leaseStart := net.ParseIP(*leaseStartFlag)
	leaseRange := *leaseRangeFlag
	leaseSubnet := net.ParseIP(*leaseSubnetFlag)
	leaseRouter := net.ParseIP(*leaseRouterFlag)
	leaseDNS := net.ParseIP(*leaseDNSFlag)
	leaseDuration := 1 * time.Hour

	if leaseStart == nil {
		fmt.Fprint(os.Stderr, "please specify the lease start ip\n")
		os.Exit(1)
	}
	if leaseRange <= 1 {
		fmt.Fprint(os.Stderr, "lease range should be greater that 1\n")
		os.Exit(1)
	}
	if leaseSubnet == nil {
		fmt.Fprint(os.Stderr, "please specify the lease subnet\n")
		os.Exit(1)
	}
	if leaseRouter == nil {
		fmt.Fprint(os.Stderr, "please specify the IP address of network router\n")
		os.Exit(1)
	}
	if leaseDNS == nil {
		fmt.Fprint(os.Stderr, "please specify an DNS server\n")
		os.Exit(1)
	}

	fmt.Printf("Blacksmith (%s)\n", version)
	fmt.Printf("  Commit:        %s\n", commit)
	fmt.Printf("  Build Time:    %s\n", buildTime)

	fmt.Printf("Server IP:       %s\n", serverIP.String())
	fmt.Printf("Interface IP:    %s\n", dhcpIP.String())
	fmt.Printf("Interface Name:  %s\n", dhcpIF.Name)

	// datasources
	etcdClient, err := etcd.New(etcd.Config{
		Endpoints:               strings.Split(*etcdFlag, ","),
		HeaderTimeoutPerRequest: time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't create etcd connection: %s\n", err)
		os.Exit(1)
	}
	kapi := etcd.NewKeysAPI(etcdClient)

	runtimeConfig, err := datasource.NewRuntimeConfiguration(kapi, etcdClient, *etcdDirFlag, *workspacePathFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't create runtime configuration: %s\n", err)
		os.Exit(1)
	}

	flagsDataSource, err := datasource.NewFlags(kapi, path.Join(*etcdDirFlag, "flags"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't create runtime configuration: %s\n", err)
		os.Exit(1)
	}

	datasources := map[string]cloudconfig.DataSource{
		"default": runtimeConfig,
		"flags":   flagsDataSource,
	}

	// serving cloudconfig
	go func() {
		log.Fatalln(cloudconfig.ServeCloudConfig(cloudConfigHTTPAddr, *workspacePathFlag, datasources))
	}()

	// serving http booter
	go func() {
		repo, err := cloudconfig.FromPath(datasources, path.Join(*workspacePathFlag, "config/bootparams"))
		logging.Log(":D", "bootparams")
		if err != nil {
			log.Fatalln(err)
		}
		log.Fatalln(pxe.ServeHTTPBooter(httpAddr, runtimeConfig, repo))
	}()
	// serving tftp
	go func() {
		log.Fatalln(pxe.ServeTFTP(tftpAddr))
	}()
	// pxe protocol
	go func() {
		log.Fatalln(pxe.ServePXE(pxeAddr, serverIP, net.TCPAddr{IP: serverIP, Port: httpAddr.Port}))
	}()
	// serving dhcp
	leasePool, err := dhcp.NewLeasePool(kapi, *etcdDirFlag, leaseStart, leaseRange, leaseDuration)
	if err != nil {
		log.Fatalln(err)
	}
	// serving web
	go func() {
		restServer := web.NewRest(leasePool, runtimeConfig)
		log.Fatalln(web.ServeWeb(restServer, webAddr))
	}()

	go func() {
		log.Fatalln(dhcp.ServeDHCP(&dhcp.DHCPSetting{
			IFName:        dhcpIF.Name,
			LeaseDuration: leaseDuration,
			ServerIP:      serverIP,
			RouterAddr:    leaseRouter,
			SubnetMask:    leaseSubnet,
			DNSAddr:       leaseDNS,
		}, leasePool))
	}()

	logging.RecordLogs(log.New(os.Stderr, "", log.LstdFlags), *debugFlag)
}

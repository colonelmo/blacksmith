package main

import (
	"io"
	"net"
	"os"
)

// A BootSpec identifies a kernel, kernel commandline, and set of initrds to boot on a machine.
//
// Kernel and Initrds are opaque reference strings provided by a
// Booter. When we need to get the associated bytes, we pass the
// opaque reference back into Booter.File(). The bytes have no other
// significance beyond that. They also do not need to be
// human-readable.
type BootSpec struct {
	Kernel  string
	Initrd  string
	Cmdline string
}

type spec struct {
	BootSpec
	cmdMap map[string]interface{}
}

// CoreOSBooter boots all machines with local files.
func CoreOSBooter(imagePath string) *coreOSBooter {
	return &coreOSBooter{imagePath, nil}
}

type coreOSBooter struct {
	imagePath  string
	dataSource *dataSource
}

// The given MAC address is now running a bootloader, and it wants
// to know what it should boot. Returning an error here will cause
// the PXE boot process to abort (i.e. the machine will reboot and
// start again at ShouldBoot).
func (b *coreOSBooter) BootSpec(unused net.HardwareAddr, prefix string) (*BootSpec, error) {
	// TODO:
	// coreOSVersion, err := b.dataSource.GetCoreOSVersion()
	// if err != nil {
	// 	return nil, err
	// }
	coreOSVersion := "835.1"
	ret := &BootSpec{
		Kernel:  prefix + coreOSVersion + "/coreos_production_pxe.vmlinuz",
		Cmdline: "cloud-config-url=http://amghezi.cafebazaar.ir/cloud-config.yml coreos.config.url=http://example.com/ignition-config.yml",
		Initrd:  prefix + coreOSVersion + "/coreos_production_pxe_image.cpio.gz",
	}
	return ret, nil
}

// Get the contents of a blob mentioned in a previously issued
// BootSpec. Additionally returns a pretty name for the blob for
// logging purposes.
func (b coreOSBooter) Read(id string) (io.ReadCloser, string, error) {
	f, err := os.Open(b.imagePath + "/" + id)
	return f, id, err
}

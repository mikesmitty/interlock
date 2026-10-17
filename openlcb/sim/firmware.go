package sim

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
)

// The simulated outpost takes firmware upgrades the way canbus-outpost
// does: Freeze is refused while a new image is still on trial, writes must
// arrive in order from address 0 and the first bytes must look like an
// image for this board, Unfreeze checks the whole image, and an accepted
// image starts after a short delay. The new image runs on trial and
// confirms itself once it has been up for a while; one that does not
// confirm itself goes back to the previous version.
//
// A simulated image is a short text file, made with FirmwareImage, rather
// than an RP2350 binary.

// FirmwareMagic starts every simulated image.
const FirmwareMagic = "interlock-sim-firmware\n"

// firmwareEnd closes a complete image; without it the image is incomplete.
const firmwareEnd = "end\n"

// FirmwareMaxSize is the size of the simulated flash partition.
const FirmwareMaxSize = 256 * 1024

// FirmwareImage builds an image the simulated outpost accepts, after which
// it reports version. With fails set the new image never confirms itself,
// so the outpost goes back to the version it had, as a real board does
// with firmware that does not come up. The image is padded past 4 KB so
// the transfer takes many writes.
func FirmwareImage(version string, fails bool) []byte {
	var b strings.Builder
	b.WriteString(FirmwareMagic)
	b.WriteString("version: " + version + "\n")
	if fails {
		b.WriteString("trial: fail\n")
	}
	b.WriteString("padding:")
	for b.Len() < 4200 {
		b.WriteString(" simulated")
	}
	b.WriteString("\n" + firmwareEnd)
	return []byte(b.String())
}

// FirmwareTiming is how fast the simulated upgrade moves; tests shorten
// it. The zero value takes the firmware's real timing.
type FirmwareTiming struct {
	// Restart is the delay from an accepted Unfreeze to the restart.
	Restart time.Duration
	// Confirm is how long the new image runs before confirming itself, or,
	// for an image that fails its trial, before the board goes back.
	Confirm time.Duration
}

func (t FirmwareTiming) withDefaults() FirmwareTiming {
	if t.Restart == 0 {
		t.Restart = 100 * time.Millisecond
	}
	if t.Confirm == 0 {
		t.Confirm = 5 * time.Second
	}
	return t
}

// firmwareState is the upgrade in progress and the trial of a new image.
type firmwareState struct {
	timing   FirmwareTiming
	image    []byte
	lastAddr uint32
	lastLen  int
	onTrial  bool
	previous openlcb.SNIP
	startNew bool
	fails    bool
	version  string
}

// Freeze implements memconfig.Firmware.
func (o *Outpost) Freeze() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fw.onTrial {
		return &openlcb.Error{Code: openlcb.ErrCodeTemporary, Op: "freeze: the running image must confirm itself first"}
	}
	o.fw.image = o.fw.image[:0]
	o.fw.lastLen = 0
	o.fw.startNew = false
	o.SetLocalProtocols(o.LocalProtocols() | openlcb.ProtocolFirmwareUpgradeActive)
	o.log.Info("sim: firmware upgrade started")
	return nil
}

// WriteFirmware implements memconfig.Firmware.
func (o *Outpost) WriteFirmware(address uint32, data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	fw := &o.fw
	if fw.lastLen != 0 && address == fw.lastAddr && len(data) == fw.lastLen {
		return nil // a resend of the last write, whose acknowledgment was lost
	}
	if address == 0 {
		fw.image = fw.image[:0]
		head := min(len(data), len(FirmwareMagic))
		if len(data) < 8 || !bytes.HasPrefix([]byte(FirmwareMagic), data[:head]) {
			return &openlcb.Error{Code: openlcb.ErrCodeFirmwareIncompatible, Op: "write firmware"}
		}
	}
	if address != uint32(len(fw.image)) {
		return &openlcb.Error{Code: openlcb.ErrCodeInvalidArguments, Op: "write firmware out of order"}
	}
	if len(fw.image)+len(data) > FirmwareMaxSize {
		return &openlcb.Error{Code: openlcb.ErrCodeAddressOutOfBounds, Op: "write firmware"}
	}
	fw.image = append(fw.image, data...)
	fw.lastAddr, fw.lastLen = address, len(data)
	return nil
}

// Unfreeze implements memconfig.Firmware.
func (o *Outpost) Unfreeze() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.SetLocalProtocols(o.LocalProtocols() &^ openlcb.ProtocolFirmwareUpgradeActive)
	fw := &o.fw
	text := string(fw.image)
	if !strings.HasPrefix(text, FirmwareMagic) {
		return &openlcb.Error{Code: openlcb.ErrCodeFirmwareIncompatible, Op: "unfreeze"}
	}
	if !strings.HasSuffix(text, firmwareEnd) {
		o.log.Info("sim: firmware image incomplete", "bytes", len(fw.image))
		return &openlcb.Error{Code: openlcb.ErrCodeFirmwareChecksum, Op: "unfreeze"}
	}
	version, fails := parseFirmware(text)
	if version == "" {
		return &openlcb.Error{Code: openlcb.ErrCodeFirmwareCorrupted, Op: "unfreeze"}
	}
	fw.version, fw.fails, fw.startNew = version, fails, true
	o.log.Info("sim: firmware image accepted", "version", version, "bytes", len(fw.image))
	time.AfterFunc(fw.timing.withDefaults().Restart, o.startNewFirmware)
	return nil
}

// parseFirmware reads the version and trial outcome out of an image.
func parseFirmware(text string) (version string, fails bool) {
	for line := range strings.SplitSeq(text, "\n") {
		if v, ok := strings.CutPrefix(line, "version: "); ok {
			version = strings.TrimSpace(v)
		}
		if line == "trial: fail" {
			fails = true
		}
	}
	return version, fails
}

// startNewFirmware restarts the node as the new image, on trial.
func (o *Outpost) startNewFirmware() {
	o.mu.Lock()
	fw := &o.fw
	if !fw.startNew {
		o.mu.Unlock()
		return
	}
	fw.startNew = false
	fw.previous = o.SNIPInfo()
	snip := fw.previous
	snip.SoftwareVersion = fw.version
	o.SetSNIP(snip)
	fw.onTrial = true
	confirm := fw.timing.withDefaults().Confirm
	o.mu.Unlock()
	o.log.Info("sim: starting the new firmware on trial", "version", snip.SoftwareVersion)
	o.restart()
	time.AfterFunc(confirm, o.endTrial)
}

// endTrial confirms the new image, or goes back to the previous one when
// the image was made to fail.
func (o *Outpost) endTrial() {
	o.mu.Lock()
	fw := &o.fw
	if !fw.onTrial {
		o.mu.Unlock()
		return
	}
	fw.onTrial = false
	fails := fw.fails
	if fails {
		o.SetSNIP(fw.previous)
	}
	o.mu.Unlock()
	if fails {
		o.log.Info("sim: new firmware did not confirm itself; going back", "version", o.SNIPInfo().SoftwareVersion)
		o.restart()
		return
	}
	o.log.Info("sim: new firmware confirmed")
}

// OnTrial reports whether a new image is running unconfirmed.
func (o *Outpost) OnTrial() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fw.onTrial
}

// SetFirmwareTiming shortens the simulated upgrade; call it before Run.
func (o *Outpost) SetFirmwareTiming(t FirmwareTiming) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fw.timing = t
}

// restart reboots the simulated node: it rejoins the bus and announces
// itself again, as the board does after a reset.
func (o *Outpost) restart() {
	o.mu.Lock()
	ctx := o.ctx
	o.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := o.Restart(ctx); err != nil {
		o.log.Debug("sim: restart failed", "error", err)
	}
}

// announce sends Initialization Complete without restarting, which the
// standard asks for after a Freeze.
func (o *Outpost) announce() {
	o.send(openlcb.Message{MTI: openlcb.MTIInitializationComplete, Payload: o.ID().AsSlice()})
}

// String describes the firmware state for logs.
func (fw *firmwareState) String() string {
	return fmt.Sprintf("firmware{bytes=%d trial=%v}", len(fw.image), fw.onTrial)
}

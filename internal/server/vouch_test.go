package server

import (
	"os"
	"path/filepath"
	"testing"
)

// A device admitted by another device must record WHO admitted it, so that
// revoking a lost phone can also reach everything that phone let in.
// Without this, a laptop enrolled by a stolen phone is indistinguishable
// from one enrolled at the machine.

func withTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".crv"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestVouchedDeviceRecordsItsApprover(t *testing.T) {
	withTempHome(t)

	if err := registerDeviceVouched("phone1", "PUBKEY-PHONE", "", "phone"); err != nil {
		t.Fatal(err)
	}
	if err := registerDeviceVouched("laptop1", "PUBKEY-LAPTOP", "phone1", "laptop"); err != nil {
		t.Fatal(err)
	}

	d := findDevice("laptop1")
	if d == nil {
		t.Fatal("laptop not registered")
	}
	if d.ApprovedBy != "phone1" {
		t.Errorf("ApprovedBy = %q, want %q", d.ApprovedBy, "phone1")
	}
	if d.Label != "laptop" {
		t.Errorf("Label = %q, want %q", d.Label, "laptop")
	}
	if self := findDevice("phone1"); self == nil || self.ApprovedBy != "" {
		t.Errorf("a self-paired device must have no approver, got %+v", self)
	}
}

func TestRevokeCascadeReachesVouchedDevices(t *testing.T) {
	withTempHome(t)

	_ = registerDeviceVouched("phone1", "K1", "", "phone")
	_ = registerDeviceVouched("laptop1", "K2", "phone1", "laptop")
	_ = registerDeviceVouched("desk1", "K3", "", "desktop")

	removed, err := revokeDevice("phone1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("cascade removed %d devices (%v), want 2", len(removed), removed)
	}
	if findDevice("phone1") != nil || findDevice("laptop1") != nil {
		t.Error("phone and the device it vouched for must both be gone")
	}
	if findDevice("desk1") == nil {
		t.Error("cascade must not touch independently-paired devices")
	}
}

func TestRevokeWithoutCascadeKeepsVouchedDevices(t *testing.T) {
	withTempHome(t)

	_ = registerDeviceVouched("phone1", "K1", "", "phone")
	_ = registerDeviceVouched("laptop1", "K2", "phone1", "laptop")

	removed, err := revokeDevice("phone1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed %v, want just the phone", removed)
	}
	// the laptop survives, but its approver is gone — it must be marked so
	// the trail never points at a device that no longer exists
	d := findDevice("laptop1")
	if d == nil {
		t.Fatal("laptop must survive a non-cascading revoke")
	}
	if d.ApprovedBy != "phone1" || !d.ApproverGone {
		t.Errorf("orphaned device should keep its approver id and be flagged: %+v", d)
	}
}

// The approver field is only meaningful if the server PROVES the voucher
// signed this enrollment. An unverified claim is worse than none: it reads
// as an audit trail while being attacker-controlled.
func TestVoucherClaimMustBeSigned(t *testing.T) {
	withTempHome(t)
	_ = registerDeviceVouched("phone1", "K1", "", "phone")

	// a caller claiming phone1 vouched, with no proof
	if ok := voucherVerified("phone1", "some-nonce", "not-a-real-signature"); ok {
		t.Error("an unsigned voucher claim must be rejected")
	}
	// a claim naming a device that does not exist
	if ok := voucherVerified("ghost", "some-nonce", "sig"); ok {
		t.Error("a claim from an unknown device must be rejected")
	}
}

// End-to-end: a device enrolled with a valid voucher proof is recorded as
// approved by that voucher; the same enrollment without proof is not.
func TestPairRecordsVerifiedVoucherOnly(t *testing.T) {
	withTempHome(t)
	_ = registerDeviceVouched("phone1", "K1", "", "phone")

	// unverifiable claim → stored WITHOUT an approver, but still enrolled
	approver := ""
	if voucherVerified("phone1", "nonce", "bogus-sig") {
		approver = "phone1"
	}
	if err := registerDeviceVouched("laptop1", "K2", approver, "laptop"); err != nil {
		t.Fatal(err)
	}
	d := findDevice("laptop1")
	if d == nil {
		t.Fatal("device must still enroll — the invitation authorized it")
	}
	if d.ApprovedBy != "" {
		t.Errorf("unproven voucher claim was recorded as %q", d.ApprovedBy)
	}
}

// Re-pairing at the machine must clear a stale voucher, or a device would
// keep claiming it was admitted by a phone that is long gone.
func TestRepairAtMachineClearsStaleApprover(t *testing.T) {
	withTempHome(t)
	_ = registerDeviceVouched("phone1", "K1", "", "phone")
	_ = registerDeviceVouched("laptop1", "K2", "phone1", "laptop")

	if err := registerDeviceVouched("laptop1", "K2new", "", "laptop"); err != nil {
		t.Fatal(err)
	}
	d := findDevice("laptop1")
	if d.ApprovedBy != "" {
		t.Errorf("ApprovedBy = %q after re-pairing at the machine, want empty", d.ApprovedBy)
	}
	if d.PubKey != "K2new" {
		t.Errorf("re-pair must replace the key, got %q", d.PubKey)
	}
}

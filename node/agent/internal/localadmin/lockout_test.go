package localadmin

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lockoutSalt is the shape save() writes: raw-base64 of 24 bytes.
func lockoutSalt() string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("s"), 24))
}

// lockoutCredentialKey is the binding persistLockout records, computed the
// same way from the same inputs.
func lockoutCredentialKey(password string) string {
	salt := lockoutSalt()
	hashed, err := pbkdf2.Key(sha256.New, password, []byte(salt), rounds, 32)
	if err != nil {
		panic(err)
	}
	_ = hashed
	return sessionKey(salt + base64.RawURLEncoding.EncodeToString(hashed))
}

// withPassword writes credentials the way save() does, so Open finds an
// administrator account instead of a first-time setup code.
func withPassword(t *testing.T, dir, password, deviceID string, now func() time.Time) *Admin {
	t.Helper()
	salt := lockoutSalt()
	if err := writePrivate(dir, "admin.json", mustJSON(t, credentials{Salt: salt, Hash: hash(password, salt)})); err != nil {
		t.Fatal(err)
	}
	admin, err := Open(Options{Dir: dir, DeviceID: deviceID, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return admin
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A restart must not wipe the failure count or lift a running lock: being
// able to reboot the process is exactly the way around the lockout that
// persistence exists to close.
func TestLockoutSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	c := &clock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	admin := withPassword(t, dir, "correct horse battery", "device-a", c.Now)

	for i := 0; i < lockoutThreshold; i++ {
		if lock := admin.refuse(); i == lockoutThreshold-1 && lock == 0 {
			t.Fatal("the threshold failure did not lock")
		}
	}
	if admin.locked() == 0 {
		t.Fatal("not locked before restart")
	}
	if _, err := os.Stat(filepath.Join(dir, lockoutFile)); err != nil {
		t.Fatalf("lockout state was not persisted: %v", err)
	}

	restarted, err := Open(Options{Dir: dir, DeviceID: "device-a", Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	if restarted.failures != lockoutThreshold {
		t.Fatalf("failures after restart = %d", restarted.failures)
	}
	if remaining := restarted.locked(); remaining == 0 || remaining > lockoutMax {
		t.Fatalf("lock after restart = %s", remaining)
	}

	// A correct credential forgives and removes the file.
	restarted.forgive()
	if _, err := os.Stat(filepath.Join(dir, lockoutFile)); !os.IsNotExist(err) {
		t.Fatal("forgive did not remove the lockout state")
	}
}

// A count below the threshold also survives, so patience across reboots
// gains a guesser nothing either.
func TestPartialCountSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	c := &clock{now: time.Now()}
	admin := withPassword(t, dir, "correct horse battery", "device-a", c.Now)
	admin.refuse()
	admin.refuse()
	restarted, err := Open(Options{Dir: dir, DeviceID: "device-a", Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	if restarted.failures != 2 {
		t.Fatalf("failures after restart = %d", restarted.failures)
	}
}

// A device without a battery-backed clock can boot years behind the moment
// the lock was recorded; such a timestamp must clamp to the maximum, not
// keep the console locked until the clock catches up.
func TestRestoredLockIsClampedToTheMaximum(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	stored := rememberedLockout{
		DeviceID:    "device-a",
		Credential:  lockoutCredentialKey("pw"),
		Failures:    9,
		LastFailure: now,
		LockedUntil: now.Add(100 * 24 * time.Hour),
	}
	if err := writePrivate(dir, lockoutFile, mustJSON(t, stored)); err != nil {
		t.Fatal(err)
	}
	// The credential binding must match what Open computes; set the same
	// credentials the stored record was keyed against.
	if err := writePrivate(dir, "admin.json", mustJSON(t, credentials{Salt: lockoutSalt(), Hash: hash("pw", lockoutSalt())})); err != nil {
		t.Fatal(err)
	}
	admin, err := Open(Options{Dir: dir, DeviceID: "device-a", Now: func() time.Time {
		return now.Add(24 * time.Hour) // a day behind the recorded lock's end
	}})
	if err != nil {
		t.Fatal(err)
	}
	if remaining := admin.locked(); remaining <= 0 || remaining > lockoutMax {
		t.Fatalf("clamped lock = %s, want at most %s", remaining, lockoutMax)
	}
}

// A lockout recorded against another device or another password opens
// nothing here: the count is ignored, not applied.
func TestForeignLockoutIsIgnored(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stored := rememberedLockout{
		DeviceID:    "another-device",
		Credential:  lockoutCredentialKey("pw"),
		Failures:    lockoutThreshold,
		LastFailure: now,
		LockedUntil: now.Add(lockoutMax),
	}
	if err := writePrivate(dir, lockoutFile, mustJSON(t, stored)); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(dir, "admin.json", mustJSON(t, credentials{Salt: lockoutSalt(), Hash: hash("pw", lockoutSalt())})); err != nil {
		t.Fatal(err)
	}
	admin, err := Open(Options{Dir: dir, DeviceID: "device-a", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if admin.failures != 0 || admin.locked() != 0 {
		t.Fatalf("a foreign lockout was applied: failures=%d locked=%s", admin.failures, admin.locked())
	}
}

// A file that fails to parse is refused loudly, matching how remembered
// sessions are treated: neither trusted nor quietly dropped.
func TestCorruptLockoutFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	if err := writePrivate(dir, "admin.json", mustJSON(t, credentials{Salt: lockoutSalt(), Hash: hash("pw", lockoutSalt())})); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(dir, lockoutFile, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{Dir: dir, DeviceID: "device-a"}); err == nil || !strings.Contains(err.Error(), "lockout") {
		t.Fatalf("corrupt lockout state returned %v", err)
	}
}

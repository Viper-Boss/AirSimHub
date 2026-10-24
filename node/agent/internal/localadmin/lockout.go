package localadmin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// rememberedLockout is the sign-in lockout state that survives a restart.
//
// Without it, restarting the process - which device administration can do
// over SSH today - wiped the consecutive-failure count and an in-progress
// lock with it, making a restart the cheapest way to keep guessing. The
// count is bound to the device and to the password it was counted against:
// a new password starts from a clean slate, and a file from another device
// or another password is ignored rather than applied.
type rememberedLockout struct {
	DeviceID    string    `json:"device_id"`
	Credential  string    `json:"credential"`
	Failures    int       `json:"failures"`
	LastFailure time.Time `json:"last_failure"`
	LockedUntil time.Time `json:"locked_until"`
}

// lockoutFile lives beside admin.json, written the same way.
const lockoutFile = "admin-lockout.json"

// persistLockout writes the current count. Called with the mutex held; an
// unwritable state directory is logged, not fatal: the in-memory lock still
// holds for this process, and the next refusal tries again.
func (a *Admin) persistLockout() {
	data, err := json.Marshal(rememberedLockout{
		DeviceID:    a.options.DeviceID,
		Credential:  sessionKey(a.cred.Salt + a.cred.Hash),
		Failures:    a.failures,
		LastFailure: a.lastFailure,
		LockedUntil: a.lockedUntil,
	})
	if err != nil {
		return
	}
	if err := writePrivate(a.options.Dir, lockoutFile, data); err != nil {
		a.logf("sign-in lockout state could not be saved: %v", err)
	}
}

// restoreLockout loads the count after a restart. A missing file is the
// normal clean case; one that belongs to another device or another password
// is ignored; one that fails to parse is refused loudly rather than trusted
// or silently dropped.
func (a *Admin) restoreLockout() error {
	data, err := os.ReadFile(filepath.Join(a.options.Dir, lockoutFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored rememberedLockout
	if json.Unmarshal(data, &stored) != nil || stored.Failures < 0 || stored.Failures > 1_000_000 {
		return errors.New("invalid local administrator lockout state")
	}
	if stored.DeviceID != a.options.DeviceID || stored.Credential != sessionKey(a.cred.Salt+a.cred.Hash) {
		return nil
	}
	if stored.Failures == 0 {
		return nil
	}
	now := a.options.Now()
	a.failures = stored.Failures
	a.lastFailure = stored.LastFailure
	// A device without a battery-backed clock can boot with a wall clock far
	// behind the one that recorded the lock. Honoring such a timestamp
	// literally would keep the console locked for years, so a lock that
	// claims to outlast the maximum is clamped to the maximum from now.
	if until := stored.LockedUntil; until.Sub(now) > lockoutMax {
		a.lockedUntil = now.Add(lockoutMax)
	} else if until.Sub(now) > 0 {
		a.lockedUntil = until
	}
	return nil
}

// refuse records one wrong credential, persists the count, and returns the
// lock it caused, if any.
func (a *Admin) refuse() time.Duration {
	lock := a.refused()
	a.persistLockout()
	return lock
}

// forgive clears the count after a correct credential and removes its file.
func (a *Admin) forgive() {
	a.failures = 0
	if err := os.Remove(filepath.Join(a.options.Dir, lockoutFile)); err != nil && !os.IsNotExist(err) {
		a.logf("sign-in lockout state could not be cleared: %v", err)
	}
}

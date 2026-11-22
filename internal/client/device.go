package client

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
)

// DefaultDeviceIDFile is where the daemon keeps the machine's stable device
// identity, outside the app config dir so it survives an app reinstall. The
// path is OS-dependent; platform files may override it (see device_windows.go).
var DefaultDeviceIDFile = "/usr/local/snet/device.id"

// deviceIDBytes is the amount of entropy in a generated device identity.
// 16 bytes (128 bits) makes accidental collisions and guessing infeasible.
const deviceIDBytes = 16

var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)

// deviceIDGen generates a fresh identity when no persisted identity exists.
// Tests override it only to make IDs deterministic; production always uses
// crypto/rand through newDeviceID.
var deviceIDGen = newDeviceID

// newDeviceID returns a random 128-bit device identity encoded as 32 hex
// characters. Device identity is deliberately NOT derived from hardware:
// cloned machines (VMs, disk images, golden images) share hardware
// identifiers, and two physical devices must never share an identity.
func newDeviceID() (string, error) {
	b := make([]byte, deviceIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validDeviceID(id string) bool {
	return deviceIDRe.MatchString(id)
}

// LoadOrCreateDeviceID returns this machine's stable device identity. The
// on-disk file is authoritative. When it is missing, a previously persisted
// config value is reused (and written back to the file), then SNET_DEVICE_ID,
// and finally a fresh cryptographically random identity is generated.
func LoadOrCreateDeviceID(deviceIDFile, existing string) (string, error) {
	id, _, err := loadOrCreateDeviceID(deviceIDFile, existing)
	return id, err
}

// loadOrCreateDeviceID also reports whether this call generated a brand-new
// identity, so the caller can perform a server-side collision check before
// using it.
func loadOrCreateDeviceID(deviceIDFile, existing string) (id string, created bool, err error) {
	if deviceIDFile != "" {
		if b, readErr := os.ReadFile(deviceIDFile); readErr == nil {
			if stored := storedDeviceID(b); stored != "" {
				return stored, false, nil
			}
			log.Printf("device id file %s contains no usable identity; generating a new one", deviceIDFile)
		} else if !os.IsNotExist(readErr) {
			return "", false, fmt.Errorf("read device id: %w", readErr)
		}
	}

	if existing = strings.TrimSpace(existing); existing != "" {
		if !validDeviceID(existing) {
			return "", false, fmt.Errorf("invalid persisted device id %q", existing)
		}
		if err := persistDeviceID(deviceIDFile, existing); err != nil {
			return "", false, err
		}
		return existing, false, nil
	}

	if env := strings.TrimSpace(os.Getenv("SNET_DEVICE_ID")); env != "" {
		if !validDeviceID(env) {
			return "", false, fmt.Errorf("invalid SNET_DEVICE_ID %q", env)
		}
		if err := persistDeviceID(deviceIDFile, env); err != nil {
			return "", false, err
		}
		return env, false, nil
	}

	for attempt := 0; attempt < 8; attempt++ {
		id, err = deviceIDGen()
		if err != nil {
			return "", false, err
		}
		if !validDeviceID(id) {
			continue
		}
		if err := persistDeviceID(deviceIDFile, id); err != nil {
			return "", false, err
		}
		return id, true, nil
	}
	return "", false, fmt.Errorf("generate device id: generator produced no valid id")
}

func storedDeviceID(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	lines := strings.SplitN(s, "\n", 2)
	id := strings.TrimSpace(lines[0])
	if !validDeviceID(id) {
		return ""
	}
	return id
}

func persistDeviceID(deviceIDFile, id string) error {
	if deviceIDFile == "" {
		return nil
	}
	if err := writeFile0600(deviceIDFile, []byte(id+"\n")); err != nil {
		return fmt.Errorf("persist device id: %w", err)
	}
	return nil
}

// LoadDeviceKeypair reads the persisted device identity, private key and
// recovery token from the device ID file. Returns empty strings when no file
// is stored (legacy client or first start). The file format is:
//
//	line 1: device ID
//	line 2: hex-encoded WireGuard private key (optional)
//	line 3: device recovery token (optional, v3+ clients)
func LoadDeviceKeypair(deviceIDFile string) (deviceID, privateKey string, err error) {
	deviceID, privateKey, _, err = LoadDeviceIdentity(deviceIDFile)
	return deviceID, privateKey, err
}

// LoadDeviceIdentity reads every value persisted in the device ID file.
func LoadDeviceIdentity(deviceIDFile string) (deviceID, privateKey, recoveryToken string, err error) {
	if deviceIDFile == "" {
		return "", "", "", nil
	}
	b, err := os.ReadFile(deviceIDFile)
	if err != nil {
		return "", "", "", nil // file missing is not an error
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", "", "", nil
	}
	lines := strings.Split(s, "\n")
	deviceID = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		privateKey = strings.TrimSpace(lines[1])
	}
	if len(lines) > 2 {
		recoveryToken = strings.TrimSpace(lines[2])
	}
	return deviceID, privateKey, recoveryToken, nil
}

// SaveDeviceKeypair persists the device identity, private key and optional
// recovery token to the device ID file, replacing any previous content. An
// empty private key or token is written as an empty line so later lines keep
// their position and the file remains parseable by older clients.
func SaveDeviceKeypair(deviceIDFile, deviceID, privateKey string, recoveryToken ...string) error {
	if deviceIDFile == "" {
		return nil
	}
	token := ""
	if len(recoveryToken) > 0 {
		token = strings.TrimSpace(recoveryToken[0])
	}
	content := deviceID + "\n" + privateKey + "\n"
	if token != "" {
		content += token + "\n"
	}
	if err := writeFile0600(deviceIDFile, []byte(content)); err != nil {
		return fmt.Errorf("persist device identity: %w", err)
	}
	return nil
}

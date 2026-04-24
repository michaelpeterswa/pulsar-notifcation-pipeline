package pulsarlib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apache/pulsar-client-go/pulsar/crypto"
)

// NewFileCryptoKeyReader returns a crypto.KeyReader that loads public
// and private keys from PEM files under dir, using the naming convention
//
//	<name>.pub.pem   — public key material
//	<name>.priv.pem  — private key material
//
// where <name> matches the key name the producer encrypts to (see
// WithEncryptionKeyNames) and the consumer decrypts with. Loading is lazy:
// files are read per request. Missing files yield a descriptive error at
// encrypt/decrypt time rather than at constructor time.
//
// Per FR-017 the on-disk files are the sole source of truth; rotation is a
// documented ops procedure and the writer may be configured with more than
// one active public key name simultaneously.
func NewFileCryptoKeyReader(dir string) crypto.KeyReader {
	return &fileKeyReader{dir: dir}
}

type fileKeyReader struct {
	dir string
}

func (r *fileKeyReader) PublicKey(keyName string, _ map[string]string) (*crypto.EncryptionKeyInfo, error) {
	path := filepath.Join(r.dir, keyName+".pub.pem")
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pulsarlib: load public key %q from %s: %w", keyName, path, err)
	}
	return crypto.NewEncryptionKeyInfo(keyName, buf, map[string]string{}), nil
}

func (r *fileKeyReader) PrivateKey(keyName string, _ map[string]string) (*crypto.EncryptionKeyInfo, error) {
	path := filepath.Join(r.dir, keyName+".priv.pem")
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pulsarlib: load private key %q from %s: %w", keyName, path, err)
	}
	return crypto.NewEncryptionKeyInfo(keyName, buf, map[string]string{}), nil
}

// KeySet is an operator-facing snapshot of currently active encryption keys.
// Useful for the writer to assert all active key names are readable at
// startup.
type KeySet struct {
	Dir         string
	ActiveNames []string
}

// LoadKeySet verifies that every name in activeNames has a readable
// corresponding <name>.pub.pem file under dir and returns a KeySet. Returns
// the first missing-key error encountered.
func LoadKeySet(dir string, activeNames []string) (*KeySet, error) {
	if len(activeNames) == 0 {
		return nil, errors.New("pulsarlib: LoadKeySet requires at least one active key name")
	}
	for _, name := range activeNames {
		if _, err := os.ReadFile(filepath.Join(dir, name+".pub.pem")); err != nil {
			return nil, fmt.Errorf("pulsarlib: active public key %q unreadable: %w", name, err)
		}
	}
	return &KeySet{Dir: dir, ActiveNames: append([]string(nil), activeNames...)}, nil
}

// Compile-time assertion that *fileKeyReader satisfies the Pulsar SDK
// interface.
var _ crypto.KeyReader = (*fileKeyReader)(nil)

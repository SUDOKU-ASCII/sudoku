package tunnel

import "crypto/sha256"

// DeriveBoostAESKey deterministically derives a 32-byte key for the
// high-bandwidth codec from the shared seed.
func DeriveBoostAESKey(seed string) []byte {
	sum := sha256.Sum256([]byte(seed + "|hb-aes"))
	return sum[:]
}

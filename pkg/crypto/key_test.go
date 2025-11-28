package crypto

import (
	"encoding/hex"
	"testing"

	"filippo.io/edwards25519"
)

func TestKeyDerivation(t *testing.T) {
	// 1. Generate Master Key
	pair, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey failed: %v", err)
	}

	masterPubHex := EncodePoint(pair.Public)
	t.Logf("Master Public: %s", masterPubHex)

	// 2. Split Key
	splitKeyHex, err := SplitPrivateKey(pair.Private)
	if err != nil {
		t.Fatalf("SplitPrivateKey failed: %v", err)
	}
	t.Logf("Split Key: %s", splitKeyHex)

	// 3. Recover Public Key from Split Key
	recoveredPub, err := RecoverPublicKey(splitKeyHex)
	if err != nil {
		t.Fatalf("RecoverPublicKey failed: %v", err)
	}
	recoveredPubHex := EncodePoint(recoveredPub)
	t.Logf("Recovered Public: %s", recoveredPubHex)

	// 4. Verify Equality
	if masterPubHex != recoveredPubHex {
		t.Errorf("Public Keys do not match!\nMaster: %s\nRecovered: %s", masterPubHex, recoveredPubHex)
	}

	// 5. Verify Recover from Master Scalar
	masterScalarHex := EncodeScalar(pair.Private)
	recoveredFromMaster, err := RecoverPublicKey(masterScalarHex)
	if err != nil {
		t.Fatalf("RecoverPublicKey(Master) failed: %v", err)
	}
	if EncodePoint(recoveredFromMaster) != masterPubHex {
		t.Errorf("Recovered from Master Scalar does not match!")
	}

	// 6. Test RecoverPublicKey from origin masterScalarHex
	pair, _ = GenerateMasterKey()
	X := EncodeScalar(pair.Private)
	recoveredFromOrigin, err := RecoverPublicKey(X)
	if err != nil {
		t.Fatalf("RecoverPublicKey(Origin) failed: %v", err)
	} else {
		t.Logf("Recovered from Origin %s :\n %s", EncodePoint(pair.Public), EncodePoint(recoveredFromOrigin))
	}

}

func TestHomomorphicProperty(t *testing.T) {
	// Verify P = (r + k)G
	pair, _ := GenerateMasterKey()

	splitHex, _ := SplitPrivateKey(pair.Private)
	splitBytes, _ := hex.DecodeString(splitHex)

	rBytes := splitBytes[:32]
	kBytes := splitBytes[32:]

	r, _ := edwards25519.NewScalar().SetCanonicalBytes(rBytes)
	k, _ := edwards25519.NewScalar().SetCanonicalBytes(kBytes)

	// sum = r + k
	sum := new(edwards25519.Scalar).Add(r, k)

	// P' = sum * G
	P_prime := new(edwards25519.Point).ScalarBaseMult(sum)

	if EncodePoint(P_prime) != EncodePoint(pair.Public) {
		t.Errorf("Homomorphic property failed!")
	}
}

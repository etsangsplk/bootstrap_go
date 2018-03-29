package crypto

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"

	"github.com/ishbir/elliptic"
)

type PrivateKey *elliptic.PrivateKey
type PublicKey *elliptic.PublicKey

func GetPubKeyHex(privateKey PrivateKey) string {
	privKey := (*elliptic.PrivateKey)(privateKey)
	return hex.EncodeToString(privKey.PublicKey.X) + hex.EncodeToString(privKey.PublicKey.Y)
}

func GeneratePrivateKey(difficulty int) PrivateKey {
	for true {
		privKey, err := elliptic.GeneratePrivateKey(elliptic.Secp256k1)
		if err != nil {
			fmt.Println("Error while generating private key", err)
			return nil
		}

		xPart := make([]byte, len(privKey.PublicKey.X))
		copy(xPart, privKey.PublicKey.X)
		pubKey := sha256.Sum256(append(xPart, privKey.PublicKey.Y...))

		nullBytes := difficulty / 8
		remainder := uint(difficulty % 8)
		isDifficult := true

		for i := 0; i < nullBytes; i++ {
			if pubKey[i] != 0 {
				isDifficult = false
				break
			}
		}

		if isDifficult {
			if pubKey[nullBytes] < (1 << (8 - remainder)) {
				keyData := GetPubKeyHex(privKey)
				ioutil.WriteFile("identity", []byte(keyData), 0600)
				return privKey
			}
		}
	}
	return nil
}

func PublicKeyFromBytes(b []byte) (PublicKey, error) {
	return elliptic.PublicKeyFromUncompressedBytes(elliptic.Secp256k1, b)
}

func EncryptPython(privateKey PrivateKey, data []byte, pubkey *elliptic.PublicKey) ([]byte, error) {
	key := (*elliptic.PrivateKey)(privateKey)
	ecdhKey, err := key.GetRawECDHKey(pubkey, 32)
	if err != nil {
		return nil, errors.New("failed to get ECDH key: " + err.Error())
	}

	cipher, err := elliptic.GetCipherByName("aes-128-ctr")
	if err != nil {
		return nil, errors.New("failed to get cipher: " + err.Error())
	}

	derivedKey := eciesKDF(ecdhKey, 32)
	key_e := derivedKey[:16]
	key_m := derivedKey[16:]
	key_m_tmp := sha256.Sum256(key_m)
	key_m = key_m_tmp[:]

	iv := make([]byte, cipher.IVSize())
	_, err = rand.Read(iv)
	if err != nil {
		return nil, errors.New("failed to get random bytes: " + err.Error())
	}

	ctx, err := elliptic.NewEncryptionCipherCtx(cipher, key_e, iv)
	if err != nil {
		return nil, errors.New("failed to create cipher ctx: " + err.Error())
	}

	encData, err := ctx.Encrypt(data)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	b.Write([]byte{0x04})
	b.Write(key.PublicKey.X)
	b.Write(key.PublicKey.Y)
	b.Write(iv)
	b.Write(encData)

	hm := hmac.New(sha256.New, key_m)
	hm.Write(b.Bytes()[1+64:])
	mac := hm.Sum(nil)

	b.Write(mac)

	return b.Bytes(), nil
}

func DecryptPython(privateKey PrivateKey, raw []byte) ([]byte, error) {
	key := (*elliptic.PrivateKey)(privateKey)
	cipher, err := elliptic.GetCipherByName("aes-128-ctr")
	if err != nil {
		return nil, errors.New("failed to get cipher: " + err.Error())
	}

	b := bytes.NewReader(raw)

	header4 := make([]byte, 1)
	_, err = b.Read(header4)
	if err != nil || header4[0] != 4 {
		return nil, errors.New("failed to read header")
	}

	pubkey := new(elliptic.PublicKey)
	pubkey.Curve = elliptic.Secp256k1
	pubkey.X = make([]byte, 32)
	pubkey.Y = make([]byte, 32)
	_, err = b.Read(pubkey.X)
	if err != nil {
		return nil, err
	}
	_, err = b.Read(pubkey.Y)
	if err != nil {
		return nil, err
	}

	iv := make([]byte, cipher.IVSize())
	_, err = b.Read(iv)
	if err != nil {
		return nil, errors.New("failed to read iv")
	}

	ciphertext := make([]byte, b.Len()-sha256.Size)
	_, err = b.Read(ciphertext)
	if err != nil {
		return nil, errors.New("failed to read ciphertext")
	}

	messageMAC := make([]byte, sha256.Size)
	_, err = b.Read(messageMAC)
	if err != nil {
		return nil, errors.New("failed to read mac")
	}

	ecdhKey, err := key.GetRawECDHKey(pubkey, 32)
	if err != nil {
		return nil, errors.New("failed to get ECDH key: " + err.Error())
	}
	derivedKey := eciesKDF(ecdhKey, 32)
	key_e := derivedKey[:16]
	key_m := derivedKey[16:]
	key_m_tmp := sha256.Sum256(key_m)
	key_m = key_m_tmp[:]

	hm := hmac.New(sha256.New, key_m)
	hm.Write(raw[1+64 : len(raw)-32])
	expectedMAC := hm.Sum(nil)

	if !hmac.Equal(expectedMAC, messageMAC) {
		return nil, elliptic.InvalidMACError
	}

	ctx, err := elliptic.NewDecryptionCipherCtx(cipher, key_e, iv)
	if err != nil {
		return nil, errors.New("failed to create cipher ctx: " + err.Error())
	}

	data, err := ctx.Decrypt(ciphertext)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func eciesKDF(keyMaterial []byte, keyLen int) []byte {
	s1 := make([]byte, 0)
	key := make([]byte, 0)
	hashBlocksize := 64
	reps := ((keyLen + 7) * 8) / (hashBlocksize * 8)
	counter := 0
	buf := make([]byte, 4)
	for counter <= reps {
		counter += 1
		ctx := sha256.New()
		binary.BigEndian.PutUint32(buf, uint32(counter))
		ctx.Write(buf)
		ctx.Write(keyMaterial)
		ctx.Write(s1)
		key = append(key, ctx.Sum(nil)...)
	}
	return key[:keyLen]
}

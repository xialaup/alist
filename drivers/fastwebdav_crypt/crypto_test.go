package fastwebdav_crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"testing"

	"golang.org/x/crypto/pbkdf2"
)

func TestAESCTRMatchesRustReference(t *testing.T) {
	plain := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-fastwebdav")
	cases := []struct {
		password string
		size     int64
		offset   int64
		length   int
	}{
		{password: "secret", size: 123456789, offset: 0, length: 32},
		{password: "secret", size: 123456789, offset: 7, length: 40},
		{password: "secret", size: 123456789, offset: 16, length: 48},
		{password: "another-pass", size: 9876543210, offset: 31, length: 33},
		{password: "edge", size: 42, offset: int64(0xffffffff*16 + 32), length: 32},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("password=%s/size=%d/offset=%d", tc.password, tc.size, tc.offset)
		t.Run(name, func(t *testing.T) {
			encrypted := rustReferenceCrypt(tc.password, tc.size, tc.offset, plain[:tc.length])
			rc := decryptReader(tc.password, tc.size, tc.offset, io.NopCloser(bytes.NewReader(encrypted)))
			decrypted, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decrypted, plain[:tc.length]) {
				t.Fatalf("decrypt mismatch\n got: %x\nwant: %x", decrypted, plain[:tc.length])
			}
		})
	}
}

func TestMixBase64MatchesRustReference(t *testing.T) {
	passwords := []string{
		"secret",
		aesPasswordOutward("secret"),
		"0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-~",
	}
	encodedNames := []string{
		"AAAA",
		"BBBB",
		"QUJDRA++",
		"MTIzNDU2",
	}
	for _, password := range passwords {
		for _, encoded := range encodedNames {
			want, wantErr := rustReferenceMixDecode(password, encoded)
			got, gotErr := mixBase64Decode(password, encoded)
			if (gotErr != nil) != (wantErr != nil) {
				t.Fatalf("error mismatch for password=%q encoded=%q: got %v want %v", password, encoded, gotErr, wantErr)
			}
			if gotErr == nil && got != want {
				t.Fatalf("decode mismatch for password=%q encoded=%q: got %q want %q", password, encoded, got, want)
			}
		}
	}
}

func TestMixSecretMatchesRustReference(t *testing.T) {
	passwords := []string{"secret", aesPasswordOutward("secret"), "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-~"}
	for _, password := range passwords {
		got := mixSecret(password)
		want := rustReferenceMixSecret(password)
		if got != want {
			t.Fatalf("secret mismatch for %q\n got: %s\nwant: %s", password, got, want)
		}
	}
}

func rustReferenceCrypt(password string, size int64, offset int64, in []byte) []byte {
	passwdOutward := hex.EncodeToString(pbkdf2.Key([]byte(password), []byte("AES-CTR"), 1000, 16, sha256.New))
	key := md5.Sum([]byte(fmt.Sprintf("%s%d", passwdOutward, size)))
	iv := md5.Sum([]byte(fmt.Sprintf("%d", size)))
	shiftedIV := rustReferenceIncrementIV(iv[:], uint64(offset/aes.BlockSize))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err)
	}
	stream := cipher.NewCTR(block, shiftedIV)
	if skip := offset % aes.BlockSize; skip > 0 {
		dummy := make([]byte, skip)
		stream.XORKeyStream(dummy, dummy)
	}
	out := append([]byte(nil), in...)
	stream.XORKeyStream(out, out)
	return out
}

func rustReferenceIncrementIV(iv []byte, increment uint64) []byte {
	out := append([]byte(nil), iv...)
	const maxUint32 = uint64(0xffffffff)
	incrementBig := increment / maxUint32
	incrementLittle := (increment % maxUint32) - incrementBig
	overflow := uint64(0)
	for idx := 0; idx < 4; idx++ {
		pos := 12 - idx*4
		num := uint64(uint32(out[pos])<<24 | uint32(out[pos+1])<<16 | uint32(out[pos+2])<<8 | uint32(out[pos+3]))
		inc := overflow
		if idx == 0 {
			inc += incrementLittle
		}
		if idx == 1 {
			inc += incrementBig
		}
		num += inc
		numBig := num / maxUint32
		numLittle := (num % maxUint32) - numBig
		overflow = numBig
		out[pos] = byte((numLittle >> 24) & 0xff)
		out[pos+1] = byte((numLittle >> 16) & 0xff)
		out[pos+2] = byte((numLittle >> 8) & 0xff)
		out[pos+3] = byte(numLittle & 0xff)
	}
	return out
}

func rustReferenceMixSecret(password string) string {
	if len(password) == len(mixBase64Source) {
		return password
	}
	key := sha256.Sum256([]byte(password + "mix64"))
	sbox := make([]byte, len(mixBase64Source))
	for i := range sbox {
		sbox[i] = byte(i)
	}
	j := 0
	for i := range sbox {
		is := int(sbox[i])
		ks := int(key[i%len(key)])
		j = (j + is + ks) % len(sbox)
		temp := is
		sbox[i] = sbox[j]
		sbox[j] = byte(temp)
	}
	buf := make([]byte, len(sbox))
	for i, v := range sbox {
		buf[i] = mixBase64Source[v]
	}
	return string(buf)
}

func rustReferenceMixDecode(password, encoded string) (string, error) {
	if len(encoded)%4 != 0 {
		return "", fmt.Errorf("invalid length")
	}
	secret := rustReferenceMixSecret(password)
	indexes := make(map[byte]byte, len(secret))
	for i := 0; i < len(secret); i++ {
		indexes[secret[i]] = byte(i)
	}
	var buf bytes.Buffer
	for i := 0; i < len(encoded); i += 4 {
		enc1, ok := indexes[encoded[i]]
		if !ok {
			return "", fmt.Errorf("invalid char")
		}
		enc2, ok := indexes[encoded[i+1]]
		if !ok {
			return "", fmt.Errorf("invalid char")
		}
		enc3, ok := indexes[encoded[i+2]]
		if !ok {
			return "", fmt.Errorf("invalid char")
		}
		enc4, ok := indexes[encoded[i+3]]
		if !ok {
			return "", fmt.Errorf("invalid char")
		}
		buf.WriteByte((enc1 << 2) | (enc2 >> 4))
		if enc3 != 64 {
			buf.WriteByte(((enc2 & 15) << 4) | (enc3 >> 2))
		}
		if enc4 != 64 {
			buf.WriteByte(((enc3 & 3) << 6) | enc4)
		}
	}
	return buf.String(), nil
}

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
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const mixBase64Source = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-~+"

func aesPasswordOutward(password string) string {
	key := pbkdf2.Key([]byte(password), []byte("AES-CTR"), 1000, 16, sha256.New)
	return hex.EncodeToString(key)
}

func newFastwebdavCTR(password, sizeSalt string, offset int64) (cipher.Stream, error) {
	passwdOutward := aesPasswordOutward(password)
	keySum := md5.Sum([]byte(passwdOutward + sizeSalt))
	ivSum := md5.Sum([]byte(sizeSalt))
	iv := incrementIVLikeRust(ivSum[:], uint64(offset/aes.BlockSize))

	block, err := aes.NewCipher(keySum[:])
	if err != nil {
		return nil, err
	}
	stream := cipher.NewCTR(block, iv)
	if skip := offset % aes.BlockSize; skip > 0 {
		dummy := make([]byte, skip)
		stream.XORKeyStream(dummy, dummy)
	}
	return stream, nil
}

func incrementIVLikeRust(iv []byte, increment uint64) []byte {
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

func decryptReader(password string, size int64, offset int64, r io.ReadCloser) io.ReadCloser {
	stream, err := newFastwebdavCTR(password, fmt.Sprintf("%d", size), offset)
	if err != nil {
		_ = r.Close()
		return errReadCloser{err: err}
	}
	return &cipherReadCloser{reader: r, stream: stream}
}

type cipherReadCloser struct {
	reader io.ReadCloser
	stream cipher.Stream
}

func (r *cipherReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.stream.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (r *cipherReadCloser) Close() error {
	return r.reader.Close()
}

type errReadCloser struct {
	err error
}

func (r errReadCloser) Read(_ []byte) (int, error) {
	return 0, r.err
}

func (r errReadCloser) Close() error {
	return nil
}

func mixSecret(password string) string {
	if len(password) == len(mixBase64Source) {
		return password
	}
	seed := sha256.Sum256([]byte(password + "mix64"))
	sbox := make([]byte, len(mixBase64Source))
	for i := range sbox {
		sbox[i] = byte(i)
	}
	j := 0
	for i := range sbox {
		j = (j + int(sbox[i]) + int(seed[i%len(seed)])) % len(sbox)
		sbox[i], sbox[j] = sbox[j], sbox[i]
	}
	var b strings.Builder
	b.Grow(len(sbox))
	for _, v := range sbox {
		b.WriteByte(mixBase64Source[v])
	}
	return b.String()
}

func mixBase64Decode(password, encoded string) (string, error) {
	secret := mixSecret(password)
	if len(encoded)%4 != 0 {
		return "", fmt.Errorf("invalid fastwebdav encoded name length")
	}
	indexes := make(map[byte]byte, len(secret))
	for i := 0; i < len(secret); i++ {
		indexes[secret[i]] = byte(i)
	}
	buf := bytes.NewBuffer(make([]byte, 0, len(encoded)/4*3))
	for i := 0; i < len(encoded); i += 4 {
		enc1, ok := indexes[encoded[i]]
		if !ok {
			return "", fmt.Errorf("invalid fastwebdav encoded char")
		}
		enc2, ok := indexes[encoded[i+1]]
		if !ok {
			return "", fmt.Errorf("invalid fastwebdav encoded char")
		}
		enc3, ok := indexes[encoded[i+2]]
		if !ok {
			return "", fmt.Errorf("invalid fastwebdav encoded char")
		}
		enc4, ok := indexes[encoded[i+3]]
		if !ok {
			return "", fmt.Errorf("invalid fastwebdav encoded char")
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

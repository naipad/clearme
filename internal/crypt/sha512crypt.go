// Package crypt 实现 SHA-512 crypt(3)，用于生成 preseed 的密码哈希。
// 参考 Ulrich Drepper 的 sha-crypt 规范。
package crypt

import (
	"crypto/rand"
	"crypto/sha512"
)

const b64Alphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var b64IndexOrder = []int{
	0, 21, 42, 22, 43, 1, 44, 2, 23, 3, 24, 45, 25, 46, 4, 47, 5, 26,
	6, 27, 48, 28, 49, 7, 50, 8, 29, 9, 30, 51, 31, 52, 10, 53, 11, 32,
	12, 33, 54, 34, 55, 13, 56, 14, 35, 15, 36, 57, 37, 58, 16, 59, 17, 38,
	18, 39, 60, 40, 61, 19, 62, 20, 41,
}

// SHA512Crypt 返回 password 使用 salt 计算出的 SHA-512 crypt(3) 哈希。
// 轮数固定为标准默认值 5000。
func SHA512Crypt(password, salt string) string {
	if len(salt) > 16 {
		salt = salt[:16]
	}
	key := []byte(password)
	s := []byte(salt)

	// B = SHA512(key, salt, key)
	h := sha512.New()
	h.Write(key)
	h.Write(s)
	h.Write(key)
	b := h.Sum(nil)

	// A 的计算：交替写入 B 和 key。
	a := sha512.New()
	a.Write(key)
	a.Write(s)
	// 重复写入 B，凑够 len(key) 字节。
	a.Write(repeatTo(b, len(key)))
	for cnt := len(key); cnt > 0; cnt >>= 1 {
		if cnt&1 != 0 {
			a.Write(b)
		} else {
			a.Write(key)
		}
	}
	ad := a.Sum(nil)

	// DP：按密码长度重复 key 后取哈希。
	dp := sha512.New()
	for i := 0; i < len(key); i++ {
		dp.Write(key)
	}
	p := repeatTo(dp.Sum(nil), len(key))

	// DS：按 salt 重复次数取哈希。
	ds := sha512.New()
	for i := 0; i < 16+int(ad[0]); i++ {
		ds.Write(s)
	}
	srep := repeatTo(ds.Sum(nil), len(s))

	// 标准轮数。
	rounds := 5000
	cur := ad
	for i := 0; i < rounds; i++ {
		c := sha512.New()
		if i&1 != 0 {
			c.Write(p)
		} else {
			c.Write(cur)
		}
		if i%3 != 0 {
			c.Write(srep)
		}
		if i%7 != 0 {
			c.Write(p)
		}
		if i&1 != 0 {
			c.Write(cur)
		} else {
			c.Write(p)
		}
		cur = c.Sum(nil)
	}

	return "$6$" + salt + "$" + encode64(cur)
}

func repeatTo(src []byte, n int) []byte {
	if n == 0 {
		return nil
	}
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, src...)
	}
	return out[:n]
}

func encode64(digest []byte) string {
	out := make([]byte, 0, 86)
	for i := 0; i+2 < len(b64IndexOrder); i += 3 {
		b2 := digest[b64IndexOrder[i]]
		b1 := digest[b64IndexOrder[i+1]]
		b0 := digest[b64IndexOrder[i+2]]
		w := uint32(b2)<<16 | uint32(b1)<<8 | uint32(b0)
		for n := 0; n < 4; n++ {
			out = append(out, b64Alphabet[w&0x3f])
			w >>= 6
		}
	}
	// 最后一个字节只产生两个字符。
	w := uint32(digest[63])
	out = append(out, b64Alphabet[w&0x3f])
	out = append(out, b64Alphabet[(w>>6)&0x3f])
	return string(out)
}

// RandomSalt 返回 16 字符的 SHA-512 crypt 盐值。
func RandomSalt() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = b64Alphabet[int(buf[i])%len(b64Alphabet)]
	}
	return string(buf), nil
}

// RandomPassword 返回 n 个字符的随机密码。
func RandomPassword(n int) (string, error) {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = chars[int(buf[i])%len(chars)]
	}
	return string(buf), nil
}

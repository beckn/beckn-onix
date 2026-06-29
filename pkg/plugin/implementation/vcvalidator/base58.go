package vcvalidator

import "fmt"

// base58btc alphabet (Bitcoin).
const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var b58Index = func() [256]int8 {
	var idx [256]int8
	for i := range idx {
		idx[i] = -1
	}
	for i := 0; i < len(b58Alphabet); i++ {
		idx[b58Alphabet[i]] = int8(i)
	}
	return idx
}()

// base58Decode decodes a base58btc string into bytes, preserving leading
// '1' characters as leading zero bytes.
func base58Decode(s string) ([]byte, error) {
	if len(s) == 0 {
		return nil, fmt.Errorf("empty base58 string")
	}
	// count leading '1's -> leading zero bytes.
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	// big-number base conversion.
	out := make([]byte, 0, len(s))
	for i := zeros; i < len(s); i++ {
		c := b58Index[s[i]]
		if c < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", s[i])
		}
		carry := int(c)
		for j := 0; j < len(out); j++ {
			carry += 58 * int(out[j])
			out[j] = byte(carry & 0xff)
			carry >>= 8
		}
		for carry > 0 {
			out = append(out, byte(carry&0xff))
			carry >>= 8
		}
	}
	// out is little-endian; reverse and prepend leading zeros.
	res := make([]byte, zeros+len(out))
	for i := 0; i < len(out); i++ {
		res[zeros+i] = out[len(out)-1-i]
	}
	return res, nil
}

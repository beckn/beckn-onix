package definition

import "context"

type Decryptor interface {
	Encrypt(ctx context.Context, b []byte) []byte
}

type DecryptorProvider interface {
	New(ctx context.Context, cfg map[string]string) (Decryptor, error)
}

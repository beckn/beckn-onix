package definition

import "context"

type Encryptor interface {
	Encrypt(ctx context.Context, b []byte) []byte
	Close() error // Close for releasing resources
}

type EncryptorProvider interface {
	New(ctx context.Context, cfg map[string]string) (Encryptor, error)
}

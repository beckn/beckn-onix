package store

// factory.go — backend selection by name. The composition root asks for the
// backend the operator configured (CRAWLER_STORE_PROVIDER); it never calls a
// driver constructor itself.

// backends is the process-wide backend registry. Implementations register
// themselves in their own init() (see postgres.go).
var backends = NewRegistry[Config, Backend]()

// RegisterBackend registers a backend implementation under name.
func RegisterBackend(name string, b Builder[Config, Backend]) { backends.Register(name, b) }

// AvailableBackends lists the registered backend names (sorted).
func AvailableBackends() []string { return backends.Available() }

// NewBackend builds the named backend from cfg. The returned Backend owns its
// connection: migrate it, then Close it on shutdown. An unknown name yields an
// error naming the backends that are available.
func NewBackend(name string, cfg Config) (Backend, error) { return backends.Create(name, cfg) }
